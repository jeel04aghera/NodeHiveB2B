// Package client is the agent runtime. It enrolls once, then maintains a single
// reconnecting bidirectional stream: sending heartbeats + inventory + metrics,
// and dispatching server commands (launch/stop) to the executor.
package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"time"

	"google.golang.org/grpc"
	"crypto/x509"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nodehive/gpu-platform/agent/discovery"
	"github.com/nodehive/gpu-platform/agent/executor"
	"github.com/nodehive/gpu-platform/agent/identity"
	"github.com/nodehive/gpu-platform/agent/metrics"
	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	metricsv1 "github.com/nodehive/gpu-platform/gen/go/metrics/v1"
	nodev1 "github.com/nodehive/gpu-platform/gen/go/node/v1"
	workloadv1 "github.com/nodehive/gpu-platform/gen/go/workload/v1"
	"github.com/nodehive/gpu-platform/internal/platform/config"
)

const Version = "0.1.0"

type Agent struct {
	cfg        config.AgentConfig
	log        *slog.Logger
	state      identity.State
	discoverer discovery.Discoverer
	sampler    metrics.Sampler
	exec       executor.Executor
}

func New(cfg config.AgentConfig, log *slog.Logger) (*Agent, error) {
	state, ok, err := identity.Load(cfg.CredentialPath)
	if err != nil {
		return nil, fmt.Errorf("load agent state: %w", err)
	}
	if !ok {
		state = identity.State{
			Fingerprint: identity.Fingerprint(),
			PublicKey:   identity.NewPublicKey(),
		}
	}
	disc := discovery.New()
	samp := metrics.New()
	exec := executor.NewWithOptions(executor.Options{
		GPUPassthrough: cfg.GPUPassthrough,
		AdvertiseHost:  cfg.AdvertiseHost,
	})
	if cfg.DevMode {
		// No NVIDIA hardware: synthesise GPU inventory + metrics. Workloads still
		// run as REAL Docker containers (GPU passthrough off) so SSH/Jupyter work.
		disc = discovery.NewDev()
		samp = metrics.NewDev()
		if dockerAvailable() {
			log.Info("agent DEV MODE — synthetic GPUs/metrics; workloads run as real Docker containers (no GPU)")
			exec = executor.NewWithOptions(executor.Options{
				GPUPassthrough: false,
				AdvertiseHost:  cfg.AdvertiseHost,
			})
		} else {
			log.Warn("agent DEV MODE — Docker not found; workloads will be SIMULATED (SSH/Jupyter endpoints are fake)")
			exec = executor.NewDev()
		}
	}
	return &Agent{
		cfg:        cfg,
		log:        log,
		state:      state,
		discoverer: disc,
		sampler:    samp,
		exec:       exec,
	}, nil
}

// dockerAvailable reports whether a working Docker daemon is reachable.
func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "info").Run() == nil
}

// Run enrolls (once) then keeps the stream alive with reconnect-backoff.
func (a *Agent) Run(ctx context.Context) error {
	conn, err := a.dial()
	if err != nil {
		return fmt.Errorf("dial %s: %w", a.cfg.ServerAddr, err)
	}
	defer func() { _ = conn.Close() }()

	c := agentv1.NewAgentServiceClient(conn)

	// Enroll on first run, OR re-enroll when started with a *different* enrollment
	// token than the one that produced the cached credential. The latter is what makes
	// `install.sh --token <X>` actually move this node into token X's org: without it
	// the agent would reconnect with its old credential and silently stay in whatever
	// org it first joined, so the node never appears for the user who generated X.
	tokenChanged := a.cfg.EnrollmentToken != "" &&
		identity.HashToken(a.cfg.EnrollmentToken) != a.state.EnrollTokenHash
	if a.state.Credential == "" || tokenChanged {
		if tokenChanged && a.state.Credential != "" {
			a.log.Info("new enrollment token supplied — re-enrolling so this node joins the token's organization",
				"node_id", a.state.NodeID)
		}
		if err := a.enroll(ctx, c); err != nil {
			return fmt.Errorf("enroll: %w", err)
		}
	} else {
		a.log.Info("already enrolled", "node_id", a.state.NodeID)
	}

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := a.runStream(ctx, c); err == nil || errors.Is(err, context.Canceled) {
			return nil
		} else {
			a.log.Warn("stream ended; reconnecting", "err", err, "backoff", backoff)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

// runStream opens one Connect stream and runs until it fails.
func (a *Agent) runStream(ctx context.Context, c agentv1.AgentServiceClient) error {
	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+a.state.Credential)
	stream, err := c.Connect(authCtx)
	if err != nil {
		return err
	}
	a.log.Info("stream connected", "server", a.cfg.ServerAddr)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to propagate errors from goroutines back to the main loop.
	errc := make(chan error, 4)

	// 1. Discover + send inventory immediately.
	go func() {
		if err := a.sendInventory(streamCtx, stream); err != nil {
			a.log.Warn("inventory send failed", "err", err)
		}
	}()

	// 2. Heartbeat loop.
	go func() {
		errc <- a.heartbeatLoop(streamCtx, stream)
	}()

	// 3. Metrics loop.
	go func() {
		a.metricsLoop(streamCtx, stream)
	}()

	// 4. Command recv loop (main goroutine).
	go func() {
		errc <- a.recvLoop(streamCtx, stream)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ── Inventory ────────────────────────────────────────────────────────────────

func (a *Agent) sendInventory(ctx context.Context, stream agentv1.AgentService_ConnectClient) error {
	node, gpus, err := a.discoverer.Discover(ctx)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	inv := &nodev1.Inventory{
		CollectedAt: timestamppb.Now(),
		Node: &nodev1.NodeInfo{
			Hostname:     host,
			Os:           runtime.GOOS,
			Kernel:       node.Kernel,
			CpuModel:     node.CPUModel,
			CpuCores:     uint32(node.CPUCores),
			RamMb:        node.RAMMB,
			NvidiaDriver: node.NvidiaDriver,
			CudaVersion:  node.CUDAVersion,
			AgentVersion: Version,
		},
	}
	for _, g := range gpus {
		inv.Gpus = append(inv.Gpus, &nodev1.GpuInfo{
			Index:      uint32(g.Index),
			Uuid:       g.UUID,
			Model:      g.Model,
			MemoryMb:   g.MemoryMB,
			MigEnabled: g.MIGEnabled,
		})
	}
	a.log.Info("sending inventory", "gpus", len(gpus))
	return stream.Send(&agentv1.AgentMessage{
		Payload: &agentv1.AgentMessage_Inventory{Inventory: inv},
	})
}

// ── Heartbeat loop ───────────────────────────────────────────────────────────

func (a *Agent) heartbeatLoop(ctx context.Context, stream agentv1.AgentService_ConnectClient) error {
	send := func() error {
		return stream.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_Heartbeat{Heartbeat: &agentv1.Heartbeat{
				Ts:           timestamppb.Now(),
				AgentVersion: Version,
				Healthy:      true,
			}},
		})
	}
	// Send immediately on connect.
	if err := send(); err != nil {
		return err
	}
	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := send(); err != nil {
				return err
			}
			a.log.Debug("heartbeat sent")
		}
	}
}

