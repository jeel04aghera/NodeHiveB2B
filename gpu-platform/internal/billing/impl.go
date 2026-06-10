package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodehive/gpu-platform/internal/domain"
)

// Admission errors. The API maps these to 402 so the frontend can tell the user
// exactly why the launch was refused.
var (
	ErrInsufficientCredit = errors.New("insufficient credit balance")
	ErrBudgetExceeded     = errors.New("budget exceeded")
)

// meterMinSlice is the smallest time slice the periodic sweep bills for a RUNNING
// workload (terminal settlement always bills the remainder, however small). It bounds
// usage/cost row volume to ~288 rows/day per GPU while keeping the balance fresh
// enough for admission checks.
const meterMinSlice = 5 * time.Minute

type ServiceImpl struct {
	db      *pgxpool.Pool
	enforce bool // credit/budget admission enforcement (BILLING_ENFORCE)
}

type Option func(*ServiceImpl)

// WithEnforcement toggles credit-balance and budget admission checks at workload
// launch. Disabled = advisory mode (internal chargeback deployments).
func WithEnforcement(b bool) Option { return func(s *ServiceImpl) { s.enforce = b } }

func NewService(db *pgxpool.Pool, opts ...Option) Service {
	s := &ServiceImpl{db: db, enforce: true}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *ServiceImpl) RecordUsage(ctx context.Context, rec domain.UsageRecord) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO usage_records (org_id, workload_id, gpu_id, project_id, user_id,
		                            period_start, period_end, gpu_seconds, avg_util_pct, max_mem_mb, source)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		rec.OrgID, rec.WorkloadID, rec.GPUID, rec.ProjectID, rec.UserID,
		rec.PeriodStart, rec.PeriodEnd, rec.GPUSeconds, rec.AvgUtilPct, rec.MaxMemMB, rec.Source)
	return err
}

func (s *ServiceImpl) Chargeback(ctx context.Context, orgID uuid.UUID, from, to time.Time, groupBy string) (ChargebackReport, error) {
	var groupCol, joinClause string
	switch groupBy {
	case "project":
		groupCol = "coalesce(p.name, 'unallocated')"
		joinClause = "LEFT JOIN projects p ON p.id = u.project_id"
	case "department":
		groupCol = "coalesce(d.name, 'unassigned')"
		joinClause = "LEFT JOIN workloads w ON w.id = u.workload_id LEFT JOIN departments d ON d.id = w.department_id"
	case "gpu_type", "gpu":
		groupBy = "gpu_type"
		groupCol = "coalesce(g.model, 'unknown')"
		joinClause = "LEFT JOIN gpus g ON g.id = u.gpu_id"
	default: // user
		groupBy = "user"
		groupCol = "coalesce(us.email, 'system')"
		joinClause = "LEFT JOIN users us ON us.id = u.user_id"
	}

	// avg_util_pct lives on usage_records; weight isn't applied (simple mean) — good
	// enough for a chargeback utilization column.
	query := fmt.Sprintf(`
		SELECT %s, sum(c.gpu_seconds), sum(c.amount), max(c.currency),
		       coalesce(avg(NULLIF(u.avg_util_pct,0)),0)
		  FROM cost_records c
		  JOIN usage_records u ON u.id = c.usage_record_id
		  %s
		 WHERE c.org_id=$1 AND c.period_start >= $2 AND c.period_end <= $3
		 GROUP BY 1 ORDER BY sum(c.amount) DESC`, groupCol, joinClause)

	rows, err := s.db.Query(ctx, query, orgID, from, to)
	if err != nil {
		return ChargebackReport{}, err
	}
	defer rows.Close()

	report := ChargebackReport{From: from, To: to, GroupBy: groupBy, Currency: "USD"}
	for rows.Next() {
		var row ChargebackRow
		var gpuSec int64
		var util float64
		if err := rows.Scan(&row.GroupKey, &gpuSec, &row.Amount, &row.Currency, &util); err != nil {
			return ChargebackReport{}, err
		}
		row.GPUHours = float64(gpuSec) / 3600
		row.UtilPct = util
		report.Rows = append(report.Rows, row)
		report.Total += row.Amount
	}
	if err := rows.Err(); err != nil {
		return ChargebackReport{}, err
	}

	// Coverage: compare actual metered periods to wall time
	var meteredSeconds float64
	_ = s.db.QueryRow(ctx,
		`SELECT coalesce(sum(gpu_seconds),0) FROM usage_records
		  WHERE org_id=$1 AND period_start >= $2 AND period_end <= $3`,
		orgID, from, to).Scan(&meteredSeconds)
	wallSeconds := to.Sub(from).Seconds() * float64(report.gpuCount(ctx, s.db, orgID))
	if wallSeconds > 0 {
		report.CoveragePct = meteredSeconds / wallSeconds * 100
		if report.CoveragePct > 100 {
			report.CoveragePct = 100
		}
	}
	return report, nil
}

