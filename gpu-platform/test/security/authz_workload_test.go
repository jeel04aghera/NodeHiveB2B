//go:build integration

// Regression test for S1 (cross-org workload IDOR). Proves that a workload created
// by one org is fully inaccessible to another org across every workload endpoint —
// read, events, logs, and stop — while the owning org retains access.
package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/httpapi"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/ops"
	"github.com/nodehive/gpu-platform/internal/policy"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

func TestWorkloadCrossOrgIsolation(t *testing.T) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("gpu"), tcpostgres.WithUsername("gpu"), tcpostgres.WithPassword("gpu"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
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

	// Wire the full HTTP API exactly as main.go does.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_ = log
	idSvc := identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour)
	nodeSvc := nodes.NewService(nodes.NewRepo(pool))
	billingSvc := billing.NewService(pool)
	wlSvc := workloads.NewService(pool, agentgw.NewAgentDispatcher(agentgw.GlobalDispatcher), billingSvc)
	router := httpapi.NewRouter(
		nodeSvc, idSvc, inventory.NewService(pool), wlSvc,
		telemetry.NewService(pool), billingSvc, audit.NewService(pool),
		policy.NewService(pool), ops.New(pool),
	)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// Two independent tenants.
	tokenA, userA, err := idSvc.Register(ctx, "Acme Corp", "admin@acme.test", "A", "password123")
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	tokenB, _, err := idSvc.Register(ctx, "Evil Inc", "admin@evil.test", "B", "password123")
	if err != nil {
		t.Fatalf("register B: %v", err)
	}

	// Plant a running workload (with a secret SSH password) owned by org A.
	const sshPw = "top-secret-pw-A"
	var wid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO workloads (org_id, user_id, name, image, status, ssh_password)
		 VALUES ($1, $2, 'acme-secret-job', 'ubuntu:22.04', 'running', $3) RETURNING id`,
		userA.OrgID, userA.ID, sshPw).Scan(&wid); err != nil {
		t.Fatalf("insert workload: %v", err)
	}

	do := func(method, path, token string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}

	// ── Same-org access SUCCEEDS ──────────────────────────────────────────────
	if code, body := do("GET", "/api/v1/workloads/"+wid, tokenA); code != 200 {
		t.Errorf("owner GET want 200, got %d", code)
	} else if body["ssh_password"] != sshPw {
		t.Errorf("owner GET should see ssh_password, got %v", body["ssh_password"])
	}
	if code, _ := do("GET", "/api/v1/workloads/"+wid+"/events", tokenA); code != 200 {
		t.Errorf("owner events want 200, got %d", code)
	}
	if code, _ := do("GET", "/api/v1/workloads/"+wid+"/logs", tokenA); code != 200 {
		t.Errorf("owner logs want 200, got %d", code)
	}

	// ── Cross-org access FAILS (404, never 200, no credential leak) ────────────
	for _, p := range []string{
		"/api/v1/workloads/" + wid,
		"/api/v1/workloads/" + wid + "/events",
		"/api/v1/workloads/" + wid + "/logs",
	} {
		if code, body := do("GET", p, tokenB); code != 404 {
			t.Errorf("cross-org GET %s want 404, got %d (body=%v)", p, code, body)
		}
	}

	// Cross-org STOP must be refused AND must not alter the workload.
	if code, _ := do("POST", "/api/v1/workloads/"+wid+"/stop", tokenB); code != 404 {
		t.Errorf("cross-org STOP want 404, got %d", code)
	}
	var statusAfter string
	_ = pool.QueryRow(ctx, `SELECT status FROM workloads WHERE id=$1`, wid).Scan(&statusAfter)
	if statusAfter != "running" {
		t.Errorf("cross-org STOP must not change state; status=%q (want running)", statusAfter)
	}

	// Owner CAN stop their own workload.
	if code, _ := do("POST", "/api/v1/workloads/"+wid+"/stop", tokenA); code != 204 {
		t.Errorf("owner STOP want 204, got %d", code)
	}
}
