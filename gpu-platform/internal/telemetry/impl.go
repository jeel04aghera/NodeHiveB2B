package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ServiceImpl struct {
	db *pgxpool.Pool
	// rawRetention mirrors RetentionPolicy.RawMetrics so reads know where raw
	// samples end and hourly rollups begin.
	rawRetention time.Duration
}

type Option func(*ServiceImpl)

// WithRawRetention aligns the read-side raw/rollup split with the retention sweep.
func WithRawRetention(d time.Duration) Option {
	return func(s *ServiceImpl) {
		if d > 0 {
			s.rawRetention = d
		}
	}
}

func NewService(db *pgxpool.Pool, opts ...Option) Service {
	s := &ServiceImpl{db: db, rawRetention: DefaultRetention().RawMetrics}
	for _, o := range opts {
		o(s)
	}
	return s
}

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

	// Raw samples exist only inside the retention window; older history lives in
	// gpu_metrics_hourly (weighted by sample_count). The boundary matches the
	// retention sweep's rollup cutoff, so the union has no gap and no overlap.
	boundary := time.Now().Add(-s.rawRetention).Truncate(time.Hour)

	var whereExtra string
	args := []any{orgID, q.From, q.To, boundary}
	switch q.Scope {
	case "node":
		whereExtra = " AND node_id=$5"
		args = append(args, q.ID)
	case "gpu":
		whereExtra = " AND gpu_id=$5"
		args = append(args, q.ID)
	}

	query := fmt.Sprintf(`
		WITH pts AS (
			SELECT ts, util_pct::float AS u, mem_used_mb::float AS mem, gpu_id, 1::float AS w
			  FROM gpu_metrics
			 WHERE org_id=$1 AND ts BETWEEN $2 AND $3 AND ts >= $4%s
			UNION ALL
			SELECT hour_ts, avg_util_pct::float, avg_mem_used_mb::float, gpu_id, sample_count::float
			  FROM gpu_metrics_hourly
			 WHERE org_id=$1 AND hour_ts BETWEEN $2 AND $3 AND hour_ts < $4%s
		)
		SELECT %s AS bucket,
		       sum(u*w)/NULLIF(sum(w),0),
		       sum(mem / NULLIF((SELECT memory_mb FROM gpus WHERE id=pts.gpu_id LIMIT 1),0) * 100 * w)
		           / NULLIF(sum(w),0)
		  FROM pts
		 GROUP BY bucket ORDER BY bucket`, whereExtra, whereExtra, bucketSQL)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		var util, mem *float64
		if err := rows.Scan(&p.TS, &util, &mem); err != nil {
			return nil, err
		}
		if util != nil {
			p.UtilPct = float32(*util)
		}
		if mem != nil {
			p.MemPct = float32(*mem)
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
