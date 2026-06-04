// Command controlplane is the V1 modular monolith.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"

	"github.com/google/uuid"
	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/httpapi"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/ops"
	"github.com/nodehive/gpu-platform/internal/platform/config"
	"github.com/nodehive/gpu-platform/internal/platform/db"
	"github.com/nodehive/gpu-platform/internal/policy"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Apply pending schema migrations before opening the pool. Idempotent and how
	// the schema is created in container/Railway deployments (which only run the
	// binary). Skip with RUN_MIGRATIONS=false if migrations are managed externally.
	if os.Getenv("RUN_MIGRATIONS") != "false" {
		if err := db.Migrate(cfg.DB.URL); err != nil {
			log.Error("migrate", "err", err)
			os.Exit(1)
		}
		log.Info("database migrations applied")
	}

	pool, err := db.Open(ctx, cfg.DB.URL)
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// ── Services ──────────────────────────────────────────────────────────────
	dispatcher := agentgw.GlobalDispatcher
	agentDispatch := agentgw.NewAgentDispatcher(dispatcher)

	nodeRepo := nodes.NewRepo(pool)
	nodeSvc := nodes.NewService(nodeRepo)

	identitySvc := identity.NewService(pool, cfg.Auth.JWTSecret, cfg.Auth.SessionTTL)
	inventorySvc := inventory.NewService(pool)
	billingSvc := billing.NewService(pool)
	workloadsSvc := workloads.NewService(pool, agentDispatch, billingSvc)
	telemetrySvc := telemetry.NewService(pool)
	auditSvc := audit.NewService(pool)
	policySvc := policy.NewService(pool)
	opsSvc := ops.New(pool)

	// ── Bootstrap ─────────────────────────────────────────────────────────────
	if cfg.DevEnrollmentToken != "" {
		if err := nodeSvc.EnsureDevToken(ctx, cfg.DevOrgSlug, cfg.DevEnrollmentToken); err != nil {
			log.Warn("could not seed dev enrollment token", "err", err)
		} else {
			log.Info("dev enrollment token ready", "token", cfg.DevEnrollmentToken, "org", cfg.DevOrgSlug)
		}
	}
	if cfg.DevBootstrapAdmin != "" {
		if err := identitySvc.BootstrapAdmin(ctx, cfg.DevBootstrapAdmin); err != nil {
			log.Warn("bootstrap admin failed", "err", err)
		} else {
			log.Info("admin user ready", "spec", cfg.DevBootstrapAdmin)
		}
	}

	// ── Background jobs ───────────────────────────────────────────────────────
	go runOfflineSweep(ctx, inventorySvc, workloadsSvc, log)
	go runIdleSweep(ctx, policySvc, workloadsSvc, log)
	go runAlertEval(ctx, opsSvc, log)

	// ── gRPC agent gateway ────────────────────────────────────────────────────
	grpcCreds, err := grpcServerCreds(cfg)
	if err != nil {
		log.Error("gRPC TLS", "err", err)
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer(
		grpc.Creds(grpcCreds),
		grpc.ChainStreamInterceptor(agentgw.StreamAuthInterceptor(nodeSvc)),
	)
	agentv1.RegisterAgentServiceServer(grpcSrv,
		agentgw.NewServer(nodeSvc, inventorySvc, telemetrySvc, workloadsSvc, auditSvc, dispatcher, log))
	lis, err := net.Listen("tcp", cfg.GRPC.Addr)
	if err != nil {
		log.Error("grpc listen", "err", err)
		os.Exit(1)
	}
	go func() {
		log.Info("grpc gateway listening", "addr", cfg.GRPC.Addr)
		if err := grpcSrv.Serve(lis); err != nil {
			log.Error("grpc serve", "err", err)
		}
	}()

	// ── HTTP API ──────────────────────────────────────────────────────────────
	httpSrv := &http.Server{
		Addr: cfg.HTTP.Addr,
		Handler: httpapi.NewRouter(
			nodeSvc, identitySvc, inventorySvc,
			workloadsSvc, telemetrySvc, billingSvc, auditSvc, policySvc, opsSvc,
		),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("http api listening", "addr", cfg.HTTP.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http serve", "err", err)
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()
	log.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shCtx)
	grpcSrv.GracefulStop()
}

func grpcServerCreds(cfg config.Config) (credentials.TransportCredentials, error) {
	if cfg.GRPC.Insecure || cfg.GRPC.CertFile == "" {
		return grpcinsecure.NewCredentials(), nil
	}
	cert, err := tls.LoadX509KeyPair(cfg.GRPC.CertFile, cfg.GRPC.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS cert: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

func runOfflineSweep(ctx context.Context, inv inventory.Service, wls workloads.Service, log *slog.Logger) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := inv.SweepOffline(ctx); err != nil {
				log.Warn("offline sweep failed", "err", err)
			}
			if n, err := wls.SweepStuck(ctx); err != nil {
				log.Warn("stuck workload sweep failed", "err", err)
			} else if n > 0 {
				log.Info("reclaimed stuck workloads", "count", n)
			}
		}
	}
}

// runAlertEval periodically evaluates cost-alert rules (F5) and raises alerts.
// 60s cadence: spend/runtime thresholds don't need finer granularity, and the
// evaluator is idempotent (active alerts are deduplicated), so re-runs are cheap.
func runAlertEval(ctx context.Context, opsSvc *ops.Service, log *slog.Logger) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := opsSvc.EvaluateAll(ctx); err != nil {
				log.Warn("alert evaluation failed", "err", err)
			} else if n > 0 {
				log.Info("cost alerts raised", "count", n)
			}
		}
	}
}

func runIdleSweep(ctx context.Context, pol policy.Service, wls workloads.Service, log *slog.Logger) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := pol.SweepIdle(ctx, func(c context.Context, id uuid.UUID) error {
				log.Info("idle auto-stop", "workload_id", id)
				return wls.Stop(c, id, domain.StopIdleReclaim)
			})
			if err != nil {
				log.Warn("idle sweep error", "err", err)
			} else if n > 0 {
				log.Info("idle sweep", "stopped", n)
			}
		}
	}
}
