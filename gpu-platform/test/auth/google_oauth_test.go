//go:build integration

// Phase 1 regression tests for Google OAuth identity handling: first-login creates a
// pre-onboarding user, links to an existing password account by verified email,
// rejects unverified email, is idempotent by subject, and onboarding attaches an org.
package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/google/uuid"
	"github.com/nodehive/gpu-platform/internal/identity"
)

func newSvc(t *testing.T) (identity.Service, *pgxpool.Pool, context.Context) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("gpu"), tcpostgres.WithUsername("gpu"), tcpostgres.WithPassword("gpu"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
	sqlDB, _ := sql.Open("pgx", dsn)
	_ = goose.SetDialect("postgres")
	if err := goose.Up(sqlDB, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_ = sqlDB.Close()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour), pool, ctx
}

func TestGoogleFirstLoginCreatesPreOnboardingUser(t *testing.T) {
	svc, _, ctx := newSvc(t)

	tok, u, err := svc.UpsertGoogleUser(ctx, "sub-1", "new@example.com", "New User", "http://img/a.png", true)
	if err != nil {
		t.Fatalf("first google login: %v", err)
	}
	if tok == "" {
		t.Error("expected a session token")
	}
	if u.Onboarded() {
		t.Errorf("new google user must be pre-onboarding (org_id nil), got %v", u.OrgID)
	}
	if u.AuthProvider != "google" || u.AvatarURL != "http://img/a.png" {
		t.Errorf("expected google provider + avatar, got %q %q", u.AuthProvider, u.AvatarURL)
	}

	// Idempotent by subject — same user, no duplicate.
	_, u2, err := svc.UpsertGoogleUser(ctx, "sub-1", "new@example.com", "New User", "http://img/a.png", true)
	if err != nil || u2.ID != u.ID {
		t.Fatalf("second login should return same user; got %v err=%v", u2.ID, err)
	}
}

func TestGoogleLinksExistingPasswordAccount(t *testing.T) {
	svc, pool, ctx := newSvc(t)

	// Existing password user (with an org) from normal registration.
	_, pwUser, err := svc.Register(ctx, "Acme Corp", "person@acme.com", "Person", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Google sign-in with the SAME verified email links to that account.
	_, gUser, err := svc.UpsertGoogleUser(ctx, "sub-acme", "person@acme.com", "Person G", "http://img/p.png", true)
	if err != nil {
		t.Fatalf("google link: %v", err)
	}
	if gUser.ID != pwUser.ID {
		t.Fatalf("google login must link to existing account: got %v want %v", gUser.ID, pwUser.ID)
	}
	if !gUser.Onboarded() || gUser.OrgID != pwUser.OrgID {
		t.Errorf("linked user should keep their org: got %v", gUser.OrgID)
	}
	var sub *string
	_ = pool.QueryRow(ctx, `SELECT google_sub FROM users WHERE id=$1`, pwUser.ID).Scan(&sub)
	if sub == nil || *sub != "sub-acme" {
		t.Errorf("google_sub not linked on the existing user")
	}

	// Password login still works after linking.
	if _, _, err := svc.Login(ctx, "person@acme.com", "password123"); err != nil {
		t.Errorf("password login should still work after linking: %v", err)
	}
}

func TestGoogleRejectsUnverifiedEmail(t *testing.T) {
	svc, _, ctx := newSvc(t)
	if _, _, err := svc.UpsertGoogleUser(ctx, "sub-x", "spoof@example.com", "S", "", false); err == nil {
		t.Fatal("unverified Google email must be rejected (account-takeover guard)")
	}
}

func TestOnboardingAttachesOrg(t *testing.T) {
	svc, pool, ctx := newSvc(t)

	_, u, err := svc.UpsertGoogleUser(ctx, "sub-onb", "founder@startup.com", "Founder", "", true)
	if err != nil {
		t.Fatalf("google login: %v", err)
	}
	if u.Onboarded() {
		t.Fatal("precondition: user should start pre-onboarding")
	}

	_, onb, err := svc.CreateOrgForUser(ctx, u.ID, "Startup Inc")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if !onb.Onboarded() || onb.Role != "owner" {
		t.Errorf("after onboarding expected owner with org, got role=%q org=%v", onb.Role, onb.OrgID)
	}
	// Org was fully provisioned (rate cards + welcome credit), like Register.
	var rateCards, credits int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM rate_cards WHERE org_id=$1`, onb.OrgID).Scan(&rateCards)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM credit_ledger WHERE org_id=$1`, onb.OrgID).Scan(&credits)
	if rateCards == 0 || credits == 0 {
		t.Errorf("org not fully provisioned: rate_cards=%d credits=%d", rateCards, credits)
	}

	// Second onboarding attempt is rejected (single-org-per-user).
	if _, _, err := svc.CreateOrgForUser(ctx, u.ID, "Another Org"); err == nil {
		t.Error("a user already in an org must not create a second one")
	}
	_ = uuid.Nil
}
