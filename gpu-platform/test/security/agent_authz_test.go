//go:build integration

// Attack-path regression tests for the agent trust boundary:
//
//	C1 — a compromised/hostile enrolled agent must not be able to mutate workloads
//	     assigned to any other node (same org or cross-org): status transitions,
//	     endpoint poisoning, and stage events are all node-scoped.
//	C2 — a client-supplied fingerprint must never re-home a node into another org,
//	     and same-org re-enrollment must rotate (revoke) prior agent credentials.
package security

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	nodev1 "github.com/nodehive/gpu-platform/gen/go/node/v1"
	workloadv1 "github.com/nodehive/gpu-platform/gen/go/workload/v1"
	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

// startDB boots a migrated Postgres for one test.
func startDB(t *testing.T) *pgxpool.Pool {
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

func TestAgentTrustBoundary(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)

	// Two tenants, each with its own enrollment token.
	var orgA, orgB uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name) VALUES ('org-a','Org A') RETURNING id`).Scan(&orgA); err != nil {
		t.Fatalf("seed org A: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name) VALUES ('org-b','Org B') RETURNING id`).Scan(&orgB); err != nil {
		t.Fatalf("seed org B: %v", err)
	}
	nodeSvc := nodes.NewService(nodes.NewRepo(pool))
	if err := nodeSvc.EnsureDevToken(ctx, "org-a", "token-a"); err != nil {
		t.Fatalf("seed token A: %v", err)
	}
	if err := nodeSvc.EnsureDevToken(ctx, "org-b", "token-b"); err != nil {
		t.Fatalf("seed token B: %v", err)
	}

	// Full gRPC gateway, wired exactly as main.go does.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dispatcher := agentgw.GlobalDispatcher
	wlSvc := workloads.NewService(pool, agentgw.NewDeliveryEngine(pool, dispatcher, slog.Default()), billing.NewService(pool))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer(grpc.ChainStreamInterceptor(agentgw.StreamAuthInterceptor(nodeSvc)))
	agentv1.RegisterAgentServiceServer(grpcSrv, agentgw.NewServer(
		nodeSvc, inventory.NewService(pool), telemetry.NewService(pool), wlSvc,
		audit.NewService(pool), dispatcher, agentgw.NewDeliveryEngine(pool, dispatcher, log), log))
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(grpcinsecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := agentv1.NewAgentServiceClient(conn)

	enroll := func(token, fp, host string) *agentv1.EnrollResponse {
		t.Helper()
		resp, err := client.Enroll(ctx, &agentv1.EnrollRequest{
			EnrollmentToken: token, Fingerprint: fp, ProtocolVersion: 1,
			Node: &nodev1.NodeInfo{Hostname: host, Os: "linux", AgentVersion: "test"},
		})
		if err != nil {
			t.Fatalf("enroll %s/%s: %v", token, fp, err)
		}
		return resp
	}

	nodeA := enroll("token-a", "fp-victim", "victim-node")
	nodeB := enroll("token-b", "fp-attacker", "attacker-node")
	nodeAID := uuid.MustParse(nodeA.GetNodeId())
	nodeBID := uuid.MustParse(nodeB.GetNodeId())

	// ── C2: cross-org fingerprint replay must NOT re-home the node ────────────────
	t.Run("C2 fingerprint hijack refused", func(t *testing.T) {
		_, err := client.Enroll(ctx, &agentv1.EnrollRequest{
			EnrollmentToken: "token-b", Fingerprint: "fp-victim", ProtocolVersion: 1,
			Node: &nodev1.NodeInfo{Hostname: "hijacked", Os: "linux", AgentVersion: "evil"},
		})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cross-org fingerprint enroll: want PermissionDenied, got %v", err)
		}
		var gotOrg uuid.UUID
		var hostname string
		if err := pool.QueryRow(ctx,
			`SELECT org_id, hostname FROM gpu_nodes WHERE fingerprint='fp-victim'`).Scan(&gotOrg, &hostname); err != nil {
			t.Fatalf("read node: %v", err)
		}
		if gotOrg != orgA {
			t.Errorf("node was re-homed: org=%s want %s", gotOrg, orgA)
		}
		if hostname != "victim-node" {
			t.Errorf("node facts overwritten by attacker: hostname=%q", hostname)
		}
	})

	// ── C2: same-org re-enrollment rotates credentials ─────────────────────────────
	t.Run("C2 re-enroll revokes old credential", func(t *testing.T) {
		oldCred := nodeA.GetCredential()
		again := enroll("token-a", "fp-victim", "victim-node")
		if again.GetNodeId() != nodeA.GetNodeId() {
			t.Fatalf("re-enroll created a new node: %s vs %s", again.GetNodeId(), nodeA.GetNodeId())
		}
		if _, _, err := nodeSvc.AuthenticateAgent(ctx, oldCred); err == nil {
			t.Error("old credential still authenticates after re-enrollment (rotation failed)")
		}
		if _, _, err := nodeSvc.AuthenticateAgent(ctx, again.GetCredential()); err != nil {
			t.Errorf("new credential should authenticate: %v", err)
		}
		nodeA = again // keep the live credential for the C1 leg below
	})

	// ── C1 setup: a running workload on org A's node ───────────────────────────────
	var wid uuid.UUID
	var userA uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (org_id, email, name, role) VALUES ($1,'a@a.test','A','owner') RETURNING id`,
		orgA).Scan(&userA); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO workloads (org_id, user_id, node_id, name, image, status, ssh_endpoint)
		 VALUES ($1,$2,$3,'victim-job','ubuntu:22.04','running','victim:22') RETURNING id`,
		orgA, userA, nodeAID).Scan(&wid); err != nil {
		t.Fatalf("seed workload: %v", err)
	}

	// ── C1: deterministic service-level checks ─────────────────────────────────────
	t.Run("C1 service rejects cross-node writes", func(t *testing.T) {
		err := wlSvc.UpdateStatus(ctx, nodeBID, wid, domain.WorkloadStopped, "evil:22", "", "pwned", "")
		if err == nil {
			t.Fatal("UpdateStatus from a foreign node must be rejected")
		}
		if err := wlSvc.RecordStageEvent(ctx, nodeBID, wid, "pulling_image"); err == nil {
			t.Fatal("RecordStageEvent from a foreign node must be rejected")
		}
		var st, ssh string
		_ = pool.QueryRow(ctx, `SELECT status, ssh_endpoint FROM workloads WHERE id=$1`, wid).Scan(&st, &ssh)
		if st != "running" || ssh != "victim:22" {
			t.Errorf("workload mutated by foreign node: status=%q ssh=%q", st, ssh)
		}
		// The assigned node CAN report.
		if err := wlSvc.UpdateStatus(ctx, nodeAID, wid, domain.WorkloadRunning, "victim:22", "", "ok", ""); err != nil {
			t.Errorf("assigned node's report should succeed: %v", err)
		}
	})

	// ── C1: full gRPC attack path — hostile agent stream reports on victim's job ──
	t.Run("C1 gRPC stream rejects cross-node status", func(t *testing.T) {
		authB := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+nodeB.GetCredential())
		streamB, err := client.Connect(authB)
		if err != nil {
			t.Fatalf("connect as attacker: %v", err)
		}
		if err := streamB.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_WorkloadStatus{WorkloadStatus: &workloadv1.WorkloadStatus{
				WorkloadId: wid.String(),
				State:      workloadv1.WorkloadState_WORKLOAD_STATE_STOPPED,
				SshEndpoint: "attacker.example.com:2222",
			}},
		}); err != nil {
			t.Fatalf("send hostile status: %v", err)
		}
		// The handler is async; give it ample time, then assert nothing changed.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(150 * time.Millisecond)
			var st, ssh string
			_ = pool.QueryRow(ctx, `SELECT status, ssh_endpoint FROM workloads WHERE id=$1`, wid).Scan(&st, &ssh)
			if st != "running" || ssh != "victim:22" {
				t.Fatalf("hostile agent mutated workload via gRPC: status=%q ssh=%q", st, ssh)
			}
		}
		_ = streamB.CloseSend()

		// Sanity: the assigned node's stream still works end-to-end.
		authA := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+nodeA.GetCredential())
		streamA, err := client.Connect(authA)
		if err != nil {
			t.Fatalf("connect as victim node: %v", err)
		}
		if err := streamA.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_WorkloadStatus{WorkloadStatus: &workloadv1.WorkloadStatus{
				WorkloadId: wid.String(),
				State:      workloadv1.WorkloadState_WORKLOAD_STATE_STOPPED,
			}},
		}); err != nil {
			t.Fatalf("send legit status: %v", err)
		}
		ok := false
		for i := 0; i < 50; i++ {
			var st string
			_ = pool.QueryRow(ctx, `SELECT status FROM workloads WHERE id=$1`, wid).Scan(&st)
			if st == "stopped" {
				ok = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ok {
			t.Error("assigned node's stop report was not applied")
		}
		_ = streamA.CloseSend()
	})
}