// ── Metrics loop ─────────────────────────────────────────────────────────────

func (a *Agent) metricsLoop(ctx context.Context, stream agentv1.AgentService_ConnectClient) {
	interval := 30 * time.Second
	if a.cfg.DevMode {
		interval = 5 * time.Second // populate charts quickly in dev
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			samples, err := a.sampler.Sample(ctx)
			if err != nil || len(samples) == 0 {
				continue
			}
			batch := &metricsv1.MetricsBatch{}
			for _, s := range samples {
				batch.Samples = append(batch.Samples, &metricsv1.GpuMetricSample{
					GpuUuid:   s.GPUUUID,
					Ts:        timestamppb.New(s.TS),
					UtilPct:   s.UtilPct,
					MemUsedMb: s.MemUsedMB,
					PowerW:    s.PowerW,
					TempC:     s.TempC,
					SmClockMhz: s.SMClockMHz,
					EccErrors: s.ECCErrors,
					ProcCount: s.ProcCount,
				})
			}
			if err := stream.Send(&agentv1.AgentMessage{
				Payload: &agentv1.AgentMessage_Metrics{Metrics: batch},
			}); err != nil {
				a.log.Warn("metrics send failed", "err", err)
				return
			}
			a.log.Debug("metrics sent", "samples", len(samples))
		}
	}
}

// ── Command recv loop ────────────────────────────────────────────────────────

func (a *Agent) recvLoop(ctx context.Context, stream agentv1.AgentService_ConnectClient) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		go a.handleCommand(ctx, stream, msg)
	}
}

