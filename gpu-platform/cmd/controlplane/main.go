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

	"github.com/jackc/pgx/v5/pgxpool"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/google/uuid"
	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	"github.com/nodehive/gpu-platform/internal/agentgw"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/email"
	"github.com/nodehive/gpu-platform/internal/events"
	"github.com/nodehive/gpu-platform/internal/httpapi"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/obs"
	"github.com/nodehive/gpu-platform/internal/ops"
	"github.com/nodehive/gpu-platform/internal/platform/config"
	"github.com/nodehive/gpu-platform/internal/platform/db"
	"github.com/nodehive/gpu-platform/internal/platform/tlsboot"
	"github.com/nodehive/gpu-platform/internal/policy"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stdout, nil)).Error("config", "err", err)
		os.Exit(1)
	}

	// Structured logging (Phase 5): JSON in production for log drains, text in dev.
	// Error-level records are mirrored to Sentry when SENTRY_DSN is set.
	log := obs.NewLogger(cfg.Obs.LogFormat, cfg.Obs.LogLevel)
	slog.SetDefault(log)
	if enabled, err := obs.InitSentry(cfg.Obs.SentryDSN, cfg.Env, cfg.Obs.Version); err != nil {
		log.Warn("sentry init failed; continuing without error monitoring", "err", err)
	} else if enabled {
		log.Info("sentry error monitoring enabled", "env", cfg.Env, "release", cfg.Obs.Version)
	}
	defer obs.FlushSentry()

	ctx := context.Background()
	started := time.Now()

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
	// Delivery engine: drains the agent_commands outbox to connected agents and
	// implements workloads.Dispatcher (Nudge). Commands are durable rows written in
	// the workload's own transaction, so a crash or disconnected agent delays
	// delivery instead of losing it.
	deliveryEngine := agentgw.NewDeliveryEngine(pool, dispatcher, log)

	nodeRepo := nodes.NewRepo(pool)
	nodeSvc := nodes.NewService(nodeRepo)

	identitySvc := identity.NewService(pool, cfg.Auth.JWTSecret, cfg.Auth.SessionTTL,
		identity.WithAccessTTL(cfg.Auth.AccessTokenTTL),
		identity.WithRefreshTTL(cfg.Auth.RefreshTokenTTL),
		identity.WithWelcomeCredit(cfg.Billing.WelcomeCredit))
	inventorySvc := inventory.NewService(pool)
	billingSvc := billing.NewService(pool, billing.WithEnforcement(cfg.Billing.Enforce))
	auditSvc := audit.NewService(pool)
	// Realtime hub (Phase 6): org-scoped SSE cache-invalidation hints. In-process,
	// like the agent dispatcher — multi-replica needs a pub/sub bridge behind it.
	eventHub := events.NewHub()
	workloadsSvc := workloads.NewService(pool, deliveryEngine, billingSvc,
		// System/agent lifecycle transitions (sweep reclaims, idle stops, agent-reported
		// terminal states) land in the same tamper-evident audit trail as user actions.
		workloads.WithAudit(func(c context.Context, e domain.AuditLog) {
			if err := auditSvc.Record(c, e); err != nil {
				log.Warn("audit record failed", "action", e.Action, "err", err)
			}
		}),
		workloads.WithEvents(eventHub.PublishTopics))
	telemetrySvc := telemetry.NewService(pool, telemetry.WithRawRetention(cfg.Retention.RawMetrics))
	policySvc := policy.NewService(pool)
	opsSvc := ops.New(pool)

	// ── Observability (Phase 5) ───────────────────────────────────────────────
	// Job tracker: every background sweep reports its outcome so /api/v1/health and
	// /metrics can flag a silently-failing loop. Registered up front with each job's
	// cadence so a sweep that never starts still shows up unhealthy.
	jobs := obs.NewJobTracker()
	jobs.Register("offline_sweep", 30*time.Second)
	jobs.Register("stuck_sweep", 30*time.Second)
	jobs.Register("idle_sweep", 5*time.Minute)
	jobs.Register("alert_eval", 60*time.Second)
	jobs.Register("meter_sweep", 60*time.Second)
	jobs.Register("queue_sweep", 20*time.Second)
	jobs.Register("retention_sweep", time.Hour)

	health := &obs.Health{
		DB:              pool,
		Jobs:            jobs,
		ConnectedAgents: func() int { return len(dispatcher.ConnectedNodes()) },
		Version:         cfg.Obs.Version,
		Env:             cfg.Env,
		GRPCTLS:         !cfg.GRPC.Insecure,
		Started:         started,
	}
	metrics := obs.NewMetrics(health)

	// ── Bootstrap ─────────────────────────────────────────────────────────────
	// DEV_* bootstrap (known admin creds + a fixed enrollment token) runs ONLY in
	// development. In production these are never created regardless of env values, so
	// a deploy can't ship a predictable admin/token backdoor. Real orgs come from
	// POST /auth/register.
	if cfg.IsDev() {
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
	} else {
		log.Info("production mode: dev bootstrap admin/token disabled")
	}

	// ── Background jobs ───────────────────────────────────────────────────────
	go deliveryEngine.Run(ctx)
	go runOfflineSweep(ctx, inventorySvc, workloadsSvc, jobs, log)
	go runIdleSweep(ctx, policySvc, workloadsSvc, jobs, log)
	go runAlertEval(ctx, opsSvc, jobs, log)
	go runMeterSweep(ctx, billingSvc, jobs, log)
	go runQueueSweep(ctx, workloadsSvc, jobs, log)
	go runRetentionSweep(ctx, telemetrySvc, cfg.Retention, jobs, log)

	// ── gRPC agent gateway ────────────────────────────────────────────────────
	grpcCreds, agentCAPEM, err := grpcServerCreds(ctx, cfg, pool, log)
	if err != nil {
		log.Error("gRPC TLS", "err", err)
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer(
		grpc.Creds(grpcCreds),
		grpc.ChainStreamInterceptor(agentgw.StreamAuthInterceptor(nodeSvc)),
		// Mirror the agent's 30s keepalive pings (anything stricter than MinTime
		// would close the connection with ENHANCE_YOUR_CALM) and probe idle agents
		// from this side too, so half-dead streams are torn down and the agent's
		// reconnect (with command redelivery) kicks in within ~a minute.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
	)
	agentSrv := agentgw.NewServer(nodeSvc, inventorySvc, telemetrySvc, workloadsSvc, auditSvc, dispatcher, deliveryEngine, log)
	agentSrv.SetEventPublisher(eventHub.PublishTopics) // node connect/disconnect/inventory → SSE
	agentv1.RegisterAgentServiceServer(grpcSrv, agentSrv)
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
			httpapi.WithGoogleOAuth(identity.NewGoogleOAuth(cfg.Google.ClientID, cfg.Google.ClientSecret, cfg.Google.RedirectURL)),
			httpapi.WithAppBaseURL(cfg.AppBaseURL),
			httpapi.WithCORSOrigins(cfg.CORSAllowedOrigins),
			httpapi.WithSecureCookies(!cfg.IsDev()),
			httpapi.WithRefreshCookieTTL(cfg.Auth.RefreshTokenTTL),
			httpapi.WithEmailSender(email.NewSender(cfg.Email.ResendAPIKey, cfg.Email.FromEmail, log)),
			httpapi.WithSelfTopup(cfg.Billing.AllowSelfTopup),
			httpapi.WithAgentTransport(!cfg.GRPC.Insecure, agentCAPEM),
			httpapi.WithNodeKicker(dispatcher.Kick),
			httpapi.WithLogger(log),
			httpapi.WithHealth(health),
			httpapi.WithMetrics(metrics, cfg.Obs.MetricsToken),
			httpapi.WithEventHub(eventHub),
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

// grpcServerCreds resolves the agent-gateway transport, in order of preference:
//
//  1. GRPC_INSECURE=true   → plaintext (dev default; loud warning in production —
//     this is also the rollback lever for the TLS rollout).
//  2. GRPC_CERT_FILE set   → operator-provided certificate (own domain + Let's
//     Encrypt / corporate PKI). Agents verify with system roots; no pinned CA.
//  3. otherwise (auto)     → self-signed CA persisted in Postgres signs a per-boot
//     server cert whose SANs cover AGENT_PUBLIC_GRPC_ADDR. The CA PEM is returned
//     so the HTTP API can distribute it to agents over the HTTPS edge.
func grpcServerCreds(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (credentials.TransportCredentials, []byte, error) {
	if cfg.GRPC.Insecure {
		if !cfg.IsDev() {
			log.Warn("SECURITY: agent gRPC is PLAINTEXT in production (GRPC_INSECURE=true) — " +
				"enrollment tokens, agent credentials and SSH passwords cross the network unencrypted; " +
				"unset GRPC_INSECURE to enable TLS")
		}
		return grpcinsecure.NewCredentials(), nil, nil
	}
	if cfg.GRPC.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.GRPC.CertFile, cfg.GRPC.KeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("load TLS cert: %w", err)
		}
		log.Info("agent gRPC TLS: operator-provided certificate", "cert", cfg.GRPC.CertFile)
		return credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientAuth:   tls.NoClientCert,
			MinVersion:   tls.VersionTLS13,
		}), nil, nil
	}

	ca, err := tlsboot.EnsureCA(ctx, pool)
	if err != nil {
		return nil, nil, fmt.Errorf("transport CA: %w", err)
	}
	hosts := []string{}
	if h := hostOnly(cfg.AgentPublicGRPCAddr); h != "" {
		hosts = append(hosts, h)
	}
	if hn, err := os.Hostname(); err == nil {
		hosts = append(hosts, hn)
	}
	tlsCfg, err := ca.ServerTLS(hosts)
	if err != nil {
		return nil, nil, fmt.Errorf("mint server cert: %w", err)
	}
	log.Info("agent gRPC TLS: auto (DB-persisted CA, per-boot server cert)",
		"ca_sha256", ca.FingerprintSHA256(), "sans", hosts)
	return credentials.NewTLS(tlsCfg), ca.CertPEM, nil
}