func (r *ChargebackReport) gpuCount(ctx context.Context, db *pgxpool.Pool, orgID uuid.UUID) int {
	var n int
	_ = db.QueryRow(ctx, `SELECT count(*) FROM gpus WHERE org_id=$1`, orgID).Scan(&n)
	return n
}

func (s *ServiceImpl) SetRate(ctx context.Context, orgID uuid.UUID, gpuModel string, ratePerHour float64, currency string) (domain.RateCard, error) {
	// Expire existing rate for this model
	_, _ = s.db.Exec(ctx,
		`UPDATE rate_cards SET effective_to=now() WHERE org_id=$1 AND gpu_model=$2 AND effective_to IS NULL`,
		orgID, gpuModel)

	var rc domain.RateCard
	err := s.db.QueryRow(ctx,
		`INSERT INTO rate_cards (org_id, gpu_model, rate_per_gpu_hour, currency)
		 VALUES ($1,$2,$3,$4)
		 RETURNING id, org_id, gpu_model, rate_per_gpu_hour, currency, effective_from`,
		orgID, gpuModel, ratePerHour, currency).
		Scan(&rc.ID, &rc.OrgID, &rc.GPUModel, &rc.RatePerGPUHour, &rc.Currency, &rc.EffectiveFrom)
	return rc, err
}

