package workloads

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// avgWaitFallback is used when there's no running workload to estimate against.
const avgWaitFallback = 15 * time.Minute

// QueueEntry is one waiting workload (F3).
type QueueEntry struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Position      int       `json:"position"`
	GPUType       string    `json:"gpu_type"`
	GPUCount      int       `json:"gpu_count"`
	QueuedAt      time.Time `json:"queued_at"`
	EstWaitMin    int       `json:"est_wait_min"`
	EstStart      time.Time `json:"est_start"`
	OwnerEmail    string    `json:"owner_email,omitempty"`
	ProjectName   string    `json:"project_name,omitempty"`
}

// QueueStats powers the admin queue dashboard.
type QueueStats struct {
	Waiting       int        `json:"waiting"`
	AvgWaitMin    int        `json:"avg_wait_min"`
	NextFreeAt    *time.Time `json:"next_free_at,omitempty"`
	Entries       []QueueEntry `json:"entries"`
}

// estWaitPerPosition estimates how long one queue slot takes to clear, from the
// average runtime of currently-running workloads in the org (real data), else a
// fallback. Coarse but honest — it's an estimate, labelled as such in the UI.
func (s *ServiceImpl) estWaitPerPosition(ctx context.Context, orgID uuid.UUID) time.Duration {
	var avgSec *float64
	_ = s.db.QueryRow(ctx,
		`SELECT avg(extract(epoch from (now()-started_at))) FROM workloads
		  WHERE org_id=$1 AND status='running' AND started_at IS NOT NULL`, orgID).Scan(&avgSec)
	if avgSec == nil || *avgSec <= 0 {
		return avgWaitFallback
	}
	return time.Duration(*avgSec) * time.Second
}

// ListQueue returns the org's waiting workloads with positions + ETAs (F3).
func (s *ServiceImpl) ListQueue(ctx context.Context, orgID uuid.UUID) (QueueStats, error) {
	rows, err := s.db.Query(ctx,
		`SELECT w.id, w.name, w.requested_gpu_type, w.requested_gpu_count,
		        COALESCE(w.queued_at, w.created_at),
		        COALESCE(u.email,''), COALESCE(p.name,'')
		   FROM workloads w
		   LEFT JOIN users u ON u.id=w.user_id
		   LEFT JOIN projects p ON p.id=w.project_id
		  WHERE w.org_id=$1 AND w.status='queued'
		  ORDER BY COALESCE(w.queued_at, w.created_at)`, orgID)
	if err != nil {
		return QueueStats{}, err
	}
	defer rows.Close()

	per := s.estWaitPerPosition(ctx, orgID)
	now := time.Now()
	out := QueueStats{Entries: []QueueEntry{}}
	pos := 0
	for rows.Next() {
		var e QueueEntry
		if err := rows.Scan(&e.ID, &e.Name, &e.GPUType, &e.GPUCount, &e.QueuedAt, &e.OwnerEmail, &e.ProjectName); err != nil {
			return QueueStats{}, err
		}
		pos++
		e.Position = pos
		wait := time.Duration(pos) * per
		e.EstWaitMin = int(wait.Minutes())
		e.EstStart = now.Add(wait)
		out.Entries = append(out.Entries, e)
	}
	out.Waiting = pos
	out.AvgWaitMin = int(per.Minutes())
	if pos > 0 {
		t := now.Add(per)
		out.NextFreeAt = &t
	}
	return out, rows.Err()
}

// CancelQueued removes a waiting workload from the queue.
func (s *ServiceImpl) CancelQueued(ctx context.Context, id uuid.UUID) error {
	ct, err := s.db.Exec(ctx,
		`UPDATE workloads SET status='stopped', stage='stopped', stopped_at=now(), stop_reason='user'
		   WHERE id=$1 AND status='queued'`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotQueued
	}
	var orgID uuid.UUID
	_ = s.db.QueryRow(ctx, `SELECT org_id FROM workloads WHERE id=$1`, id).Scan(&orgID)
	_ = s.recordEvent(ctx, id, orgID, "stopped", "cancelled from queue")
	return nil
}

// tryPromoteQueue attempts to start the oldest queued workload in an org now that
// capacity may have freed. Called after a workload stops/fails. Best-effort.
func (s *ServiceImpl) tryPromoteQueue(ctx context.Context, orgID uuid.UUID) {
	var (
		id      uuid.UUID
		gpuType string
		count   int
		deptID  *uuid.UUID
		projID  *uuid.UUID
	)
	err := s.db.QueryRow(ctx,
		`SELECT id, requested_gpu_type, requested_gpu_count, department_id, project_id
		   FROM workloads
		  WHERE org_id=$1 AND status='queued' ORDER BY COALESCE(queued_at, created_at) LIMIT 1`,
		orgID).Scan(&id, &gpuType, &count, &deptID, &projID)
	if err != nil {
		return // nothing queued
	}
	// Re-check admission at promotion time: the org's balance/budget may have been
	// exhausted while the workload waited. It stays queued until credit returns.
	if s.billing != nil {
		if err := s.billing.AuthorizeLaunch(ctx, orgID, deptID, projID); err != nil {
			return
		}
	}
	gpuIDs, nodeID, err := s.place(ctx, orgID, gpuType, count)
	if err != nil {
		return // still no capacity
	}

	// Attach GPUs and flip to pending, then dispatch — mirrors Launch's tail.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE workloads SET status='pending', stage='preparing', node_id=$2 WHERE id=$1 AND status='queued'`,
		id, nodeID); err != nil {
		return
	}
	for _, gid := range gpuIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO workload_gpus (workload_id, gpu_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, id, gid); err != nil {
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE gpus SET status='in_use' WHERE id=$1`, gid); err != nil {
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return
	}
	_ = s.recordEvent(ctx, id, orgID, "node_selected", "promoted from queue")

	wl, err := s.getByID(ctx, id) // system path: this workload's org was just resolved above
	if err != nil {
		return
	}
	go s.sendLaunchCommand(context.Background(), nodeID, wl, gpuIDs)
}