func (a *Agent) handleCommand(ctx context.Context, stream agentv1.AgentService_ConnectClient, msg *agentv1.ServerMessage) {
	cmdID := msg.GetCommandId()

	switch p := msg.GetPayload().(type) {

	case *agentv1.ServerMessage_Launch:
		spec := p.Launch.GetSpec()
		a.log.Info("launch command", "workload_id", spec.GetWorkloadId(), "image", spec.GetImage())

		sshPass := ""
		if env := spec.GetEnv(); env != nil {
			sshPass = env["SSH_PASSWORD"]
		}

		status, err := a.exec.Launch(ctx, executor.LaunchSpec{
			WorkloadID:     spec.GetWorkloadId(),
			Image:          spec.GetImage(),
			GPUUUIDs:       spec.GetGpuUuids(),
			IdleTimeoutSec: int(spec.GetIdleTimeoutSec()),
			Env:            spec.GetEnv(),
			ExposeSSH:      spec.GetExposeSsh(),
			ExposeJupyter:  spec.GetExposeJupyter(),
			SSHPassword:    sshPass,
			// Report granular deployment stages back to the control plane (F1/F2)
			// over the existing status channel, tagged with a STAGE: prefix.
			OnStage: func(stage string) {
				_ = stream.Send(&agentv1.AgentMessage{
					Payload: &agentv1.AgentMessage_WorkloadStatus{
						WorkloadStatus: &workloadv1.WorkloadStatus{
							WorkloadId: spec.GetWorkloadId(),
							State:      workloadv1.WorkloadState_WORKLOAD_STATE_PENDING,
							Message:    "STAGE:" + stage,
							UpdatedAt:  timestamppb.Now(),
						},
					},
				})
			},
		})

		state := workloadv1.WorkloadState_WORKLOAD_STATE_RUNNING
		var msg string
		if err != nil {
			state = workloadv1.WorkloadState_WORKLOAD_STATE_FAILED
			// status.Message carries the detailed reason (exit code + container logs).
			// Encode it after the LOGS separator so the control plane persists it
			// into the workload's logs field for the UI.
			msg = err.Error()
			if status.Message != "" {
				msg = msg + "\n---LOGS---\n" + status.Message
			}
			a.log.Error("launch failed", "workload_id", spec.GetWorkloadId(), "err", err)
		} else {
			a.log.Info("workload running", "workload_id", spec.GetWorkloadId(),
				"ssh", status.SSHEndpoint, "jupyter", status.JupyterEndpoint)
		}

		_ = stream.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_WorkloadStatus{
				WorkloadStatus: &workloadv1.WorkloadStatus{
					WorkloadId:      spec.GetWorkloadId(),
					State:           state,
					SshEndpoint:     status.SSHEndpoint,
					JupyterEndpoint: status.JupyterEndpoint,
					Message:         msg,
					UpdatedAt:       timestamppb.Now(),
				},
			},
		})
		_ = stream.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_CommandResult{CommandResult: &agentv1.CommandResult{
				CommandId: cmdID,
				Ok:        err == nil,
				Error:     msg,
			}},
		})

	case *agentv1.ServerMessage_Stop:
		wid := p.Stop.GetWorkloadId()
		a.log.Info("stop command", "workload_id", wid)

		// Capture final logs before stopping
		finalLogs, _ := a.exec.Logs(ctx, wid, 100)

		err := a.exec.Stop(ctx, wid)
		var errStr string
		if err != nil {
			errStr = err.Error()
		}

		msg := errStr
		if finalLogs != "" {
			msg = msg + "\n---LOGS---\n" + finalLogs
		}

		_ = stream.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_WorkloadStatus{
				WorkloadStatus: &workloadv1.WorkloadStatus{
					WorkloadId: wid,
					State:      workloadv1.WorkloadState_WORKLOAD_STATE_STOPPED,
					StopReason: workloadv1.StopReason(p.Stop.GetReason()),
					Message:    msg,
					UpdatedAt:  timestamppb.Now(),
				},
			},
		})
		_ = stream.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_CommandResult{CommandResult: &agentv1.CommandResult{
				CommandId: cmdID,
				Ok:        err == nil,
				Error:     errStr,
			}},
		})

	case *agentv1.ServerMessage_GetInventory:
		a.log.Info("get_inventory command")
		_ = a.sendInventory(ctx, stream)
		_ = stream.Send(&agentv1.AgentMessage{
			Payload: &agentv1.AgentMessage_CommandResult{CommandResult: &agentv1.CommandResult{
				CommandId: cmdID, Ok: true,
			}},
		})

	default:
		a.log.Warn("unknown server message", "cmd_id", cmdID)
	}
}

// ── Enrollment ───────────────────────────────────────────────────────────────

func (a *Agent) enroll(ctx context.Context, c agentv1.AgentServiceClient) error {
	host, _ := os.Hostname()
	resp, err := c.Enroll(ctx, &agentv1.EnrollRequest{
		EnrollmentToken: a.cfg.EnrollmentToken,
		Fingerprint:     a.state.Fingerprint,
		PublicKey:       a.state.PublicKey,
		ProtocolVersion: 1,
		Node: &nodev1.NodeInfo{
			Hostname:     host,
			Os:           runtime.GOOS,
			AgentVersion: Version,
		},
	})
	if err != nil {
		return err
	}
	a.state.NodeID = resp.GetNodeId()
	a.state.Credential = resp.GetCredential()
	a.state.EnrollTokenHash = identity.HashToken(a.cfg.EnrollmentToken)
	if err := identity.Save(a.cfg.CredentialPath, a.state); err != nil {
		return fmt.Errorf("persist credential: %w", err)
	}
	a.log.Info("enrolled", "node_id", a.state.NodeID)
	return nil
}

func (a *Agent) dial() (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials
	if a.cfg.Insecure {
		creds = insecure.NewCredentials()
	} else if a.cfg.CACertFile != "" {
		// Pin the server CA (self-signed dev CA or org CA)
		caPEM, err := os.ReadFile(a.cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %s: %w", a.cfg.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("invalid CA cert")
		}
		creds = credentials.NewTLS(&tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS13,
		})
	} else {
		// Use system root CAs (production with a real cert)
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13})
	}
	return grpc.NewClient(a.cfg.ServerAddr, grpc.WithTransportCredentials(creds))
}
