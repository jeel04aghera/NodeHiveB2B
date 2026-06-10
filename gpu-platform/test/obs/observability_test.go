//go:build integration

// Phase 5 (Operations & Observability) verification suite. Real Postgres
// (testcontainers) + real migrations. Proves: the audit log is append-only and
// tamper-evident (hash chain), audit search filters/paginates and stays org-scoped,
// the probe + diagnostics + metrics endpoints behave per spec (including the
// fail-closed production metrics gate), and request correlation IDs flow through
// HTTP responses and into audit metadata.
package obstest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/httpapi"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/obs"
	"github.com/nodehive/gpu-platform/internal/ops"
	"github.com/nodehive/gpu-platform/internal/policy"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

func setupDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
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
	return pool, dsn
}

func seedOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug) VALUES ($1,$1) RETURNING id`, slug).Scan(&id); err != nil {
		t.Fatalf("org: %v", err)
	}
	return id
}

// ── Audit: append-only + hash chain ───────────────────────────────────────────

func TestAuditTamperResistance(t *testing.T) {
	pool, _ := setupDB(t)
	ctx := context.Background()
	svc := audit.NewService(pool)
	orgID := seedOrg(t, pool, "tamper-org")

	for i := 0; i < 3; i++ {
		if err := svc.Record(ctx, domain.AuditLog{
			OrgID: orgID, ActorType: "user", ActorID: fmt.Sprintf("actor-%d", i),
			Action: "workload.launch", TargetType: "workload", TargetID: fmt.Sprintf("wl-%d", i),
			Metadata: map[string]any{"n": i},
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	// Chain intact after normal inserts.
	if bad, err := svc.VerifyChain(ctx); err != nil || len(bad) != 0 {
		t.Fatalf("fresh chain should verify, bad=%v err=%v", bad, err)
	}

	// UPDATE, DELETE and TRUNCATE are all rejected — even for the application role.
	if _, err := pool.Exec(ctx, `UPDATE audit_logs SET action='forged'`); err == nil {
		t.Fatal("UPDATE on audit_logs must be rejected")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_logs`); err == nil {
		t.Fatal("DELETE on audit_logs must be rejected")
	}
	if _, err := pool.Exec(ctx, `TRUNCATE audit_logs`); err == nil {
		t.Fatal("TRUNCATE on audit_logs must be rejected")
	}

	// A privileged attacker who disables the guard trigger and rewrites history is
	// still DETECTED: the hash chain no longer verifies.
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_logs DISABLE TRIGGER trg_audit_append_only`); err != nil {
		t.Fatalf("disable trigger (test setup): %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE audit_logs SET action='forged.action' WHERE id = (SELECT min(id) FROM audit_logs)`); err != nil {
		t.Fatalf("tamper update: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_logs ENABLE TRIGGER trg_audit_append_only`); err != nil {
		t.Fatalf("re-enable trigger: %v", err)
	}
	bad, err := svc.VerifyChain(ctx)
	if err != nil {
		t.Fatalf("verify after tamper: %v", err)
	}
	if len(bad) == 0 {
		t.Fatal("tampered row must break chain verification")
	}
	t.Logf("tamper detected at audit row(s) %v", bad)
}

// ── Audit: search filters, paging, org scoping ────────────────────────────────

