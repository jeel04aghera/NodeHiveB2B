//go:build integration

// Billing P0 abuse-case and lifecycle tests:
//
//   - registration grants NO credit by default → free-credit farming via account
//     creation yields zero usable balance;
//   - a zero-balance org cannot launch (402), via the public API;
//   - self-topup is refused (403) unless explicitly enabled;
//   - an operator credit grant immediately unlocks launches;
//   - an exhausted budget blocks launches (402);
//   - enforcement-off mode (advisory deployments) bypasses admission;
//   - the ledger stays consistent under concurrent writers;
//   - metering bills running workloads in slices, is idempotent, and settles
//     terminal workloads whose stop-time settlement was lost (restart safety).
package security

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

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

func TestBillingAdmissionAndAbuse(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)

	// Production posture: no welcome credit, enforcement on, self-topup off.
	idSvc := identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour)
	billingSvc := billing.NewService(pool) // enforcement defaults ON
	router := httpapi.NewRouter(
		nodes.NewService(nodes.NewRepo(pool)), idSvc, inventory.NewService(pool),
		workloads.NewService(pool, agentgw.NewDeliveryEngine(pool, agentgw.GlobalDispatcher, slog.Default()), billingSvc),
		telemetry.NewService(pool), billingSvc, audit.NewService(pool),
		policy.NewService(pool), ops.New(pool),
	) // note: no WithSelfTopup → endpoint disabled, the secure default
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	_, owner, err := idSvc.Register(ctx, "Freeloader Inc", "own@fl.test", "O", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	tok, _ := idSvc.IssueAccessToken(ctx, owner)

	post := func(path string, body any, token string) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	launch := map[string]any{"name": "job", "image": "ubuntu:22.04", "gpu_count": 1}

	t.Run("registration grants no credit", func(t *testing.T) {
		sum, err := billingSvc.CreditSummary(ctx, owner.OrgID)
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if sum.Balance != 0 || sum.TotalGranted != 0 {
			t.Errorf("new org should have zero credit, got balance=%v granted=%v", sum.Balance, sum.TotalGranted)
		}
	})

	t.Run("zero balance blocks launch with 402", func(t *testing.T) {
		code, body := post("/api/v1/workloads", launch, tok)
		if code != 402 {
			t.Fatalf("launch with no credit: want 402, got %d (%v)", code, body)
		}
	})

	t.Run("self-topup refused by default", func(t *testing.T) {
		code, _ := post("/api/v1/billing/credits/topup", map[string]any{"amount": 1e9}, tok)
		if code != 403 {
			t.Fatalf("self-topup: want 403, got %d", code)
		}
		if sum, _ := billingSvc.CreditSummary(ctx, owner.OrgID); sum.Balance != 0 {
			t.Errorf("balance changed despite refusal: %v", sum.Balance)
		}
	})

	t.Run("operator grant unlocks launches", func(t *testing.T) {
		if _, err := billingSvc.AddCredit(ctx, owner.OrgID, 1000, "grant", "operator grant"); err != nil {
			t.Fatalf("grant: %v", err)
		}
		code, body := post("/api/v1/workloads", launch, tok)
		if code != 201 {
			t.Fatalf("launch with credit: want 201 (queued, no GPUs), got %d (%v)", code, body)
		}
		if body["status"] != "queued" {
			t.Errorf("expected queued (no capacity), got %v", body["status"])
		}
	})

	t.Run("exhausted budget blocks launch with 402", func(t *testing.T) {
		// Month-to-date spend of $1 (₹83) against a ₹50 org budget.
		var usageID int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO usage_records (org_id, period_start, period_end, gpu_seconds)
			 VALUES ($1, now() - interval '1 hour', now(), 3600) RETURNING id`,
			owner.OrgID).Scan(&usageID); err != nil {
			t.Fatalf("seed usage: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO cost_records (org_id, usage_record_id, period_start, period_end, gpu_seconds, rate_per_gpu_hour, amount)
			 VALUES ($1, $2, now() - interval '1 hour', now(), 3600, 1.0, 1.0)`,
			owner.OrgID, usageID); err != nil {
			t.Fatalf("seed cost: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO budgets (org_id, scope_type, amount) VALUES ($1, 'organization', 50)`,
			owner.OrgID); err != nil {
			t.Fatalf("seed budget: %v", err)
		}
		code, body := post("/api/v1/workloads", launch, tok)
		if code != 402 {
			t.Fatalf("launch over budget: want 402, got %d (%v)", code, body)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM budgets WHERE org_id=$1`, owner.OrgID); err != nil {
			t.Fatalf("clean budget: %v", err)
		}
	})

	t.Run("advisory mode bypasses admission", func(t *testing.T) {
		advisory := billing.NewService(pool, billing.WithEnforcement(false))
		if err := advisory.AuthorizeLaunch(ctx, uuid.New(), nil, nil); err != nil {
			t.Errorf("enforcement-off must admit regardless of balance: %v", err)
		}
		// And the enforcing service refuses the same broke org.
		if err := billingSvc.AuthorizeLaunch(ctx, uuid.New(), nil, nil); err == nil {
			t.Error("enforcing service must refuse an org with no ledger")
		}
	})

	t.Run("configured welcome credit applies to new orgs", func(t *testing.T) {
		devID := identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour,
			identity.WithWelcomeCredit(500))
		_, u2, err := devID.Register(ctx, "Dev Org", "dev@fl.test", "D", "password123")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if sum, _ := billingSvc.CreditSummary(ctx, u2.OrgID); sum.Balance != 500 {
			t.Errorf("welcome credit: want 500, got %v", sum.Balance)
		}
	})

	t.Run("ledger consistent under concurrent writers", func(t *testing.T) {
		before, _ := billingSvc.CreditSummary(ctx, owner.OrgID)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := billingSvc.AddCredit(ctx, owner.OrgID, 1, "adjustment", "race"); err != nil {
					t.Errorf("concurrent AddCredit: %v", err)
				}
			}()
		}
		wg.Wait()
		after, _ := billingSvc.CreditSummary(ctx, owner.OrgID)
		if want := before.Balance + 20; math.Abs(after.Balance-want) > 1e-6 {
			t.Errorf("lost update: balance=%v want %v", after.Balance, want)
		}
		// The running balance column must equal the sum of deltas (no drift).
		var sumDelta float64
		_ = pool.QueryRow(ctx, `SELECT coalesce(sum(delta),0) FROM credit_ledger WHERE org_id=$1`, owner.OrgID).Scan(&sumDelta)
		if math.Abs(after.Balance-sumDelta) > 1e-6 {
			t.Errorf("running balance drifted from sum(delta): %v vs %v", after.Balance, sumDelta)
		}
	})
}

func TestMeteringLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)
	billingSvc := billing.NewService(pool)

	// Org + node + GPU + rate card + a workload that has been running 20 minutes.
	var orgID, nodeID, gpuID, userID, wid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name) VALUES ('m','Meter Org') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("org: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO gpu_nodes (org_id, fingerprint, hostname, status) VALUES ($1,'fp-m','m-node','online') RETURNING id`,
		orgID).Scan(&nodeID); err != nil {
		t.Fatalf("node: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO gpus (node_id, org_id, gpu_index, uuid, model, memory_mb, status)
		 VALUES ($1,$2,0,'GPU-M-1','TestGPU',16000,'in_use') RETURNING id`,
		nodeID, orgID).Scan(&gpuID); err != nil {
		t.Fatalf("gpu: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO rate_cards (org_id, gpu_model, rate_per_gpu_hour, currency) VALUES ($1,'TestGPU',3.6,'USD')`,
		orgID); err != nil {
		t.Fatalf("rate: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (org_id, email, name, role) VALUES ($1,'m@m.test','M','owner') RETURNING id`,
		orgID).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO workloads (org_id, user_id, node_id, name, image, status, started_at)
		 VALUES ($1,$2,$3,'meter-job','ubuntu:22.04','running', now() - interval '20 minutes') RETURNING id`,
		orgID, userID, nodeID).Scan(&wid); err != nil {
		t.Fatalf("workload: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workload_gpus (workload_id, gpu_id) VALUES ($1,$2)`, wid, gpuID); err != nil {
		t.Fatalf("attach gpu: %v", err)
	}

	// ── Slice 1: running workload accrues usage + cost + ledger debit ─────────────
	n, err := billingSvc.MeterRunning(ctx)
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 workload metered, got %d", n)
	}
	var usageCount int
	var gpuSeconds int64
	_ = pool.QueryRow(ctx,
		`SELECT count(*), coalesce(max(gpu_seconds),0) FROM usage_records WHERE workload_id=$1`, wid).
		Scan(&usageCount, &gpuSeconds)
	if usageCount != 1 || gpuSeconds < 1190 || gpuSeconds > 1215 {
		t.Errorf("slice 1 usage: count=%d gpu_seconds=%d (want 1 record, ~1200s)", usageCount, gpuSeconds)
	}
	var amount float64
	_ = pool.QueryRow(ctx,
		`SELECT coalesce(sum(c.amount),0) FROM cost_records c
		  JOIN usage_records u ON u.id=c.usage_record_id WHERE u.workload_id=$1`, wid).Scan(&amount)
	wantUSD := float64(gpuSeconds) / 3600 * 3.6
	if math.Abs(amount-wantUSD) > 0.01 {
		t.Errorf("slice 1 cost: %v want ~%v", amount, wantUSD)
	}
	var balance float64
	_ = pool.QueryRow(ctx,
		`SELECT balance FROM credit_ledger WHERE org_id=$1 ORDER BY seq DESC LIMIT 1`, orgID).Scan(&balance)
	if math.Abs(balance-(-wantUSD*83.0)) > 1 {
		t.Errorf("ledger debit: balance=%v want ~%v", balance, -wantUSD*83.0)
	}

	// ── Idempotence: an immediate re-run bills nothing (slice below minimum) ──────
	if n, _ := billingSvc.MeterRunning(ctx); n != 0 {
		t.Errorf("immediate re-run should meter 0 workloads, got %d", n)
	}

	// ── Restart safety: workload stops but stop-time settlement is LOST. ──────────
	// The sweep must settle the final slice from the watermark to stopped_at.
	if _, err := pool.Exec(ctx,
		`UPDATE workloads SET status='stopped',
		        stopped_at = metered_until + interval '4 minutes' WHERE id=$1`, wid); err != nil {
		t.Fatalf("stop workload: %v", err)
	}
	if n, err := billingSvc.MeterRunning(ctx); err != nil || n != 1 {
		t.Fatalf("settlement sweep: n=%d err=%v (want 1)", n, err)
	}
	var finalSlices int
	var lastSlice int64
	_ = pool.QueryRow(ctx,
		`SELECT count(*), coalesce(min(gpu_seconds),0) FROM usage_records
		  WHERE workload_id=$1`, wid).Scan(&finalSlices, &lastSlice)
	if finalSlices != 2 || lastSlice < 235 || lastSlice > 245 {
		t.Errorf("final settlement: %d slices, smallest=%ds (want 2 slices, ~240s)", finalSlices, lastSlice)
	}
	var settled bool
	_ = pool.QueryRow(ctx,
		`SELECT metered_until = stopped_at FROM workloads WHERE id=$1`, wid).Scan(&settled)
	if !settled {
		t.Error("watermark must equal stopped_at after settlement")
	}

	// ── Fully settled: further sweeps are no-ops ───────────────────────────────────
	if n, _ := billingSvc.MeterRunning(ctx); n != 0 {
		t.Errorf("settled workload re-billed: %d", n)
	}
}
