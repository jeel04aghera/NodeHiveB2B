//go:build integration

// Phase 4 (Reliability) verification suite. Real Postgres (testcontainers), real
// migrations, real services — exercises the placement lock under concurrency, the
// command outbox (delivery, redelivery, ack, deadline expiry, supersede), queue
// promotion, terminal-state guards, node removal and metrics retention.
package reliability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/proto"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

// ── Harness ──────────────────────────────────────────────────────────────────

func setupPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

type fixture struct {
	orgID  uuid.UUID
	userID uuid.UUID
	nodeID uuid.UUID
	gpuIDs []uuid.UUID
}

// seedOrg creates an org + user + one online node with n idle GPUs.
func seedOrg(t *testing.T, pool *pgxpool.Pool, slug string, gpus int) fixture {
	t.Helper()
	ctx := context.Background()
	var f fixture
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizations (name, slug) VALUES ($1,$1) RETURNING id`, slug).Scan(&f.orgID); err != nil {
		t.Fatalf("org: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (org_id, email, password_hash, role) VALUES ($1, $2, 'x', 'admin') RETURNING id`,
		f.orgID, slug+"@test.local").Scan(&f.userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO gpu_nodes (org_id, fingerprint, hostname, status, last_seen_at)
		 VALUES ($1, $2, $2, 'online', now()) RETURNING id`,
		f.orgID, slug+"-node").Scan(&f.nodeID); err != nil {
		t.Fatalf("node: %v", err)
	}
	for i := 0; i < gpus; i++ {
		var gid uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO gpus (node_id, org_id, gpu_index, uuid, model, memory_mb, status)
			 VALUES ($1,$2,$3,$4,'NVIDIA A100 80GB',81920,'idle') RETURNING id`,
			f.nodeID, f.orgID, i, fmt.Sprintf("GPU-%s-%d", slug, i)).Scan(&gid); err != nil {
			t.Fatalf("gpu: %v", err)
		}
		f.gpuIDs = append(f.gpuIDs, gid)
	}
	return f
}

func newWorkloadsSvc(pool *pgxpool.Pool) (workloads.Service, *agentgw.DeliveryEngine) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	eng := agentgw.NewDeliveryEngine(pool, agentgw.GlobalDispatcher, log)
	return workloads.NewService(pool, eng, nil), eng
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// ── R1: placement locking under concurrency (stress) ─────────────────────────

func TestPlacementConcurrencyStress(t *testing.T) {
	pool := setupPool(t)
	svc, _ := newWorkloadsSvc(pool)
	ctx := context.Background()

	const gpuCount = 8
	const launchers = 50
	f := seedOrg(t, pool, "stress", gpuCount)

	var wg sync.WaitGroup
	errs := make(chan error, launchers)
	for i := 0; i < launchers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{
				UserID: f.userID, Name: fmt.Sprintf("wl-%d", i), GPUCount: 1,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("launch failed under concurrency: %v", err)
	}

	pending := count(t, pool, `SELECT count(*) FROM workloads WHERE org_id=$1 AND status='pending'`, f.orgID)
	queued := count(t, pool, `SELECT count(*) FROM workloads WHERE org_id=$1 AND status='queued'`, f.orgID)
	if pending != gpuCount || queued != launchers-gpuCount {
		t.Fatalf("want %d pending / %d queued, got %d / %d", gpuCount, launchers-gpuCount, pending, queued)
	}
	// THE invariant: no GPU has more than one active attachment.
	dup := count(t, pool, `SELECT count(*) FROM (
		SELECT gpu_id FROM workload_gpus WHERE detached_at IS NULL GROUP BY gpu_id HAVING count(*) > 1
	) d`)
	if dup != 0 {
		t.Fatalf("%d GPUs double-assigned", dup)
	}
	if idle := count(t, pool, `SELECT count(*) FROM gpus WHERE org_id=$1 AND status='idle'`, f.orgID); idle != 0 {
		t.Fatalf("want 0 idle GPUs, got %d", idle)
	}
	// Every placed workload has a durable launch command.
	cmds := count(t, pool, `SELECT count(*) FROM agent_commands WHERE org_id=$1 AND kind='launch'`, f.orgID)
	if cmds != gpuCount {
		t.Fatalf("want %d launch commands, got %d", gpuCount, cmds)
	}

	// Multi-GPU requests under the same contention rules.
	f2 := seedOrg(t, pool, "stress-multi", 8)
	var wg2 sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			_, _ = svc.Launch(ctx, f2.orgID, workloads.LaunchRequest{
				UserID: f2.userID, Name: fmt.Sprintf("multi-%d", i), GPUCount: 3,
			})
		}(i)
	}
	wg2.Wait()
	placed := count(t, pool, `SELECT count(*) FROM workloads WHERE org_id=$1 AND status='pending'`, f2.orgID)
	if placed != 2 { // 8 GPUs / 3 per workload = 2 placements max
		t.Fatalf("want exactly 2 multi-GPU placements, got %d", placed)
	}
	attached := count(t, pool,
		`SELECT count(*) FROM workload_gpus wg JOIN gpus g ON g.id=wg.gpu_id
		  WHERE g.org_id=$1 AND wg.detached_at IS NULL`, f2.orgID)
	if attached != 6 {
		t.Fatalf("want 6 active attachments, got %d", attached)
	}
}

