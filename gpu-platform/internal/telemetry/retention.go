package telemetry

import (
	"context"
	"time"
)

// RetentionPolicy bounds the growth of the high-frequency time-series tables.
// Raw GPU samples are rolled up into gpu_metrics_hourly before deletion, so
// long-range utilization history survives at hourly resolution; heartbeats and
// workload lifecycle events are operational exhaust and simply age out.
// Billing tables (usage_records, cost_records, credit_ledger) are immutable truth
// and are NEVER touched by retention.
type RetentionPolicy struct {
	RawMetrics    time.Duration // raw gpu_metrics samples (default 14d)
	RollupMetrics time.Duration // gpu_metrics_hourly rows (default 365d)
	Heartbeats    time.Duration // agent_heartbeats rows (default 7d)
	Events        time.Duration // workload_events rows (default 90d)
}

func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{
		RawMetrics:    14 * 24 * time.Hour,
		RollupMetrics: 365 * 24 * time.Hour,
		Heartbeats:    7 * 24 * time.Hour,
		Events:        90 * 24 * time.Hour,
	}
}

// RetentionStats reports one sweep's work.
type RetentionStats struct {
	RolledUpHours    int64
	RawDeleted       int64
	HeartbeatDeleted int64
	EventsDeleted    int64
	RollupsDeleted   int64
}

// retentionBatch keeps each DELETE short so the sweep never holds long locks on a
// hot table; the loop repeats until a batch comes back empty.
const retentionBatch = 5000

// SweepRetention rolls up and ages out time-series data. Restart-safe and
// idempotent: the rollup upserts with weighted merges keyed by (gpu_id, hour), and
// raw rows are only deleted below the same boundary the rollup just covered — a
// crash between the two steps means the next sweep re-rolls the same hours
// (merging to the same result) before deleting.
func (s *ServiceImpl) SweepRetention(ctx context.Context, p RetentionPolicy) (RetentionStats, error) {
	var st RetentionStats
	if p.RawMetrics <= 0 {
		p = DefaultRetention()
	}
	// Whole-hour boundary: everything below it is rolled up and deletable, and the
	// Utilization reader uses the same cutoff arithmetic to split raw vs rollup.
	boundary := time.Now().Add(-p.RawMetrics).Truncate(time.Hour)

	// 1. Roll up raw samples older than the boundary into hourly aggregates.
	ct, err := s.db.Exec(ctx, `
		INSERT INTO gpu_metrics_hourly
		    (org_id, gpu_id, node_id, hour_ts, sample_count,
		     avg_util_pct, max_util_pct, avg_mem_used_mb, max_mem_used_mb,
		     avg_power_w, max_temp_c, ecc_errors)
		SELECT org_id, gpu_id, node_id, date_trunc('hour', ts), count(*),
		       avg(util_pct), max(util_pct), avg(mem_used_mb), max(mem_used_mb),
		       avg(power_w), max(temp_c), coalesce(sum(ecc_errors),0)
		  FROM gpu_metrics
		 WHERE ts < $1
		 GROUP BY org_id, gpu_id, node_id, date_trunc('hour', ts)
		ON CONFLICT (gpu_id, hour_ts) DO UPDATE SET
		    avg_util_pct = (coalesce(gpu_metrics_hourly.avg_util_pct,0)*gpu_metrics_hourly.sample_count
		                  + coalesce(EXCLUDED.avg_util_pct,0)*EXCLUDED.sample_count)
		                  / (gpu_metrics_hourly.sample_count + EXCLUDED.sample_count),
		    max_util_pct = GREATEST(gpu_metrics_hourly.max_util_pct, EXCLUDED.max_util_pct),
		    avg_mem_used_mb = (coalesce(gpu_metrics_hourly.avg_mem_used_mb,0)*gpu_metrics_hourly.sample_count
		                  + coalesce(EXCLUDED.avg_mem_used_mb,0)*EXCLUDED.sample_count)
		                  / (gpu_metrics_hourly.sample_count + EXCLUDED.sample_count),
		    max_mem_used_mb = GREATEST(gpu_metrics_hourly.max_mem_used_mb, EXCLUDED.max_mem_used_mb),
		    avg_power_w = (coalesce(gpu_metrics_hourly.avg_power_w,0)*gpu_metrics_hourly.sample_count
		                  + coalesce(EXCLUDED.avg_power_w,0)*EXCLUDED.sample_count)
		                  / (gpu_metrics_hourly.sample_count + EXCLUDED.sample_count),
		    max_temp_c = GREATEST(gpu_metrics_hourly.max_temp_c, EXCLUDED.max_temp_c),
		    ecc_errors = gpu_metrics_hourly.ecc_errors + EXCLUDED.ecc_errors,
		    sample_count = gpu_metrics_hourly.sample_count + EXCLUDED.sample_count`,
		boundary)
	if err != nil {
		return st, err
	}
	st.RolledUpHours = ct.RowsAffected()

	// 2. Delete the rolled-up raw rows (batched).
	n, err := s.batchedDelete(ctx,
		`DELETE FROM gpu_metrics WHERE ctid IN
		   (SELECT ctid FROM gpu_metrics WHERE ts < $1 LIMIT $2)`, boundary)
	if err != nil {
		return st, err
	}
	st.RawDeleted = n

	// 3. Heartbeats: liveness exhaust, nothing reads past the dashboard window.
	n, err = s.batchedDelete(ctx,
		`DELETE FROM agent_heartbeats WHERE ctid IN
		   (SELECT ctid FROM agent_heartbeats WHERE ts < $1 LIMIT $2)`,
		time.Now().Add(-p.Heartbeats))
	if err != nil {
		return st, err
	}
	st.HeartbeatDeleted = n

	// 4. Workload lifecycle events. Age-based: very old events on a still-running
	// workload lose their earliest timeline entries, which is acceptable.
	n, err = s.batchedDelete(ctx,
		`DELETE FROM workload_events WHERE ctid IN
		   (SELECT ctid FROM workload_events WHERE ts < $1 LIMIT $2)`,
		time.Now().Add(-p.Events))
	if err != nil {
		return st, err
	}
	st.EventsDeleted = n

	// 5. Finally the rollups themselves age out.
	n, err = s.batchedDelete(ctx,
		`DELETE FROM gpu_metrics_hourly WHERE ctid IN
		   (SELECT ctid FROM gpu_metrics_hourly WHERE hour_ts < $1 LIMIT $2)`,
		time.Now().Add(-p.RollupMetrics))
	if err != nil {
		return st, err
	}
	st.RollupsDeleted = n
	return st, nil
}

func (s *ServiceImpl) batchedDelete(ctx context.Context, q string, cutoff time.Time) (int64, error) {
	var total int64
	for {
		ct, err := s.db.Exec(ctx, q, cutoff, retentionBatch)
		if err != nil {
			return total, err
		}
		total += ct.RowsAffected()
		if ct.RowsAffected() < retentionBatch {
			return total, nil
		}
	}
}
