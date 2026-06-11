package agentgw

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"
	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
	metricsv1 "github.com/nodehive/gpu-platform/gen/go/metrics/v1"
	nodev1 "github.com/nodehive/gpu-platform/gen/go/node/v1"
	workloadv1 "github.com/nodehive/gpu-platform/gen/go/workload/v1"
	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

type Server struct {
	agentv1.UnimplementedAgentServiceServer
	nodeSvc      *nodes.Service
	inventorySvc inventory.Service
	telemetrySvc telemetry.Service
	workloadsSvc workloads.Service
	auditSvc     audit.Service
	dispatcher   *Dispatcher
	delivery     *DeliveryEngine
	log          *slog.Logger
	publish      func(orgID uuid.UUID, topics ...string) // optional realtime hints (Phase 6)
}

// SetEventPublisher wires realtime cache-invalidation hints for node lifecycle
// (connect/disconnect/inventory). Optional; nil = no-op.
func (s *Server) SetEventPublisher(pub func(orgID uuid.UUID, topics ...string)) { s.publish = pub }

func (s *Server) notify(orgID uuid.UUID, topics ...string) {
	if s.publish != nil && orgID != uuid.Nil {
		s.publish(orgID, topics...)
	}
}

func NewServer(
	nodeSvc *nodes.Service,
	inventorySvc inventory.Service,
	telemetrySvc telemetry.Service,
	workloadsSvc workloads.Service,
	auditSvc audit.Service,
	dispatcher *Dispatcher,
	delivery *DeliveryEngine,
	log *slog.Logger,
) *Server {
	return &Server{
		nodeSvc:      nodeSvc,
		inventorySvc: inventorySvc,
		telemetrySvc: telemetrySvc,
		workloadsSvc: workloadsSvc,
		auditSvc:     auditSvc,
		dispatcher:   dispatcher,
		delivery:     delivery,
		log:          log,
	}
}

func (s *Server) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	if req.GetEnrollmentToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "enrollment_token is required")
	}
	if req.GetFingerprint() == "" {
		return nil, status.Error(codes.InvalidArgument, "fingerprint is required")
	}
	ni := req.GetNode()
	info := nodes.NodeInfo{
		Hostname:     ni.GetHostname(),
		OS:           ni.GetOs(),
		Kernel:       ni.GetKernel(),
		CPUModel:     ni.GetCpuModel(),
		CPUCores:     int(ni.GetCpuCores()),
		RAMMB:        int64(ni.GetRamMb()),
		NvidiaDriver: ni.GetNvidiaDriver(),
		CUDAVersion:  ni.GetCudaVersion(),
		AgentVersion: ni.GetAgentVersion(),
	}
	nodeID, credential, err := s.nodeSvc.Enroll(ctx, req.GetEnrollmentToken(), req.GetFingerprint(), info)
	if err != nil {
		return nil, enrollError(err)
	}
	s.log.Info("node enrolled", "node_id", nodeID, "hostname", info.Hostname)
	nid, _ := uuid.Parse(nodeID)
	go func() {
		ai, ok := authFromContext(ctx)
		orgID := ai.OrgID
		if !ok {
			// enrollment uses token auth, not the stream credential
			orgID, _ = uuid.Parse("") // best-effort; audit may have empty orgID before auth
		}
		_ = s.auditSvc.Record(context.Background(), domain.AuditLog{
			OrgID: orgID, ActorType: "agent", ActorID: nid.String(),
			Action: "node.enroll", TargetType: "node", TargetID: nid.String(),
			Metadata: map[string]any{"hostname": info.Hostname, "os": info.OS},
		})
	}()
	return &agentv1.EnrollResponse{NodeId: nodeID, Credential: credential}, nil
}