// ── R2: outbox delivery, redelivery, ack ──────────────────────────────────────

func TestCommandDeliveryAndRedelivery(t *testing.T) {
	pool := setupPool(t)
	svc, eng := newWorkloadsSvc(pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go eng.Run(ctx)

	f := seedOrg(t, pool, "deliver", 1)

	// Launch while the agent is OFFLINE: the command must wait in the outbox.
	wl, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "offline-launch", GPUCount: 1})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // let any misguided delivery attempt happen
	var status string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts FROM agent_commands WHERE workload_id=$1`, wl.ID).Scan(&status, &attempts); err != nil {
		t.Fatalf("command row: %v", err)
	}
	if status != "pending" && status != "sent" { // nudge may have tried and failed → still retryable
		t.Fatalf("command should still be deliverable, got %q", status)
	}

	// Agent connects (simulated): register its stream channel and run the reconnect
	// hook — outstanding commands become due immediately (a backoff computed while
	// the agent was away must not delay recovery) and are delivered.
	ch, _ := agentgw.GlobalDispatcher.Register(f.nodeID)
	defer agentgw.GlobalDispatcher.Deregister(f.nodeID, ch)
	go eng.OnConnect(ctx, f.nodeID)

	var msg *agentv1.ServerMessage
	select {
	case msg = <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("command was not delivered after reconnect nudge")
	}
	launch := msg.GetLaunch()
	if launch == nil || launch.GetSpec().GetWorkloadId() != wl.ID.String() {
		t.Fatalf("delivered wrong command: %+v", msg)
	}
	cmdID := msg.GetCommandId()

	// No ack → force the row due again → engine must REDELIVER the same command id.
	if _, err := pool.Exec(ctx,
		`UPDATE agent_commands SET next_attempt_at=now() WHERE id=$1`, uuid.MustParse(cmdID)); err != nil {
		t.Fatalf("force due: %v", err)
	}
	eng.Nudge(f.nodeID)
	select {
	case redelivered := <-ch:
		if redelivered.GetCommandId() != cmdID {
			t.Fatalf("redelivery changed command id: %s != %s", redelivered.GetCommandId(), cmdID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command was not redelivered")
	}

	// Ack finalizes it; nothing further is delivered.
	eng.Ack(ctx, cmdID, true, "")
	if err := pool.QueryRow(ctx,
		`SELECT status FROM agent_commands WHERE id=$1`, uuid.MustParse(cmdID)).Scan(&status); err != nil {
		t.Fatalf("acked row: %v", err)
	}
	if status != "acked" {
		t.Fatalf("want acked, got %q", status)
	}

	// Payload sanity: stored bytes decode to the same spec the agent received.
	var payload []byte
	_ = pool.QueryRow(ctx, `SELECT payload FROM agent_commands WHERE id=$1`, uuid.MustParse(cmdID)).Scan(&payload)
	var stored agentv1.ServerMessage
	if err := proto.Unmarshal(payload, &stored); err != nil {
		t.Fatalf("stored payload corrupt: %v", err)
	}
	if stored.GetLaunch().GetSpec().GetWorkloadId() != wl.ID.String() {
		t.Fatal("stored payload does not match wire payload")
	}
}

// ── R2/R3: delivery deadline → explicit failure; supersede on terminal ───────

func TestLaunchDeadlineExpiryFailsWorkload(t *testing.T) {
	pool := setupPool(t)
	svc, _ := newWorkloadsSvc(pool)
	ctx := context.Background()

	f := seedOrg(t, pool, "expiry", 2)
	wl, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "never-delivered", GPUCount: 1})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}

	// Simulate "agent unreachable past the deadline".
	if _, err := pool.Exec(ctx,
		`UPDATE agent_commands SET deliver_by=now()-interval '1 minute' WHERE workload_id=$1`, wl.ID); err != nil {
		t.Fatalf("age command: %v", err)
	}
	if _, err := svc.SweepStuck(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var wlStatus, cmdStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM workloads WHERE id=$1`, wl.ID).Scan(&wlStatus)
	_ = pool.QueryRow(ctx, `SELECT status FROM agent_commands WHERE workload_id=$1`, wl.ID).Scan(&cmdStatus)
	if wlStatus != "failed" {
		t.Fatalf("undeliverable launch must fail the workload explicitly, got %q", wlStatus)
	}
	if cmdStatus != "expired" {
		t.Fatalf("want command expired, got %q", cmdStatus)
	}
	if idle := count(t, pool, `SELECT count(*) FROM gpus WHERE org_id=$1 AND status='idle'`, f.orgID); idle != 2 {
		t.Fatalf("GPUs must be freed on explicit failure: want 2 idle, got %d", idle)
	}
}

