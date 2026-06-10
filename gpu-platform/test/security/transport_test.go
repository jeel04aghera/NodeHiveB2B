//go:build integration

// Phase 3 transport attack-path tests:
//
//   - the gRPC gateway serves TLS from the DB-persisted CA, and enrollment works
//     end-to-end over it with the pinned CA (the installer's configuration);
//   - a client that does NOT pin the CA (system roots) is refused at handshake;
//   - a PLAINTEXT client is refused (no silent downgrade);
//   - the CA persists across control-plane restarts (EnsureCA is stable), so
//     agents' pinned trust anchors survive redeploys;
//   - the CA distribution endpoint serves the exact CA over the HTTP API and 404s
//     when the deployment has no pinned CA;
//   - admin credential revocation kills a node's credential (reconnect fails) and
//     re-enrollment issues a fresh working one (rotation loop).
package security

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	nodev1 "github.com/nodehive/gpu-platform/gen/go/node/v1"
	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/httpapi"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/ops"
	"github.com/nodehive/gpu-platform/internal/platform/tlsboot"
	"github.com/nodehive/gpu-platform/internal/policy"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

func TestAgentTransportTLS(t *testing.T) {
	ctx := context.Background()
	pool := startDB(t)

	// ── CA persistence: a "restart" must load the SAME CA ─────────────────────────
	ca1, err := tlsboot.EnsureCA(ctx, pool)
	if err != nil {
		t.Fatalf("ensure ca: %v", err)
	}
	ca2, err := tlsboot.EnsureCA(ctx, pool)
	if err != nil {
		t.Fatalf("ensure ca again: %v", err)
	}
	if ca1.FingerprintSHA256() != ca2.FingerprintSHA256() {
		t.Fatal("CA changed across restarts — every agent's pinned trust anchor would break")
	}

	// ── TLS gRPC gateway, wired as production main.go does ────────────────────────
	tlsCfg, err := ca1.ServerTLS([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("server tls: %v", err)
	}
	nodeSvc := nodes.NewService(nodes.NewRepo(pool))
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (slug, name) VALUES ('t','TLS Org')`); err != nil {
		t.Fatalf("org: %v", err)
	}
	if err := nodeSvc.EnsureDevToken(ctx, "t", "tls-token"); err != nil {
		t.Fatalf("token: %v", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	wlSvc := workloads.NewService(pool, agentgw.NewDeliveryEngine(pool, agentgw.GlobalDispatcher, slog.Default()), billing.NewService(pool))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainStreamInterceptor(agentgw.StreamAuthInterceptor(nodeSvc)))
	agentv1.RegisterAgentServiceServer(grpcSrv, agentgw.NewServer(
		nodeSvc, inventory.NewService(pool), telemetry.NewService(pool), wlSvc,
		audit.NewService(pool), agentgw.GlobalDispatcher, agentgw.NewDeliveryEngine(pool, agentgw.GlobalDispatcher, log), log))
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)
	addr := lis.Addr().String()

	enrollReq := &agentv1.EnrollRequest{
		EnrollmentToken: "tls-token", Fingerprint: "fp-tls", ProtocolVersion: 1,
		Node: &nodev1.NodeInfo{Hostname: "tls-node", Os: "linux", AgentVersion: "test"},
	}
	dialCtx := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 5*time.Second) }

	var firstCred string
	t.Run("pinned CA client enrolls over TLS", func(t *testing.T) {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca1.CertPEM) {
			t.Fatal("bad CA PEM")
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13})))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		c, cancel := dialCtx()
		defer cancel()
		resp, err := agentv1.NewAgentServiceClient(conn).Enroll(c, enrollReq)
		if err != nil {
			t.Fatalf("enroll over TLS: %v", err)
		}
		if resp.GetCredential() == "" {
			t.Fatal("no credential issued")
		}
		firstCred = resp.GetCredential()
	})

	t.Run("unpinned client (system roots) is refused", func(t *testing.T) {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13})))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		c, cancel := dialCtx()
		defer cancel()
		_, err = agentv1.NewAgentServiceClient(conn).Enroll(c, enrollReq)
		if err == nil {
			t.Fatal("handshake must fail without the pinned CA")
		}
		if s, ok := status.FromError(err); ok && s.Code() == codes.OK {
			t.Fatalf("unexpected success-ish status: %v", err)
		}
	})

	t.Run("plaintext client is refused (no downgrade)", func(t *testing.T) {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(grpcinsecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		c, cancel := dialCtx()
		defer cancel()
		if _, err := agentv1.NewAgentServiceClient(conn).Enroll(c, enrollReq); err == nil {
			t.Fatal("plaintext connection must be refused by the TLS server")
		}
	})

	// ── Credential rotation: admin revoke → old cred dead → re-enroll works ───────
	t.Run("revocation endpoint forces rotation", func(t *testing.T) {
		idSvc := identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour)
		if err := idSvc.BootstrapAdmin(ctx, "admin@tls.test:password123"); err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		tok, _, err := idSvc.Login(ctx, "admin@tls.test", "password123")
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		router := httpapi.NewRouter(
			nodeSvc, idSvc, inventory.NewService(pool), wlSvc,
			telemetry.NewService(pool), billing.NewService(pool), audit.NewService(pool),
			policy.NewService(pool), ops.New(pool),
			httpapi.WithAgentTransport(true, ca1.CertPEM),
		)
		srv := httptest.NewServer(router)
		t.Cleanup(srv.Close)

		var nodeID uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM gpu_nodes WHERE fingerprint='fp-tls'`).Scan(&nodeID); err != nil {
			t.Fatalf("node id: %v", err)
		}
		if _, _, err := nodeSvc.AuthenticateAgent(ctx, firstCred); err != nil {
			t.Fatalf("precondition: credential should authenticate: %v", err)
		}

		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/nodes/"+nodeID.String()+"/revoke-credentials", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("revoke: want 200, got %d", resp.StatusCode)
		}
		if _, _, err := nodeSvc.AuthenticateAgent(ctx, firstCred); err == nil {
			t.Fatal("revoked credential still authenticates")
		}

		// Re-enrollment (same org, same fingerprint) issues a fresh working credential.
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(ca1.CertPEM)
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13})))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		c, cancel := dialCtx()
		defer cancel()
		again, err := agentv1.NewAgentServiceClient(conn).Enroll(c, enrollReq)
		if err != nil {
			t.Fatalf("re-enroll: %v", err)
		}
		if _, _, err := nodeSvc.AuthenticateAgent(ctx, again.GetCredential()); err != nil {
			t.Fatalf("fresh credential should authenticate: %v", err)
		}

		// CA distribution endpoint serves the exact pinned CA + installer points at it.
		caResp, err := http.Get(srv.URL + "/api/v1/agent/ca")
		if err != nil {
			t.Fatalf("ca endpoint: %v", err)
		}
		pem, _ := io.ReadAll(caResp.Body)
		caResp.Body.Close()
		if caResp.StatusCode != 200 || string(pem) != string(ca1.CertPEM) {
			t.Errorf("ca endpoint: status=%d, PEM match=%v", caResp.StatusCode, string(pem) == string(ca1.CertPEM))
		}
		inst, err := http.Get(srv.URL + "/install.sh")
		if err != nil {
			t.Fatalf("install.sh: %v", err)
		}
		script, _ := io.ReadAll(inst.Body)
		inst.Body.Close()
		if strings.Contains(string(script), `TLSARGS="--insecure"`) {
			t.Error("TLS deployment's installer must not configure --insecure")
		}
		if !strings.Contains(string(script), "/api/v1/agent/ca") {
			t.Error("installer must fetch and pin the transport CA")
		}
	})

	// ── Plaintext (dev) deployment: no CA endpoint, installer says --insecure ─────
	t.Run("plaintext deployment renders dev installer", func(t *testing.T) {
		idSvc := identity.NewService(pool, "test-secret-thirtytwo-chars-long!!", time.Hour)
		router := httpapi.NewRouter(
			nodeSvc, idSvc, inventory.NewService(pool), wlSvc,
			telemetry.NewService(pool), billing.NewService(pool), audit.NewService(pool),
			policy.NewService(pool), ops.New(pool),
			httpapi.WithAgentTransport(false, nil),
		)
		srv := httptest.NewServer(router)
		t.Cleanup(srv.Close)
		if resp, _ := http.Get(srv.URL + "/api/v1/agent/ca"); resp.StatusCode != 404 {
			t.Errorf("plaintext deployment must 404 the CA endpoint, got %d", resp.StatusCode)
		}
		inst, _ := http.Get(srv.URL + "/install.sh")
		script, _ := io.ReadAll(inst.Body)
		inst.Body.Close()
		if !strings.Contains(string(script), `TLSARGS="--insecure"`) {
			t.Error("plaintext installer must configure --insecure explicitly")
		}
	})
}