func TestAuditSearch(t *testing.T) {
	pool, _ := setupDB(t)
	ctx := context.Background()
	svc := audit.NewService(pool)
	orgA := seedOrg(t, pool, "org-a")
	orgB := seedOrg(t, pool, "org-b")

	events := []domain.AuditLog{
		{OrgID: orgA, ActorType: "user", ActorID: "alice", Action: "workload.launch",
			TargetType: "workload", TargetID: "w1", Metadata: map[string]any{"name": "bert-trainer"}},
		{OrgID: orgA, ActorType: "user", ActorID: "alice", Action: "workload.stop",
			TargetType: "workload", TargetID: "w1"},
		{OrgID: orgA, ActorType: "user", ActorID: "bob", Action: "auth.login",
			TargetType: "user", TargetID: "bob-id"},
		{OrgID: orgA, ActorType: "system", ActorID: "", Action: "billing.topup",
			TargetType: "credit_ledger", Metadata: map[string]any{"amount": 500}},
		{OrgID: orgB, ActorType: "user", ActorID: "mallory", Action: "workload.launch",
			TargetType: "workload", TargetID: "wB"},
	}
	for i, e := range events {
		if err := svc.Record(ctx, e); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	// Org scoping: A sees only its 4 events; B only its 1.
	itemsA, totalA, err := svc.Query(ctx, orgA, audit.QueryFilter{})
	if err != nil || totalA != 4 || len(itemsA) != 4 {
		t.Fatalf("org A want 4 events, got total=%d len=%d err=%v", totalA, len(itemsA), err)
	}
	if _, totalB, _ := svc.Query(ctx, orgB, audit.QueryFilter{}); totalB != 1 {
		t.Fatalf("org B want 1 event, got %d", totalB)
	}

	// Action prefix match.
	if _, n, _ := svc.Query(ctx, orgA, audit.QueryFilter{Action: "workload"}); n != 2 {
		t.Errorf("action=workload want 2, got %d", n)
	}
	// Actor filter.
	if _, n, _ := svc.Query(ctx, orgA, audit.QueryFilter{ActorID: "alice"}); n != 2 {
		t.Errorf("actor=alice want 2, got %d", n)
	}
	// Target filter.
	if _, n, _ := svc.Query(ctx, orgA, audit.QueryFilter{TargetType: "workload", TargetID: "w1"}); n != 2 {
		t.Errorf("target w1 want 2, got %d", n)
	}
	// Free text hits metadata.
	if items, n, _ := svc.Query(ctx, orgA, audit.QueryFilter{Q: "bert-trainer"}); n != 1 || items[0].Action != "workload.launch" {
		t.Errorf("q=bert-trainer want the launch event, got n=%d", n)
	}
	// LIKE metacharacters in user input must not act as wildcards.
	if _, n, _ := svc.Query(ctx, orgA, audit.QueryFilter{Q: "%"}); n != 0 {
		t.Errorf("q=%% must be literal, got %d matches", n)
	}
	// Paging: limit/offset with stable total.
	p1, total, _ := svc.Query(ctx, orgA, audit.QueryFilter{Limit: 2, Offset: 0})
	p2, _, _ := svc.Query(ctx, orgA, audit.QueryFilter{Limit: 2, Offset: 2})
	if total != 4 || len(p1) != 2 || len(p2) != 2 {
		t.Fatalf("paging want 2+2 of 4, got %d+%d of %d", len(p1), len(p2), total)
	}
	if p1[0].ID == p2[0].ID {
		t.Error("pages must not overlap")
	}
}

// ── HTTP: probes, diagnostics, metrics, request IDs, audit-over-HTTP ──────────

func buildRouter(t *testing.T, pool *pgxpool.Pool, extra ...httpapi.Option) (http.Handler, identity.Service) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	idSvc := identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour)
	nodeSvc := nodes.NewService(nodes.NewRepo(pool))
	billingSvc := billing.NewService(pool)
	wlSvc := workloads.NewService(pool, agentgw.NewDeliveryEngine(pool, agentgw.GlobalDispatcher, log), billingSvc)

	jobs := obs.NewJobTracker()
	jobs.Register("test_sweep", time.Minute)
	jobs.Record("test_sweep", nil)
	health := &obs.Health{
		DB: pool, Jobs: jobs,
		ConnectedAgents: func() int { return 3 },
		Version:         "test-1", Env: "test", Started: time.Now(),
	}
	opts := append([]httpapi.Option{
		httpapi.WithLogger(log),
		httpapi.WithHealth(health),
		httpapi.WithMetrics(obs.NewMetrics(health), ""),
	}, extra...)
	router := httpapi.NewRouter(
		nodeSvc, idSvc, inventory.NewService(pool), wlSvc,
		telemetry.NewService(pool), billingSvc, audit.NewService(pool),
		policy.NewService(pool), ops.New(pool),
		opts...,
	)
	return router, idSvc
}