func TestStopSupersededWhenSweepReclaims(t *testing.T) {
	pool := setupPool(t)
	svc, _ := newWorkloadsSvc(pool)
	ctx := context.Background()

	f := seedOrg(t, pool, "supersede", 1)
	wl, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "stop-lost", GPUCount: 1})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	// Reaches running, then a stop is issued but the agent never confirms.
	if err := svc.UpdateStatus(ctx, f.nodeID, wl.ID, domain.WorkloadRunning, "h:22", "", "", ""); err != nil {
		t.Fatalf("running: %v", err)
	}
	if err := svc.Stop(ctx, wl.ID, domain.StopUser); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Sweep grace passed with no confirmation → workload failed, stop command superseded.
	if _, err := pool.Exec(ctx,
		`UPDATE workloads SET stopping_at=now()-interval '3 minutes' WHERE id=$1`, wl.ID); err != nil {
		t.Fatalf("age stopping: %v", err)
	}
	if _, err := svc.SweepStuck(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var wlStatus, stopStatus string
	_ = pool.QueryRow(ctx, `SELECT status FROM workloads WHERE id=$1`, wl.ID).Scan(&wlStatus)
	_ = pool.QueryRow(ctx,
		`SELECT status FROM agent_commands WHERE workload_id=$1 AND kind='stop'`, wl.ID).Scan(&stopStatus)
	if wlStatus != "failed" {
		t.Fatalf("want failed, got %q", wlStatus)
	}
	if stopStatus != "superseded" {
		t.Fatalf("stop command for terminal workload must be superseded, got %q", stopStatus)
	}
}

// ── R3: terminal-state guards (restart/reconcile safety) ─────────────────────

