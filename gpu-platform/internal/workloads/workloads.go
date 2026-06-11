// Package workloads is the Workloads module.
// Owns tables: workloads, workload_gpus. Placement is first-fit (NOT a scheduler).
package workloads

import (
	"context"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type LaunchRequest struct {
	UserID         uuid.UUID
	ProjectID      *uuid.UUID
	DepartmentID   *uuid.UUID
	Name           string
	TemplateID     *uuid.UUID // resolved template (nil = ad-hoc image)
	Image          string     // base image that actually runs (resolved from template)
	GPUType        string
	GPUCount       int
	IdleTimeoutSec *int
	ExposeSSH      bool
	ExposeJupyter  bool
}

type ListFilter struct {
	Status    domain.WorkloadState
	UserID    *uuid.UUID
	ProjectID *uuid.UUID
	// Viewer enables project-level isolation (Phase 6): when set and ViewerIsAdmin
	// is false, workloads in restricted projects the viewer doesn't belong to are
	// hidden (own workloads always remain visible).
	Viewer        *uuid.UUID
	ViewerIsAdmin bool
}

// WorkloadGPU is one GPU attached to a workload.
type WorkloadGPU struct {
	UUID  string `json:"uuid"`
	Model string `json:"model"`
}

// DetailView enriches a workload with GPU allocation and live runtime cost.
type DetailView struct {
	domain.Workload
	GPUs           []WorkloadGPU `json:"gpus"`
	RuntimeSeconds int64         `json:"runtime_seconds"`
	RuntimeCost    float64       `json:"runtime_cost"`
	Currency       string        `json:"currency"`
}

type Service interface {
	Launch(ctx context.Context, orgID uuid.UUID, req LaunchRequest) (domain.Workload, error)
	Stop(ctx context.Context, id uuid.UUID, reason domain.StopReason) error
	// Get and Detail are org-scoped: a workload outside orgID returns ErrNotFound,
	// so one org cannot read another org's workload (incl. its SSH credential).
	Get(ctx context.Context, orgID, id uuid.UUID) (domain.Workload, error)
	Detail(ctx context.Context, orgID, id uuid.UUID) (DetailView, error)
	List(ctx context.Context, orgID uuid.UUID, f ListFilter) ([]domain.Workload, error)
	// UpdateStatus applies an agent-reported state change. nodeID is the reporting
	// agent's authenticated node identity: a workload not assigned to that node
	// returns ErrNotFound, so one enrolled agent can never mutate another node's
	// (or another org's) workloads.
	UpdateStatus(ctx context.Context, nodeID, id uuid.UUID, state domain.WorkloadState, ssh, jupyter, msg, logs string) error
	// SweepStuck reclaims GPUs from workloads whose node is offline (marks them failed)
	// and frees any GPUs still attached to terminal workloads. Returns count reclaimed.
	SweepStuck(ctx context.Context) (int, error)

	// F1 — lifecycle events / timeline.
	RecordEvent(ctx context.Context, workloadID, orgID uuid.UUID, stage, message string) error
	// RecordStageEvent is node-scoped like UpdateStatus (agent-originated input).
	RecordStageEvent(ctx context.Context, nodeID, workloadID uuid.UUID, stage string) error
	ListEvents(ctx context.Context, orgID, workloadID uuid.UUID) ([]WorkloadEvent, error)

	// F3 — queue.
	ListQueue(ctx context.Context, orgID uuid.UUID) (QueueStats, error)
	CancelQueued(ctx context.Context, id uuid.UUID) error
	// PromoteQueued starts queued workloads in one org as capacity allows;
	// PromoteAllQueued does it fleet-wide (periodic sweep — promotion survives
	// control-plane restarts because the queue lives in the database).
	PromoteQueued(ctx context.Context, orgID uuid.UUID) (int, error)
	PromoteAllQueued(ctx context.Context) (int, error)
}

// Dispatcher is the workloads → agent-gateway seam. Commands are durable rows in
// the agent_commands outbox (written in the same transaction as the workload state
// they implement); Nudge tells the delivery engine "there may be new deliverable
// rows for this node" and never blocks. Losing a nudge is safe — the engine's
// periodic tick redelivers anything still due.
type Dispatcher interface {
	Nudge(nodeID uuid.UUID)
}
