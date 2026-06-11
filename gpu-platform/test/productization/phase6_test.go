//go:build integration

// Phase 6 productization tests: API keys (personal + service-account) authenticate,
// expire, revoke and die with org membership; email verification and password reset
// tokens are single-use, superseding and expiring; project-level isolation gates
// restricted projects on membership and hides their workloads from non-members.
package productization

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/google/uuid"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

func newStack(t *testing.T) (identity.Service, *nodes.Service, *pgxpool.Pool, context.Context) {
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
	return identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour),
		nodes.NewService(nodes.NewRepo(pool)), pool, ctx
}

// addOrgMember inserts a plain member directly (the invite flow is covered by the
// Phase 3 membership suite; here we only need a second principal in the org).
func addOrgMember(t *testing.T, pool *pgxpool.Pool, ctx context.Context, orgID uuid.UUID, email string) uuid.UUID {
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (org_id, email, name, role, password_hash) VALUES ($1,$2,'Member','member','x')
		 RETURNING id`, orgID, email).Scan(&id)
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO organization_members (org_id, user_id, role) VALUES ($1,$2,'member')`, orgID, id); err != nil {
		t.Fatalf("insert membership: %v", err)
	}
	return id
}

// ── API keys ──────────────────────────────────────────────────────────────────

func TestPersonalAPIKeyLifecycle(t *testing.T) {
	svc, _, _, ctx := newStack(t)
	_, owner, err := svc.Register(ctx, "KeyOrg", "owner@key.com", "Owner", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	raw, key, err := svc.CreateAPIKey(ctx, owner.OrgID, identity.APIKeyRequest{
		Name: "ci", OwnerUserID: &owner.ID, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if !strings.HasPrefix(raw, identity.APIKeyScheme) {
		t.Errorf("raw key %q missing %q scheme", raw, identity.APIKeyScheme)
	}
	if !strings.HasPrefix(raw, key.Prefix) || key.Status != "active" || key.ExpiresAt != nil {
		t.Errorf("key meta: prefix=%q status=%q expires=%v", key.Prefix, key.Status, key.ExpiresAt)
	}

	// The raw key authenticates as its owner (current role, same org).
	principal, err := svc.AuthenticateAPIKey(ctx, raw)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if principal.ID != owner.ID || principal.OrgID != owner.OrgID || principal.IsServiceAccount {
		t.Errorf("principal = %+v, want owner %v", principal, owner.ID)
	}

	// Member-scoped list sees it; revoke kills it.
	list, err := svc.ListAPIKeys(ctx, owner.OrgID, &owner.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v (n=%d)", err, len(list))
	}
	if err := svc.RevokeAPIKey(ctx, owner.OrgID, key.ID, &owner.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, identity.ErrAPIKeyInvalid) {
		t.Fatalf("revoked key must fail with ErrAPIKeyInvalid, got %v", err)
	}
	list, _ = svc.ListAPIKeys(ctx, owner.OrgID, &owner.ID)
	if len(list) != 1 || list[0].Status != "revoked" {
		t.Errorf("revoked key should stay listed as tombstone, got %+v", list)
	}
	// Double revoke and a junk token both fail closed.
	if err := svc.RevokeAPIKey(ctx, owner.OrgID, key.ID, &owner.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("double revoke = %v, want ErrNotFound", err)
	}
	if _, err := svc.AuthenticateAPIKey(ctx, identity.APIKeyScheme+"nope"); !errors.Is(err, identity.ErrAPIKeyInvalid) {
		t.Errorf("unknown key = %v, want ErrAPIKeyInvalid", err)
	}
}

func TestAPIKeyExpiryAndOwnerValidation(t *testing.T) {
	svc, _, _, ctx := newStack(t)
	_, u, _ := svc.Register(ctx, "ExpOrg", "e@exp.com", "E", "password123")

	raw, _, err := svc.CreateAPIKey(ctx, u.OrgID, identity.APIKeyRequest{
		Name: "short", OwnerUserID: &u.ID, CreatedBy: u.ID, TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, identity.ErrAPIKeyInvalid) {
		t.Fatalf("expired key = %v, want ErrAPIKeyInvalid", err)
	}
	list, _ := svc.ListAPIKeys(ctx, u.OrgID, &u.ID)
	if len(list) != 1 || list[0].Status != "expired" {
		t.Errorf("status = %v, want expired", list)
	}

	// Exactly one owner: neither or both must be rejected.
	if _, _, err := svc.CreateAPIKey(ctx, u.OrgID, identity.APIKeyRequest{Name: "x", CreatedBy: u.ID}); err == nil {
		t.Error("key with no owner must be rejected")
	}
	sa, _ := svc.CreateServiceAccount(ctx, u.OrgID, u.ID, "bot", "", domain.RoleMember)
	if _, _, err := svc.CreateAPIKey(ctx, u.OrgID, identity.APIKeyRequest{
		Name: "x", OwnerUserID: &u.ID, ServiceAccountID: &sa.ID, CreatedBy: u.ID,
	}); err == nil {
		t.Error("key with two owners must be rejected")
	}
}

func TestServiceAccountKeys(t *testing.T) {
	svc, _, _, ctx := newStack(t)
	_, owner, _ := svc.Register(ctx, "BotOrg", "o@bot.com", "O", "password123")

	sa, err := svc.CreateServiceAccount(ctx, owner.OrgID, owner.ID, "ci-runner", "deploys", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("create SA: %v", err)
	}
	if _, err := svc.CreateServiceAccount(ctx, owner.OrgID, owner.ID, "ci-runner", "", domain.RoleMember); !errors.Is(err, identity.ErrServiceAccountExists) {
		t.Errorf("duplicate SA = %v, want ErrServiceAccountExists", err)
	}
	// Machine identities are capped at admin — owner is downgraded to member.
	capped, _ := svc.CreateServiceAccount(ctx, owner.OrgID, owner.ID, "sneaky", "", domain.RoleOwner)
	if capped.Role != domain.RoleMember {
		t.Errorf("owner-role SA must be capped, got %v", capped.Role)
	}

	raw, _, err := svc.CreateAPIKey(ctx, owner.OrgID, identity.APIKeyRequest{
		Name: "sa-key", ServiceAccountID: &sa.ID, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("create SA key: %v", err)
	}
	principal, err := svc.AuthenticateAPIKey(ctx, raw)
	if err != nil {
		t.Fatalf("authenticate SA key: %v", err)
	}
	if !principal.IsServiceAccount || principal.Role != domain.RoleAdmin || principal.OrgID != owner.OrgID {
		t.Errorf("SA principal = %+v", principal)
	}

	// Disabling the SA invalidates its keys instantly; re-enabling restores them.
	if err := svc.SetServiceAccountDisabled(ctx, owner.OrgID, sa.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, identity.ErrAPIKeyInvalid) {
		t.Fatalf("disabled SA key = %v, want ErrAPIKeyInvalid", err)
	}
	if err := svc.SetServiceAccountDisabled(ctx, owner.OrgID, sa.ID, false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.AuthenticateAPIKey(ctx, raw); err != nil {
		t.Fatalf("re-enabled SA key should authenticate: %v", err)
	}
}

func TestAPIKeyCrossOrgAndMembership(t *testing.T) {
	svc, _, pool, ctx := newStack(t)
	_, alice, _ := svc.Register(ctx, "OrgA", "alice@a.com", "Alice", "password123")
	_, bob, _ := svc.Register(ctx, "OrgB", "bob@b.com", "Bob", "password123")

	raw, key, _ := svc.CreateAPIKey(ctx, alice.OrgID, identity.APIKeyRequest{
		Name: "a-key", OwnerUserID: &alice.ID, CreatedBy: alice.ID,
	})

	// Org B cannot revoke (or even see) org A's key.
	if err := svc.RevokeAPIKey(ctx, bob.OrgID, key.ID, nil); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("cross-org revoke = %v, want ErrNotFound", err)
	}
	if list, _ := svc.ListAPIKeys(ctx, bob.OrgID, nil); len(list) != 0 {
		t.Errorf("org B sees %d foreign keys", len(list))
	}

	// A member-scoped revoke cannot touch someone else's key.
	member := addOrgMember(t, pool, ctx, alice.OrgID, "m@a.com")
	if err := svc.RevokeAPIKey(ctx, alice.OrgID, key.ID, &member); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("foreign-owner revoke = %v, want ErrNotFound", err)
	}

	// The key reflects the owner's CURRENT role…
	if _, err := pool.Exec(ctx, `UPDATE users SET role='member' WHERE id=$1`, alice.ID); err != nil {
		t.Fatal(err)
	}
	principal, err := svc.AuthenticateAPIKey(ctx, raw)
	if err != nil || principal.Role != domain.RoleMember {
		t.Fatalf("demoted owner's key: role=%v err=%v, want member", principal.Role, err)
	}
	// …and dies when the owner leaves the org.
	if _, err := pool.Exec(ctx, `UPDATE users SET org_id=NULL WHERE id=$1`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, identity.ErrAPIKeyInvalid) {
		t.Fatalf("orphaned key = %v, want ErrAPIKeyInvalid", err)
	}
}

// ── Email verification & password reset ───────────────────────────────────────

func TestEmailVerificationFlow(t *testing.T) {
	svc, _, _, ctx := newStack(t)
	_, u, _ := svc.Register(ctx, "VOrg", "v@v.com", "V", "password123")
	if u.EmailVerified {
		t.Fatal("freshly registered user must start unverified")
	}

	raw1, _, err := svc.RequestEmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// A second request supersedes the first token.
	raw2, _, err := svc.RequestEmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("re-request: %v", err)
	}
	if _, err := svc.ConfirmEmailVerification(ctx, raw1); !errors.Is(err, identity.ErrAuthTokenInvalid) {
		t.Fatalf("superseded token = %v, want ErrAuthTokenInvalid", err)
	}
	verified, err := svc.ConfirmEmailVerification(ctx, raw2)
	if err != nil || !verified.EmailVerified {
		t.Fatalf("confirm: err=%v verified=%v", err, verified.EmailVerified)
	}
	// Single use; and a verified account can't request another token.
	if _, err := svc.ConfirmEmailVerification(ctx, raw2); !errors.Is(err, identity.ErrAuthTokenInvalid) {
		t.Errorf("reused token = %v, want ErrAuthTokenInvalid", err)
	}
	if _, _, err := svc.RequestEmailVerification(ctx, u.ID); !errors.Is(err, identity.ErrAlreadyVerified) {
		t.Errorf("verified re-request = %v, want ErrAlreadyVerified", err)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	svc, _, _, ctx := newStack(t)
	_, u, _ := svc.Register(ctx, "ROrg", "r@r.com", "R", "oldpassword1")
	_, _, _ = svc.CreateSession(ctx, u.ID, identity.DeviceInfo{UserAgent: "test"})

	if _, _, err := svc.RequestPasswordReset(ctx, "nobody@r.com"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("unknown email = %v, want ErrNotFound", err)
	}

	raw, _, err := svc.RequestPasswordReset(ctx, "R@R.COM") // case-insensitive lookup
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// A too-short password is rejected WITHOUT consuming the token.
	if _, err := svc.ConfirmPasswordReset(ctx, raw, "short"); err == nil {
		t.Fatal("short password must be rejected")
	}
	reset, err := svc.ConfirmPasswordReset(ctx, raw, "newpassword1")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// Reset proves mailbox control → email becomes verified.
	if !reset.EmailVerified {
		t.Error("password reset should mark the email verified")
	}
	// Old credential dead, new one works, all sessions revoked.
	if _, _, err := svc.Login(ctx, "r@r.com", "oldpassword1"); err == nil {
		t.Error("old password must stop working")
	}
	if _, _, err := svc.Login(ctx, "r@r.com", "newpassword1"); err != nil {
		t.Errorf("new password login: %v", err)
	}
	if sessions, _ := svc.ListSessions(ctx, u.ID); len(sessions) != 0 {
		t.Errorf("reset must revoke all sessions, %d remain", len(sessions))
	}
	// Token is single use.
	if _, err := svc.ConfirmPasswordReset(ctx, raw, "anotherpass1"); !errors.Is(err, identity.ErrAuthTokenInvalid) {
		t.Errorf("reused reset token = %v, want ErrAuthTokenInvalid", err)
	}
}

func TestAuthTokenExpiry(t *testing.T) {
	svc, _, pool, ctx := newStack(t)
	_, u, _ := svc.Register(ctx, "TOrg", "t@t.com", "T", "password123")

	raw, _, err := svc.RequestPasswordReset(ctx, "t@t.com")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE auth_tokens SET expires_at = now() - interval '1 minute' WHERE user_id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmPasswordReset(ctx, raw, "newpassword1"); !errors.Is(err, identity.ErrAuthTokenInvalid) {
		t.Fatalf("expired token = %v, want ErrAuthTokenInvalid", err)
	}
}

// ── Project isolation ─────────────────────────────────────────────────────────

func TestProjectIsolation(t *testing.T) {
	svc, projects, pool, ctx := newStack(t)
	_, owner, _ := svc.Register(ctx, "IsoOrg", "own@iso.com", "Own", "password123")
	member := addOrgMember(t, pool, ctx, owner.OrgID, "mem@iso.com")
	_, outsider, _ := svc.Register(ctx, "OtherOrg", "out@other.com", "Out", "password123")

	p, err := projects.CreateProject(ctx, owner.OrgID, "Client X", "isolated client work", owner.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if p.Visibility != "open" {
		t.Fatalf("default visibility = %q, want open", p.Visibility)
	}
	// Open project: any org member may use it.
	if err := projects.AuthorizeProjectUse(ctx, owner.OrgID, p.ID, member, false); err != nil {
		t.Fatalf("open project should admit members: %v", err)
	}

	restricted := "restricted"
	if _, err := projects.UpdateProject(ctx, owner.OrgID, p.ID, nil, nil, &restricted); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	// Restricted: non-member blocked, admins pass, project members pass.
	if err := projects.AuthorizeProjectUse(ctx, owner.OrgID, p.ID, member, false); !errors.Is(err, nodes.ErrProjectForbidden) {
		t.Fatalf("non-member use = %v, want ErrProjectForbidden", err)
	}
	if ok, _ := projects.CanViewProject(ctx, owner.OrgID, p.ID, member, false); ok {
		t.Error("non-member must not view a restricted project")
	}
	if err := projects.AuthorizeProjectUse(ctx, owner.OrgID, p.ID, owner.ID, true); err != nil {
		t.Errorf("admin must pass the restriction: %v", err)
	}
	if err := projects.AddProjectMember(ctx, owner.OrgID, p.ID, member, owner.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := projects.AddProjectMember(ctx, owner.OrgID, p.ID, member, owner.ID); err != nil {
		t.Errorf("re-adding a member must be idempotent: %v", err)
	}
	if err := projects.AuthorizeProjectUse(ctx, owner.OrgID, p.ID, member, false); err != nil {
		t.Errorf("project member must pass: %v", err)
	}
	if ok, _ := projects.CanViewProject(ctx, owner.OrgID, p.ID, member, false); !ok {
		t.Error("project member must view the project")
	}

	// Cross-org: a foreign user can't be granted; a foreign org can't even see it.
	if err := projects.AddProjectMember(ctx, owner.OrgID, p.ID, outsider.ID, owner.ID); !errors.Is(err, nodes.ErrNotFound) {
		t.Errorf("cross-org grant = %v, want ErrNotFound", err)
	}
	if _, err := projects.GetProject(ctx, outsider.OrgID, p.ID); !errors.Is(err, nodes.ErrNotFound) {
		t.Errorf("cross-org get = %v, want ErrNotFound", err)
	}

	// Removing the member restores the block; double-remove is NotFound.
	if err := projects.RemoveProjectMember(ctx, owner.OrgID, p.ID, member); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := projects.AuthorizeProjectUse(ctx, owner.OrgID, p.ID, member, false); !errors.Is(err, nodes.ErrProjectForbidden) {
		t.Errorf("removed member use = %v, want ErrProjectForbidden", err)
	}
	if err := projects.RemoveProjectMember(ctx, owner.OrgID, p.ID, member); !errors.Is(err, nodes.ErrNotFound) {
		t.Errorf("double remove = %v, want ErrNotFound", err)
	}

	// Archived projects refuse new use for EVERYONE (even admins); restore reopens.
	if err := projects.SetProjectArchived(ctx, owner.OrgID, p.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := projects.AuthorizeProjectUse(ctx, owner.OrgID, p.ID, owner.ID, true); !errors.Is(err, nodes.ErrProjectForbidden) {
		t.Errorf("archived use (admin) = %v, want ErrProjectForbidden", err)
	}
	if err := projects.SetProjectArchived(ctx, owner.OrgID, p.ID, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := projects.AuthorizeProjectUse(ctx, owner.OrgID, p.ID, owner.ID, true); err != nil {
		t.Errorf("restored project should admit again: %v", err)
	}
}

func TestEnsureDefaultProject(t *testing.T) {
	svc, projects, _, ctx := newStack(t)
	_, owner, _ := svc.Register(ctx, "DefOrg", "d@def.com", "D", "password123")

	// Registration's migration backfill may or may not have created it (the org is
	// new) — EnsureDefaultProject must be idempotent either way.
	id1, err := projects.EnsureDefaultProject(ctx, owner.OrgID)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	id2, err := projects.EnsureDefaultProject(ctx, owner.OrgID)
	if err != nil || id2 != id1 {
		t.Fatalf("ensure must be idempotent: %v / %v (err=%v)", id1, id2, err)
	}
	list, err := projects.ListProjects(ctx, owner.OrgID, owner.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, p := range list {
		if p.ID == id1 && p.Name == "Default" {
			found = true
		}
	}
	if !found {
		t.Errorf("Default project missing from list: %+v", list)
	}
}

// ── Workload list isolation ───────────────────────────────────────────────────

type nopDispatch struct{}

func (nopDispatch) Nudge(uuid.UUID) {}

type nopBiller struct{}

func (nopBiller) AuthorizeLaunch(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID) error {
	return nil
}
func (nopBiller) MeterWorkload(context.Context, uuid.UUID) error { return nil }

func TestWorkloadListHidesRestrictedProjects(t *testing.T) {
	svc, projects, pool, ctx := newStack(t)
	_, owner, _ := svc.Register(ctx, "WLOrg", "w@wl.com", "W", "password123")
	member := addOrgMember(t, pool, ctx, owner.OrgID, "viewer@wl.com")

	open, _ := projects.CreateProject(ctx, owner.OrgID, "Open", "", owner.ID)
	secret, _ := projects.CreateProject(ctx, owner.OrgID, "Secret", "", owner.ID)
	restricted := "restricted"
	if _, err := projects.UpdateProject(ctx, owner.OrgID, secret.ID, nil, nil, &restricted); err != nil {
		t.Fatalf("restrict: %v", err)
	}

	insert := func(name string, projectID *uuid.UUID, userID uuid.UUID) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO workloads (org_id, project_id, user_id, name, image, status)
			 VALUES ($1,$2,$3,$4,'ubuntu:22.04','running')`,
			owner.OrgID, projectID, userID, name); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}
	insert("in-open", &open.ID, owner.ID)
	insert("no-project", nil, owner.ID)
	insert("owners-secret", &secret.ID, owner.ID) // owner's wl in the restricted project
	insert("members-own-secret", &secret.ID, member)

	wls := workloads.NewService(pool, nopDispatch{}, nopBiller{})

	names := func(list []domain.Workload) map[string]bool {
		m := map[string]bool{}
		for _, w := range list {
			m[w.Name] = true
		}
		return m
	}

	// Non-admin viewer outside the restricted project: sees open + project-less +
	// their OWN workload inside the restricted project, but not the owner's.
	got, err := wls.List(ctx, owner.OrgID, workloads.ListFilter{Viewer: &member})
	if err != nil {
		t.Fatalf("list as member: %v", err)
	}
	n := names(got)
	if !n["in-open"] || !n["no-project"] || !n["members-own-secret"] {
		t.Errorf("member view missing expected workloads: %v", n)
	}
	if n["owners-secret"] {
		t.Error("member must not see a stranger's workload in a restricted project")
	}

	// Admin viewer sees everything.
	got, err = wls.List(ctx, owner.OrgID, workloads.ListFilter{Viewer: &owner.ID, ViewerIsAdmin: true})
	if err != nil || len(got) != 4 {
		t.Fatalf("admin should see all 4, got %d (err=%v)", len(got), err)
	}

	// Project membership unhides it.
	if err := projects.AddProjectMember(ctx, owner.OrgID, secret.ID, member, owner.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = wls.List(ctx, owner.OrgID, workloads.ListFilter{Viewer: &member})
	if !names(got)["owners-secret"] {
		t.Error("project member should now see the restricted project's workloads")
	}
}
