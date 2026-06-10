//go:build integration

// Regression tests for H1 (workload secret exposure) and H3 (auth rate limiting).
//
// H1: ssh_password and logs are per-workload secrets. Only the workload owner and
// org admins/owners may see them; ordinary members still see the workload itself.
// H3: credential endpoints are per-IP rate limited; a brute-force loop gets 429.
package security

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/httpapi"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/ops"
	"github.com/nodehive/gpu-platform/internal/policy"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

func TestWorkloadSecretsAndRateLimit(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)

	idSvc := identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour)
	billingSvc := billing.NewService(pool)
	router := httpapi.NewRouter(
		nodes.NewService(nodes.NewRepo(pool)), idSvc, inventory.NewService(pool),
		workloads.NewService(pool, agentgw.NewDeliveryEngine(pool, agentgw.GlobalDispatcher, slog.Default()), billingSvc),
		telemetry.NewService(pool), billingSvc, audit.NewService(pool),
		policy.NewService(pool), ops.New(pool),
	)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// Org with three principals: owner (launches the workload), a second admin, and
	// an ordinary member.
	_, owner, err := idSvc.Register(ctx, "Acme", "owner@acme.test", "Owner", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	ownerTok, _ := idSvc.IssueAccessToken(ctx, owner)
	adminUser, err := idSvc.CreateUser(ctx, owner.OrgID, "admin@acme.test", "Admin", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	adminTok, _ := idSvc.IssueAccessToken(ctx, adminUser)
	memberUser, err := idSvc.CreateUser(ctx, owner.OrgID, "member@acme.test", "Member", domain.RoleMember)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	memberTok, _ := idSvc.IssueAccessToken(ctx, memberUser)

	const sshPw = "owner-secret-pw"
	var wid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO workloads (org_id, user_id, name, image, status, ssh_password, logs)
		 VALUES ($1, $2, 'owner-job', 'ubuntu:22.04', 'running', $3, 'secret build logs') RETURNING id`,
		owner.OrgID, owner.ID, sshPw).Scan(&wid); err != nil {
		t.Fatalf("insert workload: %v", err)
	}

	get := func(path, token string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}

	// ── H1: owner and admin see the secret; member does not ───────────────────────
	for name, tok := range map[string]string{"owner": ownerTok, "admin": adminTok} {
		code, body := get("/api/v1/workloads/"+wid, tok)
		if code != 200 || body["ssh_password"] != sshPw {
			t.Errorf("%s detail: want 200 + ssh_password, got %d %v", name, code, body["ssh_password"])
		}
		if code, _ := get("/api/v1/workloads/"+wid+"/logs", tok); code != 200 {
			t.Errorf("%s logs: want 200, got %d", name, code)
		}
	}

	code, body := get("/api/v1/workloads/"+wid, memberTok)
	if code != 200 {
		t.Fatalf("member detail: want 200 (workload itself is visible), got %d", code)
	}
	if _, leaked := body["ssh_password"]; leaked {
		t.Errorf("member detail leaks ssh_password: %v", body["ssh_password"])
	}
	if _, leaked := body["logs"]; leaked {
		t.Error("member detail leaks logs")
	}
	if code, _ := get("/api/v1/workloads/"+wid+"/logs", memberTok); code != 403 {
		t.Errorf("member logs endpoint: want 403, got %d", code)
	}

	// List view must be redacted the same way.
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/workloads", nil)
	req.Header.Set("Authorization", "Bearer "+memberTok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var list []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) == 0 {
		t.Fatal("member should see the org's workloads in the list")
	}
	for _, wl := range list {
		if _, leaked := wl["ssh_password"]; leaked {
			t.Errorf("member list leaks ssh_password for %v", wl["id"])
		}
	}

	// ── H3: login brute force hits the per-IP limiter ──────────────────────────────
	// Limit is 20/min/IP; the loop must start returning 429 (not 401) past that.
	var saw429 bool
	for i := 0; i < 25; i++ {
		payload, _ := json.Marshal(map[string]string{"email": "owner@acme.test", "password": "wrong-password"})
		resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("login attempt %d: %v", i, err)
		}
		resp.Body.Close()
		switch {
		case i < 20 && resp.StatusCode != 401:
			t.Fatalf("attempt %d: want 401 (within limit), got %d", i, resp.StatusCode)
		case i >= 20 && resp.StatusCode == http.StatusTooManyRequests:
			saw429 = true
			if resp.Header.Get("Retry-After") == "" {
				t.Error("429 must carry Retry-After")
			}
		}
	}
	if !saw429 {
		t.Error("brute-force loop was never rate limited (expected 429 after 20 attempts)")
	}
}