// hostOnly strips an optional :port from host:port.
func hostOnly(addr string) string {
	if addr == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func runOfflineSweep(ctx context.Context, inv inventory.Service, wls workloads.Service, jobs *obs.JobTracker, log *slog.Logger) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := inv.SweepOffline(ctx)
			jobs.Record("offline_sweep", err)
			if err != nil {
				log.Warn("offline sweep failed", "err", err)
			}
			n, err := wls.SweepStuck(ctx)
			jobs.Record("stuck_sweep", err)
			if err != nil {
				log.Warn("stuck workload sweep failed", "err", err)
			} else if n > 0 {
				log.Info("reclaimed stuck workloads", "count", n)
			}
		}
	}
}

// runMeterSweep periodically bills running workloads (usage/cost records + ledger
// debit per unmetered slice) and settles terminal workloads whose stop-time
// settlement was lost. 60s cadence; the per-workload minimum slice lives in billing,
// so frequent ticks are cheap no-ops. This is what makes billing restart-safe: all
// metering state is the metered_until watermark in the database.
func runMeterSweep(ctx context.Context, b billing.Service, jobs *obs.JobTracker, log *slog.Logger) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := b.MeterRunning(ctx)
			jobs.Record("meter_sweep", err)
			if err != nil {
				log.Warn("metering sweep error", "err", err, "metered", n)
			} else if n > 0 {
				log.Info("metered workloads", "count", n)
			}
		}
	}
}

