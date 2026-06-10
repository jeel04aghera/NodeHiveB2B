// Package workloads is the Workloads module.
// Owns tables: workloads, workload_gpus. Placement is first-fit (NOT a scheduler).
package workloads

import (
	"context"

	"github.com/google/uuid"

	agentv1 "github.com/nodehive/gpu-platform/gen/go/agent/v1"
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
}

// Dispatcher sends typed ServerMessages to connected agents.
// Using the concrete proto type avoids the silent-drop bug (any → type assertion failure).
type Dispatcher interface {
	Send(ctx context.Context, nodeID uuid.UUID, msg *agentv1.ServerMessage) error
}
