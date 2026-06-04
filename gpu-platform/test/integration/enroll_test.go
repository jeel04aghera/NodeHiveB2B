//go:build integration

// Package integration exercises the full enroll-a-node slice against a real
// Postgres (via testcontainers). Run with: make test-integration  (Docker required).
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	nodev1 "github.com/nodehive/gpu-platform/gen/go/node/v1"
	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/httpapi"
	"github.com/nodehive/gpu-platform/internal/nodes"
)

func TestEnrollHeartbeatAndList(t *testing.T) {
	ctx := context.Background()

	// 1. Start a test database.
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("gpu"),
		tcpostgres.WithUsername("gpu"),
		tcpostgres.WithPassword("gpu"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}

	// Apply migrations with goose.
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open sql: %v", err)
	}
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

	// Seed the dev org + enrollment token.
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (slug, name) VALUES ('dev','Dev Org')`); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	repo := nodes.NewRepo(pool)
	svc := nodes.NewService(repo)
	if err := svc.EnsureDevToken(ctx, "dev", "test-token"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	orgID, err := repo.OrgIDBySlug(ctx, "dev")
	if err != nil {
		t.Fatalf("org id: %v", err)
	}

	// 2. Start the gRPC server on a random port.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer(grpc.ChainStreamInterceptor(agentgw.StreamAuthInterceptor(svc)))
	agentv1.RegisterAgentServiceServer(grpcSrv, agentgw.NewServer(svc, log))
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	// 3. Start the agent client (in-process gRPC client).
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c := agentv1.NewAgentServiceClient(conn)

	// 4. Enroll the node.
	enrollResp, err := c.Enroll(ctx, &agentv1.EnrollRequest{
		EnrollmentToken: "test-token",
		Fingerprint:     "fp-integration-1",
		ProtocolVersion: 1,
		Node:            &nodev1.NodeInfo{Hostname: "it-node-1", Os: "linux", AgentVersion: "test"},
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if enrollResp.GetNodeId() == "" || enrollResp.GetCredential() == "" {
		t.Fatal("enroll returned empty node id/credential")
	}

	// 5. Send a heartbeat over an authenticated stream (kept open for the rest of the test).
	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+enrollResp.GetCredential())
	stream, err := c.Connect(authCtx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := stream.Send(&agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{
			Ts: timestamppb.Now(), AgentVersion: "test", Healthy: true,
		}},
	}); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}

	// 6. Verify DB state: exactly one node, at least one heartbeat (poll briefly).
	var nodeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gpu_nodes WHERE fingerprint='fp-integration-1'`).Scan(&nodeCount); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if nodeCount != 1 {
		t.Fatalf("expected 1 node, got %d", nodeCount)
	}

	var hbCount int
	for i := 0; i < 50; i++ {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_heartbeats WHERE node_id=$1`, enrollResp.GetNodeId()).Scan(&hbCount); err != nil {
			t.Fatalf("count heartbeats: %v", err)
		}
		if hbCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if hbCount == 0 {
		t.Fatal("no heartbeat persisted")
	}

	// 7. Call the HTTP endpoint.
	httpSrv := httptest.NewServer(httpapi.NewRouter(svc, orgID))
	t.Cleanup(httpSrv.Close)
	resp, err := http.Get(httpSrv.URL + "/api/v1/nodes")
	if err != nil {
		t.Fatalf("GET /nodes: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 8. Verify the response.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got []httpapi.NodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 node in response, got %d", len(got))
	}
	if got[0].Hostname != "it-node-1" {
		t.Errorf("hostname = %q, want it-node-1", got[0].Hostname)
	}
	if got[0].Status != "online" {
		t.Errorf("status = %q, want online", got[0].Status)
	}
	if got[0].ID != enrollResp.GetNodeId() {
		t.Errorf("id = %q, want %q", got[0].ID, enrollResp.GetNodeId())
	}
}
