package billing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type ServiceImpl struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) Service { return &ServiceImpl{db: db} }

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

// RecordWorkloadUsage is called when a workload stops — computes GPU-hours and inserts records.
func (s *ServiceImpl) RecordWorkloadUsage(ctx context.Context, workloadID, orgID uuid.UUID, start, end time.Time) error {
	rows, err := s.db.Query(ctx,
		`SELECT wg.gpu_id, g.model
		   FROM workload_gpus wg JOIN gpus g ON g.id=wg.gpu_id
		  WHERE wg.workload_id=$1`, workloadID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type gpuEntry struct {
		id    uuid.UUID
		model string
	}
	var gpus []gpuEntry
	for rows.Next() {
		var e gpuEntry
		if err := rows.Scan(&e.id, &e.model); err != nil {
			return err
		}
		gpus = append(gpus, e)
	}

	gpuSeconds := int64(end.Sub(start).Seconds())
	var total float64
	for _, g := range gpus {
		// Find applicable rate
		var rate float64
		var currency string
		var rateCardID *uuid.UUID
		var rcID uuid.UUID
		err := s.db.QueryRow(ctx,
			`SELECT id, rate_per_gpu_hour, currency FROM rate_cards
			  WHERE org_id=$1 AND gpu_model=$2 AND effective_from <= $3
			    AND (effective_to IS NULL OR effective_to > $3)
			  ORDER BY effective_from DESC LIMIT 1`,
			orgID, g.model, end).Scan(&rcID, &rate, &currency)
		if err == nil {
			rateCardID = &rcID
		} else {
			// Use org default rate
			var dr float64
			var cur string
			_ = s.db.QueryRow(ctx,
				`SELECT (settings->>'default_rate')::float, coalesce(settings->>'currency','USD')
				   FROM organizations WHERE id=$1`, orgID).Scan(&dr, &cur)
			rate = dr
			currency = cur
			if currency == "" {
				currency = "USD"
			}
		}

		// Get workload details for user/project FK
		var userID uuid.UUID
		var projectID *uuid.UUID
		_ = s.db.QueryRow(ctx,
			`SELECT user_id, project_id FROM workloads WHERE id=$1`, workloadID).
			Scan(&userID, &projectID)

		// Avg utilization for this GPU over the workload window (real telemetry).
		var avgUtil *float64
		_ = s.db.QueryRow(ctx,
			`SELECT avg(util_pct) FROM gpu_metrics
			  WHERE gpu_id=$1 AND ts >= $2 AND ts <= $3`, g.id, start, end).Scan(&avgUtil)

		// Insert usage record
		var usageID int64
		err = s.db.QueryRow(ctx,
			`INSERT INTO usage_records
			   (org_id, workload_id, gpu_id, project_id, user_id, period_start, period_end, gpu_seconds, avg_util_pct, source)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'workload')
			 RETURNING id`,
			orgID, workloadID, g.id, projectID, userID, start, end, gpuSeconds, avgUtil).Scan(&usageID)
		if err != nil {
			continue
		}

		amount := float64(gpuSeconds) / 3600 * rate
		_, _ = s.db.Exec(ctx,
			`INSERT INTO cost_records
			   (org_id, usage_record_id, rate_card_id, project_id, user_id,
			    period_start, period_end, gpu_seconds, rate_per_gpu_hour, currency, amount)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			orgID, usageID, rateCardID, projectID, userID, start, end, gpuSeconds, rate, currency, amount)
		total += amount
	}

	// Debit the org's credit balance for the finalized cost of this workload run.
	// The ledger is denominated in the product's display currency (INR); rate cards
	// and cost_records are in USD, so convert here.
	if total > 0 {
		_ = s.postLedger(ctx, orgID, -total*usdToINR, "charge", "Workload usage", &workloadID)
	}
	return nil
}

// usdToINR is the display-currency conversion used for the credit ledger. It mirrors
// the frontend's USD_TO_INR so credit charges line up with displayed spend.
const usdToINR = 83.0

// postLedger appends a credit_ledger entry, computing the running balance from
// the most recent entry for the org. Not safe under heavy concurrency, but the
// metering path is serialized per workload-stop, which is sufficient here.
func (s *ServiceImpl) postLedger(ctx context.Context, orgID uuid.UUID, delta float64, kind, desc string, workloadID *uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO credit_ledger (org_id, delta, balance, kind, description, workload_id)
		 VALUES ($1, $2,
		         coalesce((SELECT balance FROM credit_ledger WHERE org_id=$1
		                    ORDER BY created_at DESC, id DESC LIMIT 1), 0) + $2,
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
	if err := s.postLedger(ctx, orgID, amount, kind, description, nil); err != nil {
		return 0, err
	}
	sum, err := s.CreditSummary(ctx, orgID)
	return sum.Balance, err
}

func (s *ServiceImpl) CreditSummary(ctx context.Context, orgID uuid.UUID) (CreditSummary, error) {
	var out CreditSummary
	_ = s.db.QueryRow(ctx,
		`SELECT
		   coalesce((SELECT balance FROM credit_ledger WHERE org_id=$1 ORDER BY created_at DESC, id DESC LIMIT 1), 0),
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
		  ORDER BY created_at DESC, id DESC LIMIT $2`, orgID, limit)
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