// runAlertEval periodically evaluates cost-alert rules (F5) and raises alerts.
// 60s cadence: spend/runtime thresholds don't need finer granularity, and the
// evaluator is idempotent (active alerts are deduplicated), so re-runs are cheap.
func runAlertEval(ctx context.Context, opsSvc *ops.Service, jobs *obs.JobTracker, log *slog.Logger) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := opsSvc.EvaluateAll(ctx)
			jobs.Record("alert_eval", err)
			if err != nil {
				log.Warn("alert evaluation failed", "err", err)
			} else if n > 0 {
				log.Info("cost alerts raised", "count", n)
			}
		}
	}
}

// runQueueSweep periodically promotes queued workloads (F3) fleet-wide. This is
// what makes promotion reliable: the event-driven nudge after a stop is just a
// latency optimization, while this tick guarantees a queued workload starts even
// if that nudge was lost to a crash, or capacity appeared without a stop event
// (new node enrolled, sweep-freed GPUs, credits topped up).
func runQueueSweep(ctx context.Context, wls workloads.Service, jobs *obs.JobTracker, log *slog.Logger) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := wls.PromoteAllQueued(ctx)
			jobs.Record("queue_sweep", err)
			if err != nil {
				log.Warn("queue promotion sweep failed", "err", err)
			} else if n > 0 {
				log.Info("promoted queued workloads", "count", n)
			}
		}
	}
}

// runRetentionSweep ages out time-series data hourly (raw GPU metrics roll up to
// gpu_metrics_hourly first, so long-range utilization survives). The first sweep
// runs shortly after boot so a long-stopped deployment catches up quickly.
func runRetentionSweep(ctx context.Context, tel telemetry.Service, cfg config.RetentionConfig, jobs *obs.JobTracker, log *slog.Logger) {
	policy := telemetry.RetentionPolicy{
		RawMetrics:    cfg.RawMetrics,
		RollupMetrics: cfg.RollupMetrics,
		Heartbeats:    cfg.Heartbeats,
		Events:        cfg.Events,
	}
	run := func() {
		st, err := tel.SweepRetention(ctx, policy)
		jobs.Record("retention_sweep", err)
		if err != nil {
			log.Warn("retention sweep failed", "err", err)
			return
		}
		if st.RawDeleted+st.HeartbeatDeleted+st.EventsDeleted+st.RollupsDeleted > 0 {
			log.Info("retention sweep",
				"rolled_up_hours", st.RolledUpHours, "raw_deleted", st.RawDeleted,
				"heartbeats_deleted", st.HeartbeatDeleted, "events_deleted", st.EventsDeleted,
				"rollups_deleted", st.RollupsDeleted)
		}
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
		run()
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

func runIdleSweep(ctx context.Context, pol policy.Service, wls workloads.Service, jobs *obs.JobTracker, log *slog.Logger) {
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
			jobs.Record("idle_sweep", err)
			if err != nil {
				log.Warn("idle sweep error", "err", err)
			} else if n > 0 {
				log.Info("idle sweep", "stopped", n)
			}
		}
	}
}