func TestObservabilityHTTP(t *testing.T) {
	pool, _ := setupDB(t)
	ctx := context.Background()
	router, idSvc := buildRouter(t, pool)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	get := func(path, token string, hdr map[string]string) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}

	// Liveness + readiness.
	if resp, _ := get("/healthz", "", nil); resp.StatusCode != 200 {
		t.Errorf("/healthz want 200, got %d", resp.StatusCode)
	}
	if resp, _ := get("/readyz", "", nil); resp.StatusCode != 200 {
		t.Errorf("/readyz want 200, got %d", resp.StatusCode)
	}

	// Request IDs: inbound id echoed; absent id generated.
	if resp, _ := get("/healthz", "", map[string]string{"X-Request-ID": "corr-abc-123"}); resp.Header.Get("X-Request-ID") != "corr-abc-123" {
		t.Errorf("inbound request id not echoed, got %q", resp.Header.Get("X-Request-ID"))
	}
	if resp, _ := get("/healthz", "", nil); resp.Header.Get("X-Request-ID") == "" {
		t.Error("missing request id must be generated")
	}
	// Hostile inbound ids (spaces / log-injection-prone characters) are replaced.
	hostile := "bad id $(rm -rf)"
	if resp, _ := get("/healthz", "", map[string]string{"X-Request-ID": hostile}); resp.Header.Get("X-Request-ID") == hostile {
		t.Error("unsafe request id must not be reflected")
	}

	// Metrics (dev posture: open without token).
	if resp, body := get("/metrics", "", nil); resp.StatusCode != 200 {
		t.Errorf("/metrics want 200, got %d", resp.StatusCode)
	} else {
		for _, want := range []string{"nodehive_build_info", "nodehive_agents_connected", "nodehive_http_requests_total", "nodehive_job_last_success_timestamp_seconds"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("/metrics missing %s", want)
			}
		}
	}

	// Admin diagnostics: 401 unauthenticated, 403 member, 200 owner with components.
	ownerToken, owner, err := idSvc.Register(ctx, "Obs Org", "owner@obs.test", "O", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	member, err := idSvc.CreateUser(ctx, owner.OrgID, "member@obs.test", "M", domain.RoleMember)
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	memberToken, err := idSvc.IssueAccessToken(ctx, member)
	if err != nil {
		t.Fatalf("member token: %v", err)
	}
	if resp, _ := get("/api/v1/health", "", nil); resp.StatusCode != 401 {
		t.Errorf("diagnostics unauthenticated want 401, got %d", resp.StatusCode)
	}
	if resp, _ := get("/api/v1/health", memberToken, nil); resp.StatusCode != 403 {
		t.Errorf("diagnostics as member want 403, got %d", resp.StatusCode)
	}
	resp, body := get("/api/v1/health", ownerToken, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("diagnostics as owner want 200, got %d (%s)", resp.StatusCode, body)
	}
	var rep obs.Report
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("diagnostics decode: %v", err)
	}
	if rep.Status != "ok" || !rep.Database.OK || !rep.Queue.OK || len(rep.Jobs) == 0 {
		t.Errorf("diagnostics want healthy report, got %+v", rep)
	}
	if rep.Deployment["version"] != "test-1" {
		t.Errorf("diagnostics version want test-1, got %v", rep.Deployment["version"])
	}

	// Audit over HTTP: login produced an auth.login event carrying request_id + IP,
	// and the search endpoint filters by action.
	loginBody := strings.NewReader(`{"email":"owner@obs.test","password":"password123"}`)
	lreq, _ := http.NewRequest("POST", srv.URL+"/api/v1/auth/login", loginBody)
	lreq.Header.Set("Content-Type", "application/json")
	lreq.Header.Set("X-Request-ID", "login-corr-1")
	lresp, err := http.DefaultClient.Do(lreq)
	if err != nil || lresp.StatusCode != 200 {
		t.Fatalf("login: %v code=%v", err, lresp.StatusCode)
	}
	lresp.Body.Close()

	// The audit write is async; poll briefly.
	deadline := time.Now().Add(3 * time.Second)
	var found map[string]any
	for time.Now().Before(deadline) {
		_, sb := get("/api/v1/audit-logs?action=auth.login", ownerToken, nil)
		var out struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		_ = json.Unmarshal(sb, &out)
		if out.Total >= 1 {
			found = out.Items[0]
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if found == nil {
		t.Fatal("auth.login audit event not found via search endpoint")
	}
	meta, _ := found["metadata"].(map[string]any)
	if meta["request_id"] != "login-corr-1" {
		t.Errorf("audit event should carry the request correlation id, got %v", meta)
	}
	if found["ip"] == "" {
		t.Error("audit event should carry the client IP")
	}

	// Failed login is audited too (attributed to the account's org).
	badBody := strings.NewReader(`{"email":"owner@obs.test","password":"WRONG"}`)
	bresp, _ := http.Post(srv.URL+"/api/v1/auth/login", "application/json", badBody)
	if bresp != nil {
		bresp.Body.Close()
	}
	deadline = time.Now().Add(3 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		_, sb := get("/api/v1/audit-logs?action=auth.login_failed", ownerToken, nil)
		var out struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(sb, &out)
		if out.Total >= 1 {
			ok = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Error("auth.login_failed audit event not recorded")
	}
}

func TestMetricsGate(t *testing.T) {
	pool, _ := setupDB(t)

	// Production posture, no token configured → endpoint disabled (fail closed).
	closedRouter, _ := buildRouter(t, pool,
		httpapi.WithSecureCookies(true),
	)
	closedSrv := httptest.NewServer(closedRouter)
	t.Cleanup(closedSrv.Close)
	if resp, err := http.Get(closedSrv.URL + "/metrics"); err != nil || resp.StatusCode != 404 {
		t.Errorf("prod metrics without token want 404, got %v err=%v", resp.StatusCode, err)
	}

	// Token configured → Bearer required.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_ = log
	health := &obs.Health{DB: pool, Version: "t", Env: "test", Started: time.Now()}
	gatedRouter, _ := buildRouter(t, pool,
		httpapi.WithSecureCookies(true),
		httpapi.WithMetrics(obs.NewMetrics(health), "scrape-secret"),
	)
	gatedSrv := httptest.NewServer(gatedRouter)
	t.Cleanup(gatedSrv.Close)

	if resp, _ := http.Get(gatedSrv.URL + "/metrics"); resp.StatusCode != 401 {
		t.Errorf("gated metrics without bearer want 401, got %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", gatedSrv.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer scrape-secret")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 200 {
		t.Errorf("gated metrics with bearer want 200, got %d", resp.StatusCode)
	}
}

// ── Migration 0016 down/up round-trip ─────────────────────────────────────────

func TestObservabilityMigrationRoundTrip(t *testing.T) {
	_, dsn := setupDB(t)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()
	_ = goose.SetDialect("postgres")
	if err := goose.DownTo(sqlDB, "../../migrations", 15); err != nil {
		t.Fatalf("down to 15: %v", err)
	}
	if err := goose.Up(sqlDB, "../../migrations"); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	// Triggers exist again after re-up.
	var n int
	if err := sqlDB.QueryRow(
		`SELECT count(*) FROM pg_trigger WHERE tgname IN ('trg_audit_chain','trg_audit_append_only','trg_audit_no_truncate')`).
		Scan(&n); err != nil || n != 3 {
		t.Fatalf("want 3 audit triggers after round-trip, got %d err=%v", n, err)
	}
}
