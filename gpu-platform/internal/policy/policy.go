// Package policy is the Policy/Idle module.
// Owns: no tables of its own (derives over telemetry + gpus). Reads org idle settings.
// Produces events: events.GPUIdleDetected. Consumes: events.MetricsIngested.
package policy

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type IdleFinding struct {
	GPUID       uuid.UUID
	IdleSeconds int64
	IdleCost    float64
	// WorkloadID is set only when a managed workload occupies the idle GPU and is
	// therefore eligible for safe auto-stop. Unmanaged idle is alert-only.
	WorkloadID *uuid.UUID
}

type IdleReport struct {
	From, To       time.Time
	IdleGPUs       []IdleFinding
	TotalIdleCost  float64
}

// StopFn is called by SweepIdle when a managed workload should be auto-stopped.
type StopFn func(ctx context.Context, workloadID uuid.UUID) error

type Service interface {
	// EvaluateIdle runs on each rollup; flags GPUs below threshold for >= duration.
	EvaluateIdle(ctx context.Context, orgID uuid.UUID) ([]IdleFinding, error)
	IdleReport(ctx context.Context, orgID uuid.UUID, from, to time.Time) (IdleReport, error)
	// SweepIdle evaluates all orgs and calls stop for each eligible managed workload.
	SweepIdle(ctx context.Context, stop StopFn) (stopped int, err error)
}
