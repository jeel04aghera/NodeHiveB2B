package policy

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ServiceImpl struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) Service { return &ServiceImpl{db: db} }

func (s *ServiceImpl) EvaluateIdle(ctx context.Context, orgID uuid.UUID) ([]IdleFinding, error) {
	threshold, durationMin := s.orgIdleSettings(ctx, orgID)
	since := time.Now().Add(-time.Duration(durationMin) * time.Minute)

	// GPUs with enough recent metrics AND avg util below threshold
	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT
		  g.id,
		  coalesce(avg(m.util_pct), 0)                            AS avg_util,
		  extract(epoch FROM now() - max(m.ts))::bigint            AS idle_sec,
		  w.id                                                      AS workload_id,
		  coalesce(rc.rate_per_gpu_hour, 0)                        AS rate
		FROM gpus g
		LEFT JOIN gpu_metrics m
		  ON m.gpu_id = g.id AND m.ts >= $2
		LEFT JOIN workload_gpus wg
		  ON wg.gpu_id = g.id AND wg.detached_at IS NULL
		LEFT JOIN workloads w
		  ON w.id = wg.workload_id AND w.status = 'running'
		LEFT JOIN rate_cards rc
		  ON rc.org_id = g.org_id AND rc.gpu_model = g.model AND rc.effective_to IS NULL
		WHERE g.org_id = $1
		GROUP BY g.id, w.id, rc.rate_per_gpu_hour
		HAVING count(m.id) >= 3    -- at least 3 data points
		   AND avg(m.util_pct) < $3
	`, ), orgID, since, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []IdleFinding
	for rows.Next() {
		var f IdleFinding
		var avgUtil float64
		var rate float64
		var wid *uuid.UUID
		if err := rows.Scan(&f.GPUID, &avgUtil, &f.IdleSeconds, &wid, &rate); err != nil {
			return nil, err
		}
		f.WorkloadID = wid
		f.IdleCost = float64(f.IdleSeconds) / 3600 * rate
		findings = append(findings, f)
	}
	return findings, rows.Err()
}

func (s *ServiceImpl) IdleReport(ctx context.Context, orgID uuid.UUID, from, to time.Time) (IdleReport, error) {
	findings, err := s.EvaluateIdle(ctx, orgID)
	if err != nil {
		return IdleReport{}, err
	}
	report := IdleReport{From: from, To: to}
	for _, f := range findings {
		report.IdleGPUs = append(report.IdleGPUs, f)
		report.TotalIdleCost += f.IdleCost
	}
	return report, nil
}

func (s *ServiceImpl) SweepIdle(ctx context.Context, stop StopFn) (int, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM organizations`)
	if err != nil {
		return 0, err
	}
	var orgIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		orgIDs = append(orgIDs, id)
	}
	rows.Close()

	stopped := 0
	for _, orgID := range orgIDs {
		findings, err := s.EvaluateIdle(ctx, orgID)
		if err != nil {
			continue
		}
		for _, f := range findings {
			if f.WorkloadID == nil {
				continue // unmanaged idle — alert only, don't stop
			}
			if err := stop(ctx, *f.WorkloadID); err == nil {
				stopped++
			}
		}
	}
	return stopped, nil
}

func (s *ServiceImpl) orgIdleSettings(ctx context.Context, orgID uuid.UUID) (thresholdPct float64, durationMin int) {
	thresholdPct = 10 // defaults
	durationMin = 30
	_ = s.db.QueryRow(ctx, `
		SELECT
		  coalesce((settings->>'idle_threshold_pct')::float, 10),
		  coalesce((settings->>'idle_duration_min')::int, 30)
		FROM organizations WHERE id = $1`, orgID).
		Scan(&thresholdPct, &durationMin)
	return
}