func TestTerminalWorkloadIsNeverResurrected(t *testing.T) {
	pool := setupPool(t)
	svc, _ := newWorkloadsSvc(pool)
	ctx := context.Background()

	f := seedOrg(t, pool, "guards", 1)
	wl, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "guard", GPUCount: 1})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := svc.UpdateStatus(ctx, f.nodeID, wl.ID, domain.WorkloadRunning, "h:22", "", "", ""); err != nil {
		t.Fatalf("running: %v", err)
	}
	if err := svc.UpdateStatus(ctx, f.nodeID, wl.ID, domain.WorkloadStopped, "", "", "", ""); err != nil {
		t.Fatalf("stopped: %v", err)
	}
	var stoppedAt time.Time
	_ = pool.QueryRow(ctx, `SELECT stopped_at FROM workloads WHERE id=$1`, wl.ID).Scan(&stoppedAt)

	// A late/reconcile-time 'running' report must NOT resurrect it…
	if err := svc.UpdateStatus(ctx, f.nodeID, wl.ID, domain.WorkloadRunning, "h:22", "", "", ""); err != nil {
		t.Fatalf("late running report: %v", err)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM workloads WHERE id=$1`, wl.ID).Scan(&status)
	if status != "stopped" {
		t.Fatalf("terminal workload resurrected to %q", status)
	}
	// …and the leaked container gets a reap stop command instead.
	reaps := count(t, pool,
		`SELECT count(*) FROM agent_commands WHERE workload_id=$1 AND kind='stop' AND status IN ('pending','sent')`, wl.ID)
	if reaps != 1 {
		t.Fatalf("want 1 reap stop command, got %d", reaps)
	}

	// Duplicate terminal report: stopped_at must not move (idempotent).
	time.Sleep(50 * time.Millisecond)
	if err := svc.UpdateStatus(ctx, f.nodeID, wl.ID, domain.WorkloadStopped, "", "", "", ""); err != nil {
		t.Fatalf("dup stopped: %v", err)
	}
	var stoppedAt2 time.Time
	_ = pool.QueryRow(ctx, `SELECT stopped_at FROM workloads WHERE id=$1`, wl.ID).Scan(&stoppedAt2)
	if !stoppedAt2.Equal(stoppedAt) {
		t.Fatal("duplicate terminal report re-stamped stopped_at")
	}
}

// ── R4: queue promotion reliability ───────────────────────────────────────────

func TestQueuePromotionSweep(t *testing.T) {
	pool := setupPool(t)
	svc, _ := newWorkloadsSvc(pool)
	ctx := context.Background()

	f := seedOrg(t, pool, "queue", 1)
	a, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "holder", GPUCount: 1})
	if err != nil {
		t.Fatalf("launch a: %v", err)
	}
	b, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "waiter", GPUCount: 1})
	if err != nil {
		t.Fatalf("launch b: %v", err)
	}
	if b.Status != domain.WorkloadQueued {
		t.Fatalf("b should queue, got %s", b.Status)
	}

	// Crash-simulation: the GPU frees WITHOUT the terminal-status nudge running
	// (as if the control plane restarted right after the stop landed).
	if _, err := pool.Exec(ctx,
		`UPDATE workloads SET status='stopped', stopped_at=now() WHERE id=$1`, a.ID); err != nil {
		t.Fatalf("force-stop a: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE workload_gpus SET detached_at=now() WHERE workload_id=$1`, a.ID); err != nil {
		t.Fatalf("detach a: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE gpus SET status='idle' WHERE org_id=$1`, f.orgID); err != nil {
		t.Fatalf("free gpu: %v", err)
	}

	// The periodic sweep — not any event — must promote b.
	n, err := svc.PromoteAllQueued(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 promotion, got %d", n)
	}
	var status string
	var nodeID *uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT status, node_id FROM workloads WHERE id=$1`, b.ID).Scan(&status, &nodeID)
	if status != "pending" || nodeID == nil {
		t.Fatalf("b not promoted correctly: status=%s node=%v", status, nodeID)
	}
	launchCmds := count(t, pool,
		`SELECT count(*) FROM agent_commands WHERE workload_id=$1 AND kind='launch'`, b.ID)
	if launchCmds != 1 {
		t.Fatalf("promoted workload must have a launch command, got %d", launchCmds)
	}

	// Concurrent promoters must not double-promote (advisory lock + CAS).
	c, _ := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "waiter2", GPUCount: 1})
	if c.Status != domain.WorkloadQueued {
		t.Fatalf("c should queue, got %s", c.Status)
	}
	_, _ = pool.Exec(ctx, `UPDATE workloads SET status='stopped', stopped_at=now() WHERE id=$1`, b.ID)
	_, _ = pool.Exec(ctx, `UPDATE workload_gpus SET detached_at=now() WHERE workload_id=$1`, b.ID)
	_, _ = pool.Exec(ctx, `UPDATE gpus SET status='idle' WHERE org_id=$1`, f.orgID)
	var wg sync.WaitGroup
	total := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, _ := svc.PromoteQueued(ctx, f.orgID)
			total <- n
		}()
	}
	wg.Wait()
	close(total)
	sum := 0
	for n := range total {
		sum += n
	}
	if sum != 1 {
		t.Fatalf("8 concurrent promoters promoted %d times (want exactly 1)", sum)
	}
	if dup := count(t, pool, `SELECT count(*) FROM (
		SELECT gpu_id FROM workload_gpus WHERE detached_at IS NULL GROUP BY gpu_id HAVING count(*)>1) d`); dup != 0 {
		t.Fatalf("promotion double-assigned %d GPUs", dup)
	}
}

// ── R5: node removal lifecycle ────────────────────────────────────────────────

func TestNodeRemovalLifecycle(t *testing.T) {
	pool := setupPool(t)
	svc, _ := newWorkloadsSvc(pool)
	nodeSvc := nodes.NewService(nodes.NewRepo(pool))
	ctx := context.Background()

	f := seedOrg(t, pool, "removal", 2)
	wl, err := svc.Launch(ctx, f.orgID, workloads.LaunchRequest{UserID: f.userID, Name: "on-node", GPUCount: 1})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := svc.UpdateStatus(ctx, f.nodeID, wl.ID, domain.WorkloadRunning, "h:22", "", "", ""); err != nil {
		t.Fatalf("running: %v", err)
	}
	// Billing history that must survive the removal.
	if _, err := pool.Exec(ctx,
		`INSERT INTO usage_records (org_id, workload_id, gpu_id, user_id, period_start, period_end, gpu_seconds)
		 VALUES ($1,$2,$3,$4, now()-interval '1 hour', now(), 3600)`,
		f.orgID, wl.ID, f.gpuIDs[0], f.userID); err != nil {
		t.Fatalf("usage record: %v", err)
	}
	// Agent credential to be revoked.
	if _, err := pool.Exec(ctx,
		`INSERT INTO agent_credentials (org_id, node_id, public_key) VALUES ($1,$2,'cred-hash-removal')`,
		f.orgID, f.nodeID); err != nil {
		t.Fatalf("credential: %v", err)
	}

	// Safe path refuses while workloads are active.
	if _, err := nodeSvc.Remove(ctx, f.orgID, f.nodeID, false); !errors.Is(err, nodes.ErrNodeBusy) {
		t.Fatalf("want ErrNodeBusy, got %v", err)
	}
	// Cross-org removal is invisible.
	other := seedOrg(t, pool, "removal-other", 0)
	if _, err := nodeSvc.Remove(ctx, other.orgID, f.nodeID, true); !errors.Is(err, nodes.ErrNotFound) {
		t.Fatalf("cross-org removal must be NotFound, got %v", err)
	}

	res, err := nodeSvc.Remove(ctx, f.orgID, f.nodeID, true)
	if err != nil {
		t.Fatalf("force remove: %v", err)
	}
	if res.FailedWorkloads != 1 || res.CredentialsRevoked != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if n := count(t, pool, `SELECT count(*) FROM gpu_nodes WHERE id=$1`, f.nodeID); n != 0 {
		t.Fatal("node row still present")
	}
	if n := count(t, pool, `SELECT count(*) FROM gpus WHERE node_id=$1`, f.nodeID); n != 0 {
		t.Fatal("gpus not cascaded")
	}
	var wlStatus string
	var wlNode *uuid.UUID
	_ = pool.QueryRow(ctx, `SELECT status, node_id FROM workloads WHERE id=$1`, wl.ID).Scan(&wlStatus, &wlNode)
	if wlStatus != "failed" || wlNode != nil {
		t.Fatalf("workload after removal: status=%s node=%v (want failed, NULL)", wlStatus, wlNode)
	}
	// Billing truth preserved, GPU reference nulled.
	var gpuRef *uuid.UUID
	var gpuSeconds int64
	if err := pool.QueryRow(ctx,
		`SELECT gpu_id, gpu_seconds FROM usage_records WHERE workload_id=$1`, wl.ID).Scan(&gpuRef, &gpuSeconds); err != nil {
		t.Fatalf("usage record gone: %v", err)
	}
	if gpuRef != nil || gpuSeconds != 3600 {
		t.Fatalf("usage record mutated: gpu=%v seconds=%d", gpuRef, gpuSeconds)
	}
	// Re-enrollment of the same fingerprint must be possible (unique index freed).
	if n := count(t, pool, `SELECT count(*) FROM gpu_nodes WHERE fingerprint='removal-node'`); n != 0 {
		t.Fatal("fingerprint still occupied")
	}
}

// ── R6: metrics retention ─────────────────────────────────────────────────────

func TestMetricsRetention(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()
	f := seedOrg(t, pool, "retention", 1)

	telSvc := telemetry.NewService(pool, telemetry.WithRawRetention(14*24*time.Hour))

	// 48 old samples (20 days ago, spanning 2 hours) + 12 recent ones.
	old := time.Now().Add(-20 * 24 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 48; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO gpu_metrics (org_id, gpu_id, node_id, ts, util_pct, mem_used_mb)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			f.orgID, f.gpuIDs[0], f.nodeID, old.Add(time.Duration(i)*150*time.Second), 50.0, 4096); err != nil {
			t.Fatalf("old sample: %v", err)
		}
	}
	for i := 0; i < 12; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO gpu_metrics (org_id, gpu_id, node_id, ts, util_pct, mem_used_mb)
			 VALUES ($1,$2,$3,now()-make_interval(mins=>$4),80.0,8192)`,
			f.orgID, f.gpuIDs[0], f.nodeID, i); err != nil {
			t.Fatalf("recent sample: %v", err)
		}
	}
	// Old heartbeats + events age out too.
	if _, err := pool.Exec(ctx,
		`INSERT INTO agent_heartbeats (org_id, node_id, ts, status) VALUES ($1,$2,now()-interval '30 days','healthy')`,
		f.orgID, f.nodeID); err != nil {
		t.Fatalf("old heartbeat: %v", err)
	}

	st, err := telSvc.SweepRetention(ctx, telemetry.DefaultRetention())
	if err != nil {
		t.Fatalf("retention sweep: %v", err)
	}
	if st.RawDeleted != 48 {
		t.Fatalf("want 48 raw deleted, got %d", st.RawDeleted)
	}
	if st.HeartbeatDeleted != 1 {
		t.Fatalf("want 1 heartbeat deleted, got %d", st.HeartbeatDeleted)
	}
	if n := count(t, pool, `SELECT count(*) FROM gpu_metrics WHERE org_id=$1`, f.orgID); n != 12 {
		t.Fatalf("recent raw samples must survive: want 12, got %d", n)
	}
	// Rollups carry the history forward.
	var hours, samples int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(sample_count),0) FROM gpu_metrics_hourly WHERE org_id=$1`,
		f.orgID).Scan(&hours, &samples); err != nil {
		t.Fatalf("rollups: %v", err)
	}
	if hours != 2 || samples != 48 {
		t.Fatalf("want 2 rollup hours covering 48 samples, got %d/%d", hours, samples)
	}
	// Idempotent: a second sweep changes nothing.
	st2, err := telSvc.SweepRetention(ctx, telemetry.DefaultRetention())
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if st2.RawDeleted != 0 || st2.RolledUpHours != 0 {
		t.Fatalf("second sweep not idempotent: %+v", st2)
	}

	// Reads span the boundary: old window served from rollups, recent from raw.
	pts, err := telSvc.Utilization(ctx, f.orgID, telemetry.UtilQuery{
		From: old.Add(-time.Hour), To: time.Now(), Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("utilization: %v", err)
	}
	var sawOld, sawRecent bool
	for _, p := range pts {
		if p.TS.Before(time.Now().Add(-15 * 24 * time.Hour)) {
			sawOld = true
			if p.UtilPct < 49 || p.UtilPct > 51 {
				t.Fatalf("rollup-backed point should average 50%%, got %f", p.UtilPct)
			}
		} else {
			sawRecent = true
		}
	}
	if !sawOld || !sawRecent {
		t.Fatalf("utilization must merge rollups+raw (old=%v recent=%v, %d pts)", sawOld, sawRecent, len(pts))
	}
}
