package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ServiceImpl struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) Service { return &ServiceImpl{db: db} }

func (s *ServiceImpl) Ingest(ctx context.Context, orgID uuid.UUID, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	// Resolve GPU UUIDs to DB ids (batch lookup)
	uuids := make([]string, 0, len(samples))
	for _, sm := range samples {
		uuids = append(uuids, sm.GPUID.String())
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, node_id FROM gpus WHERE id = ANY($1)`, uuids)
	if err != nil {
		return err
	}
	type gpuInfo struct{ id, nodeID uuid.UUID }
	known := map[uuid.UUID]gpuInfo{}
	for rows.Next() {
		var g gpuInfo
		_ = rows.Scan(&g.id, &g.nodeID)
		known[g.id] = g
	}
	rows.Close()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, sm := range samples {
		g, ok := known[sm.GPUID]
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO gpu_metrics (org_id, gpu_id, node_id, ts, util_pct, mem_used_mb, power_w, temp_c, ecc_errors, proc_count)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			orgID, g.id, g.nodeID, sm.TS, sm.UtilPct, sm.MemUsedMB, sm.PowerW, sm.TempC, sm.ECCErrors, sm.ProcCount); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *ServiceImpl) Utilization(ctx context.Context, orgID uuid.UUID, q UtilQuery) ([]Point, error) {
	interval := q.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	bucketSQL := fmt.Sprintf("date_trunc('minute', ts) + (EXTRACT(MINUTE FROM ts)::int / %d) * interval '%d minutes'",
		int(interval.Minutes()), int(interval.Minutes()))

	var whereExtra string
	args := []any{orgID, q.From, q.To}
	switch q.Scope {
	case "node":
		whereExtra = " AND node_id=$4"
		args = append(args, q.ID)
	case "gpu":
		whereExtra = " AND gpu_id=$4"
		args = append(args, q.ID)
	}

	query := fmt.Sprintf(`
		SELECT %s AS bucket, avg(util_pct), avg(mem_used_mb::float / NULLIF(
			(SELECT memory_mb FROM gpus WHERE id=gpu_id LIMIT 1),0)*100)
		  FROM gpu_metrics
		 WHERE org_id=$1 AND ts BETWEEN $2 AND $3%s
		 GROUP BY bucket ORDER BY bucket`, bucketSQL, whereExtra)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		var util, mem *float32
		if err := rows.Scan(&p.TS, &util, &mem); err != nil {
			return nil, err
		}
		if util != nil {
			p.UtilPct = *util
		}
		if mem != nil {
			p.MemPct = *mem
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *ServiceImpl) FleetSummary(ctx context.Context, orgID uuid.UUID) (Summary, error) {
	var sum Summary
	// GPU totals
	_ = s.db.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE status='idle')
		   FROM gpus WHERE org_id=$1`, orgID).
		Scan(&sum.GPUTotal, &sum.GPUsIdle)

	// Average utilization over last 10 min
	_ = s.db.QueryRow(ctx,
		`SELECT coalesce(avg(util_pct),0) FROM gpu_metrics
		  WHERE org_id=$1 AND ts > now() - interval '10 minutes'`, orgID).
		Scan(&sum.AvgUtilPct)

	// Active workloads
	_ = s.db.QueryRow(ctx,
		`SELECT count(*) FROM workloads WHERE org_id=$1 AND status IN ('pending','running')`, orgID).
		Scan(&sum.WorkloadsActive)

	// Idle cost 24h (estimate from idle GPUs * default rate)
	_ = s.db.QueryRow(ctx,
		`SELECT coalesce(sum(r.rate_per_gpu_hour * 24),0)
		   FROM gpus g
		   JOIN rate_cards r ON r.org_id=g.org_id AND r.gpu_model=g.model
		  WHERE g.org_id=$1 AND g.status='idle'
		    AND r.effective_to IS NULL`, orgID).
		Scan(&sum.IdleCost24h)

	return sum, nil
}