func (s *Server) Connect(stream grpc.BidiStreamingServer[agentv1.AgentMessage, agentv1.ServerMessage]) error {
	ai, ok := authFromContext(stream.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "unauthenticated stream")
	}

	// Register dispatch channel so the control plane can push commands.
	cmdCh, kicked := s.dispatcher.Register(ai.NodeID)
	s.log.Info("agent connected", "node_id", ai.NodeID)
	s.notify(ai.OrgID, "nodes")

	defer func() {
		// Only mark offline if we were still the active stream. A reconnect may have
		// already registered a newer stream for this node — don't tear that one down.
		if s.dispatcher.Deregister(ai.NodeID, cmdCh) {
			_ = s.nodeSvc.MarkOffline(context.Background(), ai.NodeID)
			s.log.Info("agent disconnected", "node_id", ai.NodeID)
			s.notify(ai.OrgID, "nodes")
		}
	}()

	// Reconnect recovery: any command that was enqueued or in flight while this
	// agent was away is still in the outbox — make it due now and deliver it.
	if s.delivery != nil {
		go s.delivery.OnConnect(context.Background(), ai.NodeID)
	}

	// Fan-out: send queued commands to the agent.
	go func() {
		for msg := range cmdCh {
			if err := stream.Send(msg); err != nil {
				s.log.Warn("failed to send command to agent", "node_id", ai.NodeID, "err", err)
				return
			}
		}
	}()

	// Recv in a goroutine so the handler can also honor a Kick (node removal):
	// returning from this handler is the only way to close a server stream.
	type recvMsg struct {
		msg *agentv1.AgentMessage
		err error
	}
	recvCh := make(chan recvMsg)
	go func() {
		for {
			m, err := stream.Recv()
			select {
			case recvCh <- recvMsg{m, err}:
			case <-kicked:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		var msg *agentv1.AgentMessage
		select {
		case <-kicked:
			s.log.Info("agent stream kicked (node removed)", "node_id", ai.NodeID)
			return status.Error(codes.PermissionDenied, "node removed")
		case rm := <-recvCh:
			if errors.Is(rm.err, io.EOF) {
				return nil
			}
			if rm.err != nil {
				return rm.err
			}
			msg = rm.msg
		}

		switch p := msg.GetPayload().(type) {
		case *agentv1.AgentMessage_Heartbeat:
			hb := p.Heartbeat
			if err := s.nodeSvc.Heartbeat(stream.Context(), ai.OrgID, ai.NodeID,
				hb.GetAgentVersion(), int(hb.GetGpuCount()), hb.GetMeanUtilPct()); err != nil {
				s.log.Error("heartbeat persist failed", "node_id", ai.NodeID, "err", err)
			}

		case *agentv1.AgentMessage_Inventory:
			inv := p.Inventory
			go s.handleInventory(stream.Context(), ai, inv)

		case *agentv1.AgentMessage_Metrics:
			go s.handleMetricsBatch(stream.Context(), ai, p.Metrics)

		case *agentv1.AgentMessage_WorkloadStatus:
			go s.handleWorkloadStatus(stream.Context(), ai, p.WorkloadStatus)

		case *agentv1.AgentMessage_CommandResult:
			// Finalize the outbox row: 'acked' (delivered + executed) or 'failed'
			// (delivered, agent reports it could not execute). Either way the
			// command's fate is now explicit — no silent drops.
			if s.delivery != nil {
				s.delivery.Ack(stream.Context(), p.CommandResult.GetCommandId(),
					p.CommandResult.GetOk(), p.CommandResult.GetError())
			}
			s.log.Debug("command result", "node_id", ai.NodeID,
				"cmd", p.CommandResult.GetCommandId(), "ok", p.CommandResult.GetOk())
		}
	}
}

func (s *Server) handleInventory(ctx context.Context, ai authInfo, inv *nodev1.Inventory) {
	if inv == nil {
		return
	}
	ni := inv.GetNode()
	node := domain.Node{
		ID:           ai.NodeID,
		OrgID:        ai.OrgID,
		Hostname:     ni.GetHostname(),
		OS:           ni.GetOs(),
		Kernel:       ni.GetKernel(),
		CPUModel:     ni.GetCpuModel(),
		CPUCores:     int(ni.GetCpuCores()),
		RAMMB:        int64(ni.GetRamMb()),
		NvidiaDriver: ni.GetNvidiaDriver(),
		CUDAVersion:  ni.GetCudaVersion(),
		AgentVersion: ni.GetAgentVersion(),
	}
	var gpus []domain.GPU
	for _, g := range inv.GetGpus() {
		gpus = append(gpus, domain.GPU{
			NodeID:     ai.NodeID,
			OrgID:      ai.OrgID,
			Index:      int(g.GetIndex()),
			UUID:       g.GetUuid(),
			Model:      g.GetModel(),
			MemoryMB:   int64(g.GetMemoryMb()),
			MIGEnabled: g.GetMigEnabled(),
		})
	}
	if err := s.inventorySvc.UpsertInventory(ctx, ai.NodeID, node, gpus); err != nil {
		s.log.Error("upsert inventory failed", "node_id", ai.NodeID, "err", err)
		return
	}
	s.notify(ai.OrgID, "nodes")
}

func (s *Server) handleMetricsBatch(ctx context.Context, ai authInfo, batch *metricsv1.MetricsBatch) {
	if batch == nil {
		return
	}

	// Resolve GPU UUID strings to DB UUIDs via a simple query approach:
	// We pass samples using GPU DB IDs resolved on the fly.
	// For simplicity, map GPU UUID -> DB ID here.
	uuidToID := s.resolveGPUIDs(ctx, ai.NodeID)

	var samples []telemetry.Sample
	for _, sm := range batch.GetSamples() {
		dbID, ok := uuidToID[sm.GetGpuUuid()]
		if !ok {
			continue
		}
		samples = append(samples, telemetry.Sample{
			GPUID:     dbID,
			NodeID:    ai.NodeID,
			TS:        sm.GetTs().AsTime(),
			UtilPct:   sm.GetUtilPct(),
			MemUsedMB: int32(sm.GetMemUsedMb()),
			PowerW:    sm.GetPowerW(),
			TempC:     sm.GetTempC(),
			ECCErrors: int32(sm.GetEccErrors()),
			ProcCount: int32(sm.GetProcCount()),
		})
	}
	if len(samples) > 0 {
		if err := s.telemetrySvc.Ingest(ctx, ai.OrgID, samples); err != nil {
			s.log.Warn("metrics ingest failed", "node_id", ai.NodeID, "err", err)
		}
	}
}

func (s *Server) resolveGPUIDs(ctx context.Context, nodeID uuid.UUID) map[string]uuid.UUID {
	_, gpuList, err := s.inventorySvc.GetNode(ctx, nodeID)
	if err != nil {
		return nil
	}
	m := make(map[string]uuid.UUID, len(gpuList))
	for _, g := range gpuList {
		m[g.UUID] = g.ID
	}
	return m
}

// handleWorkloadStatus applies an agent's status report. The workload reference is
// attacker-controlled input (any enrolled agent can claim any UUID), so every write
// below is scoped to ai.NodeID inside the workloads service — a report for a workload
// not assigned to this node is dropped and logged (C1: cross-tenant write prevention).
func (s *Server) handleWorkloadStatus(ctx context.Context, ai authInfo, ws *workloadv1.WorkloadStatus) {
	if ws == nil {
		return
	}
	id, err := uuid.Parse(ws.GetWorkloadId())
	if err != nil {
		return
	}
	// Granular deployment stage report (F1/F2) — recorded as a lifecycle event,
	// not a status transition.
	if msg := ws.GetMessage(); strings.HasPrefix(msg, "STAGE:") {
		if err := s.workloadsSvc.RecordStageEvent(ctx, ai.NodeID, id, strings.TrimPrefix(msg, "STAGE:")); err != nil {
			s.log.Warn("record stage event rejected", "workload_id", id, "node_id", ai.NodeID, "err", err)
		}
		return
	}
	state := protoToWorkloadState(ws.GetState())
	// message field carries optional JSON {"logs":"..."} from the agent
	logs := extractLogs(ws.GetMessage())
	if err := s.workloadsSvc.UpdateStatus(ctx, ai.NodeID, id, state,
		ws.GetSshEndpoint(), ws.GetJupyterEndpoint(), ws.GetMessage(), logs); err != nil {
		s.log.Warn("update workload status rejected", "workload_id", id, "node_id", ai.NodeID, "err", err)
	}
}

func extractLogs(msg string) string {
	// Agent encodes logs as a JSON suffix: "...message...\n---LOGS---\n<lines>"
	const sep = "\n---LOGS---\n"
	if i := len(msg) - len(sep); i > 0 {
		if idx := findSep(msg, sep); idx >= 0 {
			return msg[idx+len(sep):]
		}
	}
	return ""
}

func findSep(s, sep string) int {
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

func protoToWorkloadState(s workloadv1.WorkloadState) domain.WorkloadState {
	switch s {
	case workloadv1.WorkloadState_WORKLOAD_STATE_RUNNING:
		return domain.WorkloadRunning
	case workloadv1.WorkloadState_WORKLOAD_STATE_STOPPING:
		return domain.WorkloadStopping
	case workloadv1.WorkloadState_WORKLOAD_STATE_STOPPED:
		return domain.WorkloadStopped
	case workloadv1.WorkloadState_WORKLOAD_STATE_FAILED:
		return domain.WorkloadFailed
	default:
		return domain.WorkloadPending
	}
}

func enrollError(err error) error {
	switch {
	case errors.Is(err, nodes.ErrInvalidToken):
		return status.Error(codes.Unauthenticated, "invalid enrollment token")
	case errors.Is(err, nodes.ErrTokenExpired):
		return status.Error(codes.PermissionDenied, "enrollment token expired")
	case errors.Is(err, nodes.ErrTokenExhausted):
		return status.Error(codes.PermissionDenied, "enrollment token exhausted")
	case errors.Is(err, nodes.ErrFingerprintConflict):
		return status.Error(codes.PermissionDenied, "this machine is already enrolled by another organization")
	default:
		return status.Error(codes.Internal, "enrollment failed")
	}
}

// workloads.Dispatcher is now implemented by the DeliveryEngine (delivery.go):
// commands are durable outbox rows and Nudge wakes the deliverer, replacing the
// old fire-and-forget AgentDispatcher.Send.