func (s *ServiceImpl) ListRates(ctx context.Context, orgID uuid.UUID) ([]domain.RateCard, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, gpu_model, rate_per_gpu_hour, currency, effective_from, effective_to
		   FROM rate_cards WHERE org_id=$1 AND effective_to IS NULL ORDER BY gpu_model`,
		orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RateCard
	for rows.Next() {
		var rc domain.RateCard
		if err := rows.Scan(&rc.ID, &rc.OrgID, &rc.GPUModel, &rc.RatePerGPUHour,
			&rc.Currency, &rc.EffectiveFrom, &rc.EffectiveTo); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// ── Admission (launch-time enforcement) ────────────────────────────────────────

// AuthorizeLaunch decides whether an org may start a new workload: the credit
// balance must be positive and no applicable budget (organization scope, plus the
// workload's department/project scopes) may already be exhausted month-to-date.
// Enforcement is configuration-gated; advisory deployments always pass.
func (s *ServiceImpl) AuthorizeLaunch(ctx context.Context, orgID uuid.UUID, departmentID, projectID *uuid.UUID) error {
	if !s.enforce {
		return nil
	}
	var balance float64
	err := s.db.QueryRow(ctx,
		`SELECT balance FROM credit_ledger WHERE org_id=$1 ORDER BY seq DESC LIMIT 1`,
		orgID).Scan(&balance)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read balance: %w", err) // fail closed on DB errors
	}
	if balance <= 0 {
		return ErrInsufficientCredit
	}

	rows, err := s.db.Query(ctx,
		`SELECT scope_type, scope_id, amount FROM budgets
		  WHERE org_id=$1
		    AND (scope_type='organization'
		         OR (scope_type='department' AND scope_id=$2)
		         OR (scope_type='project'    AND scope_id=$3))`,
		orgID, departmentID, projectID)
	if err != nil {
		return fmt.Errorf("read budgets: %w", err)
	}
	defer rows.Close()
	type budget struct {
		scopeType string
		scopeID   *uuid.UUID
		amount    float64
	}
	var budgets []budget
	for rows.Next() {
		var b budget
		if err := rows.Scan(&b.scopeType, &b.scopeID, &b.amount); err != nil {
			return err
		}
		budgets = append(budgets, b)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, b := range budgets {
		if b.amount <= 0 {
			continue
		}
		if s.scopeSpendINR(ctx, orgID, b.scopeType, b.scopeID) >= b.amount {
			return fmt.Errorf("%w: %s budget is exhausted for this month", ErrBudgetExceeded, b.scopeType)
		}
	}
	return nil
}

// scopeSpendINR is month-to-date spend for a budget scope, in the ledger currency.
// Department spend goes through usage_records to reach the workload (cost_records
// has no workload_id column).
func (s *ServiceImpl) scopeSpendINR(ctx context.Context, orgID uuid.UUID, scopeType string, scopeID *uuid.UUID) float64 {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var usd *float64
	switch scopeType {
	case "project":
		_ = s.db.QueryRow(ctx,
			`SELECT sum(amount) FROM cost_records
			  WHERE org_id=$1 AND project_id=$2 AND period_start >= $3`,
			orgID, scopeID, monthStart).Scan(&usd)
	case "department":
		_ = s.db.QueryRow(ctx,
			`SELECT sum(c.amount)
			   FROM cost_records c
			   JOIN usage_records u ON u.id = c.usage_record_id
			   JOIN workloads w ON w.id = u.workload_id
			  WHERE c.org_id=$1 AND w.department_id=$2 AND c.period_start >= $3`,
			orgID, scopeID, monthStart).Scan(&usd)
	default: // organization
		_ = s.db.QueryRow(ctx,
			`SELECT sum(amount) FROM cost_records WHERE org_id=$1 AND period_start >= $2`,
			orgID, monthStart).Scan(&usd)
	}
	if usd == nil {
		return 0
	}
	return *usd * usdToINR
}

// ── Metering (periodic accrual + terminal settlement) ──────────────────────────

// MeterWorkload bills the unmetered time slice of one workload: from the watermark
// (metered_until, or started_at for the first slice) to now for active workloads, or
// to stopped_at for terminal ones. Usage records, cost records, the ledger debit and
// the watermark advance commit in ONE transaction under a row lock, so the sweep and
// stop-time settlement can race freely without double-billing, and a crash anywhere
// re-bills nothing and loses nothing (the next sweep resumes from the watermark).
func (s *ServiceImpl) MeterWorkload(ctx context.Context, workloadID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID, userID uuid.UUID
	var projectID *uuid.UUID
	var status string
	var startedAt, stoppedAt, meteredUntil *time.Time
	err = tx.QueryRow(ctx,
		`SELECT org_id, user_id, project_id, status, started_at, stopped_at, metered_until
		   FROM workloads WHERE id=$1 FOR UPDATE`, workloadID).
		Scan(&orgID, &userID, &projectID, &status, &startedAt, &stoppedAt, &meteredUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // workload gone; nothing to bill
	}
	if err != nil {
		return fmt.Errorf("load workload: %w", err)
	}
	if startedAt == nil {
		return nil // never started; nothing billable
	}

	from := *startedAt
	if meteredUntil != nil && meteredUntil.After(from) {
		from = *meteredUntil
	}
	terminal := status == "stopped" || status == "failed"
	to := time.Now()
	if terminal {
		if stoppedAt == nil {
			return nil
		}
		to = *stoppedAt
	}
	if !to.After(from) {
		return nil // fully settled
	}
	if !terminal && to.Sub(from) < meterMinSlice {
		return nil // wait for a fuller slice
	}

	// GPUs attached to this workload (rows persist after detach, so terminal
	// settlement still sees them).
	rows, err := tx.Query(ctx,
		`SELECT wg.gpu_id, g.model
		   FROM workload_gpus wg JOIN gpus g ON g.id=wg.gpu_id
		  WHERE wg.workload_id=$1`, workloadID)
	if err != nil {
		return err
	}
	type gpuEntry struct {
		id    uuid.UUID
		model string
	}
	var gpus []gpuEntry
	for rows.Next() {
		var e gpuEntry
		if err := rows.Scan(&e.id, &e.model); err != nil {
			rows.Close()
			return err
		}
		gpus = append(gpus, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	gpuSeconds := int64(to.Sub(from).Seconds())
	var total float64
	for _, g := range gpus {
		var rate float64
		var currency string
		var rateCardID *uuid.UUID
		var rcID uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT id, rate_per_gpu_hour, currency FROM rate_cards
			  WHERE org_id=$1 AND gpu_model=$2 AND effective_from <= $3
			    AND (effective_to IS NULL OR effective_to > $3)
			  ORDER BY effective_from DESC LIMIT 1`,
			orgID, g.model, to).Scan(&rcID, &rate, &currency)
		if err == nil {
			rateCardID = &rcID
		} else {
			var dr float64
			var cur string
			_ = tx.QueryRow(ctx,
				`SELECT (settings->>'default_rate')::float, coalesce(settings->>'currency','USD')
				   FROM organizations WHERE id=$1`, orgID).Scan(&dr, &cur)
			rate = dr
			currency = cur
			if currency == "" {
				currency = "USD"
			}
		}

		// Avg utilization for this GPU over the slice (real telemetry).
		var avgUtil *float64
		_ = tx.QueryRow(ctx,
			`SELECT avg(util_pct) FROM gpu_metrics
			  WHERE gpu_id=$1 AND ts >= $2 AND ts <= $3`, g.id, from, to).Scan(&avgUtil)

		var usageID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO usage_records
			   (org_id, workload_id, gpu_id, project_id, user_id, period_start, period_end, gpu_seconds, avg_util_pct, source)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'workload')
			 RETURNING id`,
			orgID, workloadID, g.id, projectID, userID, from, to, gpuSeconds, avgUtil).Scan(&usageID); err != nil {
			return fmt.Errorf("insert usage: %w", err)
		}

		amount := float64(gpuSeconds) / 3600 * rate
		if _, err := tx.Exec(ctx,
			`INSERT INTO cost_records
			   (org_id, usage_record_id, rate_card_id, project_id, user_id,
			    period_start, period_end, gpu_seconds, rate_per_gpu_hour, currency, amount)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			orgID, usageID, rateCardID, projectID, userID, from, to, gpuSeconds, rate, currency, amount); err != nil {
			return fmt.Errorf("insert cost: %w", err)
		}
		total += amount
	}

	// Ledger debit (display currency) + watermark advance, same transaction.
	if total > 0 {
		if err := postLedgerTx(ctx, tx, orgID, -total*usdToINR, "charge", "Workload usage", &workloadID); err != nil {
			return fmt.Errorf("ledger debit: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE workloads SET metered_until=$2 WHERE id=$1`, workloadID, to); err != nil {
		return fmt.Errorf("advance watermark: %w", err)
	}
	return tx.Commit(ctx)
}

// MeterRunning finds every workload owing a billable slice — active ones past the
// minimum slice age, and terminal ones whose final slice was never settled (e.g. the
// control plane died before stop-time settlement) — and meters each. Returns the
// number of workloads billed.
func (s *ServiceImpl) MeterRunning(ctx context.Context) (int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM workloads
		 WHERE started_at IS NOT NULL
		   AND (
		     (status IN ('running','stopping')
		        AND COALESCE(metered_until, started_at) < now() - interval '5 minutes')
		     OR (status IN ('stopped','failed') AND stopped_at IS NOT NULL
		        AND COALESCE(metered_until, started_at) < stopped_at)
		   )`)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	metered := 0
	var firstErr error
	for _, id := range ids {
		if err := s.MeterWorkload(ctx, id); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("meter %s: %w", id, err)
			}
			continue
		}
		metered++
	}
	return metered, firstErr
}

// usdToINR is the display-currency conversion used for the credit ledger. It mirrors
// the frontend's USD_TO_INR so credit charges line up with displayed spend.
const usdToINR = 83.0

// postLedgerTx appends a credit_ledger entry inside the caller's transaction. A
// per-org advisory transaction lock serializes concurrent writers (top-up racing the
// metering sweep), so the read-compute-write of the running balance is safe; seq
// gives rows a stable total order for "latest balance" reads.
func postLedgerTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, delta float64, kind, desc string, workloadID *uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 42))`, orgID.String()); err != nil {
		return fmt.Errorf("ledger lock: %w", err)
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO credit_ledger (org_id, delta, balance, kind, description, workload_id)
		 VALUES ($1, $2,
		         coalesce((SELECT balance FROM credit_ledger WHERE org_id=$1
		                    ORDER BY seq DESC LIMIT 1), 0) + $2,
		         $3, $4, $5)`,
		orgID, delta, kind, desc, workloadID)
	return err
}

func (s *ServiceImpl) AddCredit(ctx context.Context, orgID uuid.UUID, amount float64, kind, description string) (float64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	if kind == "" {
		kind = "topup"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := postLedgerTx(ctx, tx, orgID, amount, kind, description, nil); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	sum, err := s.CreditSummary(ctx, orgID)
	return sum.Balance, err
}

func (s *ServiceImpl) CreditSummary(ctx context.Context, orgID uuid.UUID) (CreditSummary, error) {
	var out CreditSummary
	_ = s.db.QueryRow(ctx,
		`SELECT
		   coalesce((SELECT balance FROM credit_ledger WHERE org_id=$1 ORDER BY seq DESC LIMIT 1), 0),
		   coalesce(sum(delta) FILTER (WHERE delta > 0), 0),
		   coalesce(-sum(delta) FILTER (WHERE delta < 0), 0),
		   coalesce(-sum(delta) FILTER (WHERE delta < 0 AND created_at >= date_trunc('month', now())), 0)
		 FROM credit_ledger WHERE org_id=$1`,
		orgID).Scan(&out.Balance, &out.TotalGranted, &out.TotalSpent, &out.MonthSpent)
	entries, err := s.Ledger(ctx, orgID, 10)
	if err != nil {
		return out, err
	}
	out.RecentEntries = entries
	return out, nil
}

func (s *ServiceImpl) Ledger(ctx context.Context, orgID uuid.UUID, limit int) ([]LedgerEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, delta, balance, kind, description, workload_id, created_at
		   FROM credit_ledger WHERE org_id=$1
		  ORDER BY seq DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerEntry{}
	for rows.Next() {
		var e LedgerEntry
		var wl *uuid.UUID
		if err := rows.Scan(&e.ID, &e.Delta, &e.Balance, &e.Kind, &e.Description, &wl, &e.CreatedAt); err != nil {
			return nil, err
		}
		if wl != nil {
			s := wl.String()
			e.WorkloadID = &s
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
