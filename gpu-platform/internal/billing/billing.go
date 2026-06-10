// Package billing is the Billing/Chargeback module.
// Owns tables: usage_records (immutable), cost_records, rate_cards.
// Produces events: events.UsageRecorded.
// Consumes events: events.WorkloadStarted/Stopped + telemetry rollups -> usage records.
package billing

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type ChargebackRow struct {
	GroupKey string  `json:"group_key"`
	GPUHours float64 `json:"gpu_hours"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	UtilPct  float64 `json:"util_pct"` // avg GPU utilization over metered records
}

type ChargebackReport struct {
	From        time.Time      `json:"from"`
	To          time.Time      `json:"to"`
	GroupBy     string         `json:"group_by"`
	Rows        []ChargebackRow `json:"rows"`
	Total       float64        `json:"total"`
	Currency    string         `json:"currency"`
	CoveragePct float64        `json:"coverage_pct"`
}

// LedgerEntry is one row of the org credit ledger.
type LedgerEntry struct {
	ID          string    `json:"id"`
	Delta       float64   `json:"delta"`
	Balance     float64   `json:"balance"`
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
	WorkloadID  *string   `json:"workload_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreditSummary is the org's current credit position.
type CreditSummary struct {
	Balance       float64       `json:"balance"`
	TotalGranted  float64       `json:"total_granted"`
	TotalSpent    float64       `json:"total_spent"`
	MonthSpent    float64       `json:"month_spent"`
	RecentEntries []LedgerEntry `json:"recent_entries"`
}

type Service interface {
	// RecordUsage appends an immutable usage record (insert-only).
	RecordUsage(ctx context.Context, rec domain.UsageRecord) error

	// AuthorizeLaunch is the admission gate: positive credit balance and no exhausted
	// budget in the workload's scopes. Returns ErrInsufficientCredit/ErrBudgetExceeded.
	// A no-op when enforcement is disabled (advisory deployments).
	AuthorizeLaunch(ctx context.Context, orgID uuid.UUID, departmentID, projectID *uuid.UUID) error
	// MeterWorkload bills a workload's unmetered slice (watermark → now/stopped_at)
	// atomically: usage records, cost records, ledger debit, watermark advance.
	// Idempotent — safe to call from both stop-time settlement and the sweep.
	MeterWorkload(ctx context.Context, workloadID uuid.UUID) error
	// MeterRunning meters every workload owing a billable slice, including terminal
	// workloads whose final settlement was lost to a crash. Run on a ticker.
	MeterRunning(ctx context.Context) (int, error)

	Chargeback(ctx context.Context, orgID uuid.UUID, from, to time.Time, groupBy string) (ChargebackReport, error)
	SetRate(ctx context.Context, orgID uuid.UUID, gpuModel string, ratePerHour float64, currency string) (domain.RateCard, error)
	ListRates(ctx context.Context, orgID uuid.UUID) ([]domain.RateCard, error)

	// Credit ledger.
	CreditSummary(ctx context.Context, orgID uuid.UUID) (CreditSummary, error)
	Ledger(ctx context.Context, orgID uuid.UUID, limit int) ([]LedgerEntry, error)
	// AddCredit posts a positive entry (grant/topup/adjustment) and returns the new balance.
	AddCredit(ctx context.Context, orgID uuid.UUID, amount float64, kind, description string) (float64, error)
}

type Store interface {
	InsertUsage(ctx context.Context, rec domain.UsageRecord) error
	InsertCost(ctx context.Context, rec domain.CostRecord) error
	UpsertRate(ctx context.Context, rc domain.RateCard) (domain.RateCard, error)
	ListRates(ctx context.Context, orgID uuid.UUID) ([]domain.RateCard, error)
	AggregateChargeback(ctx context.Context, orgID uuid.UUID, from, to time.Time, groupBy string) ([]ChargebackRow, error)
}
