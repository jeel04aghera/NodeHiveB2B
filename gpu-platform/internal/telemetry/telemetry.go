// Package telemetry is the Telemetry module.
// Owns tables: gpu_metrics (partitioned) + hourly rollups.
// Produces events: events.MetricsIngested. Consumes: agent MetricsBatch (via gateway).
package telemetry

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Sample struct {
	GPUID     uuid.UUID
	NodeID    uuid.UUID
	TS        time.Time
	UtilPct   float32
	MemUsedMB int32
	PowerW    float32
	TempC     float32
	ECCErrors int32
	ProcCount int32
}

type UtilQuery struct {
	Scope    string // "fleet" | "node" | "gpu"
	ID       uuid.UUID
	From, To time.Time
	Interval time.Duration
}

type Point struct {
	TS      time.Time
	UtilPct float32
	MemPct  float32
}

type Summary struct {
	GPUTotal        int
	GPUsIdle        int
	AvgUtilPct      float32
	IdleCost24h     float64
	WorkloadsActive int
}

type Service interface {
	Ingest(ctx context.Context, orgID uuid.UUID, samples []Sample) error
	Utilization(ctx context.Context, orgID uuid.UUID, q UtilQuery) ([]Point, error)
	FleetSummary(ctx context.Context, orgID uuid.UUID) (Summary, error)
	// SweepRetention rolls raw samples into hourly aggregates and ages out
	// time-series exhaust (Phase 4 — metrics retention). Billing tables untouched.
	SweepRetention(ctx context.Context, p RetentionPolicy) (RetentionStats, error)
}

type Store interface {
	InsertSamples(ctx context.Context, samples []Sample) error
	QueryUtilization(ctx context.Context, q UtilQuery) ([]Point, error)
	Summary(ctx context.Context, orgID uuid.UUID) (Summary, error)
}
