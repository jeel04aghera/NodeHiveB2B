package workloads

import (
	"context"
	"errors"
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

// PromoteQueued starts as many queued workloads in an org as current capacity
// allows, oldest first. It is safe to call from anywhere, any number of times:
// a per-org advisory lock serializes concurrent promoters (terminal-status nudges
// racing the periodic sweep), and each promotion claims its GPUs under the same
// FOR UPDATE SKIP LOCKED placement transaction Launch uses. Returns the number of
// workloads promoted.
//
// Entries whose GPU type/count cannot currently be satisfied are skipped rather
// than blocking the queue head — a later entry with a satisfiable request may
// start first (documented FIFO-with-skip semantics).
func (s *ServiceImpl) PromoteQueued(ctx context.Context, orgID uuid.UUID) (int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, requested_gpu_type, requested_gpu_count, department_id, project_id
		   FROM workloads
		  WHERE org_id=$1 AND status='queued' ORDER BY COALESCE(queued_at, created_at)`, orgID)
	if err != nil {
		return 0, err
	}
	type cand struct {
		id             uuid.UUID
		gpuType        string
		count          int
		deptID, projID *uuid.UUID
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.gpuType, &c.count, &c.deptID, &c.projID); err != nil {
			rows.Close()
			return 0, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if len(cands) == 0 {
		return 0, nil
	}

	promoted := 0
	for _, c := range cands {
		// Re-check admission at promotion time: the org's balance/budget may have
		// been exhausted while the workload waited. It stays queued until credit
		// returns; admission is org-wide, so stop scanning on refusal.
		if s.billing != nil {
			if err := s.billing.AuthorizeLaunch(ctx, orgID, c.deptID, c.projID); err != nil {
				break
			}
		}
		ok, err := s.promoteOne(ctx, orgID, c.id, c.gpuType, c.count)
		if err != nil {
			return promoted, err
		}
		if ok {
			promoted++
		}
	}
	return promoted, nil
}

// promoteOne attempts to place and start a single queued workload. The whole
// promotion — advisory lock, CAS on status='queued', GPU claim, launch command —
// is one transaction, mirroring Launch.
func (s *ServiceImpl) promoteOne(ctx context.Context, orgID, id uuid.UUID, gpuType string, count int) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Per-org promotion lock: two promoters for the same org serialize here, so a
	// queued workload can't be claimed twice (the status CAS below is the backstop).
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('nodehive.queue.' || $1::text))`, orgID); err != nil {
		return false, err
	}

	placed, err := s.placeAndAttach(ctx, tx, orgID, id, gpuType, count)
	if errors.Is(err, ErrNoGPUsAvailable) {
		return false, nil // still no capacity for this entry
	}
	if err != nil {
		return false, err
	}

	ct, err := tx.Exec(ctx,
		`UPDATE workloads SET status='pending', stage='preparing', node_id=$2
		  WHERE id=$1 AND status='queued'`, id, placed.nodeID)
	if err != nil {
		return false, err
	}
	if ct.RowsAffected() == 0 {
		return false, nil // cancelled or already promoted under our feet
	}
	if err := placed.attach(ctx, tx); err != nil {
		return false, err
	}
	wl, err := s.getByIDTx(ctx, tx, id)
	if err != nil {
		return false, err
	}
	wl.NodeID = &placed.nodeID
	if err := s.enqueueLaunch(ctx, tx, wl, placed); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	_ = s.recordEvent(ctx, id, orgID, "node_selected", "promoted from queue")
	_ = s.recordEvent(ctx, id, orgID, "preparing", "")
	s.dispatch.Nudge(placed.nodeID)
	return true, nil
}

// PromoteAllQueued runs queue promotion for every org with waiting workloads.
// Called by the periodic queue sweep, which makes promotion crash-safe: a nudge
// lost to a control-plane restart is retried on the next tick.
func (s *ServiceImpl) PromoteAllQueued(ctx context.Context) (int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT org_id FROM workloads WHERE status='queued'`)
	if err != nil {
		return 0, err
	}
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		orgs = append(orgs, id)
	}
	rows.Close()

	total := 0
	for _, org := range orgs {
		n, err := s.PromoteQueued(ctx, org)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
