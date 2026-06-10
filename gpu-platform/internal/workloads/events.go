package workloads

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// WorkloadEvent is one lifecycle entry for the detail-page timeline (F1).
type WorkloadEvent struct {
	ID      int64     `json:"id"`
	Stage   string    `json:"stage"`
	Message string    `json:"message"`
	TS      time.Time `json:"ts"`
}

// Canonical deployment stages in order. Percent drives the progress tracker (F2);
// the frontend maps these to labels. Terminal stages sit at 100.
var stageOrder = []string{
	"queued", "scheduling", "node_selected", "preparing",
	"pulling_image", "building_env", "configuring", "starting_container",
	"ssh_enabled", "jupyter_enabled", "ready",
	"stopping", "stopped", "failed",
}

func isTerminalStage(stage string) bool {
	return stage == "stopped" || stage == "failed"
}

// recordEvent appends a lifecycle event and advances workloads.stage (unless the
// workload is already terminal). Best-effort: a logging failure never blocks a launch.
func (s *ServiceImpl) recordEvent(ctx context.Context, workloadID, orgID uuid.UUID, stage, message string) error {
	if _, err := s.db.Exec(ctx,
		`INSERT INTO workload_events (workload_id, org_id, stage, message) VALUES ($1,$2,$3,$4)`,
		workloadID, orgID, stage, message); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx,
		`UPDATE workloads SET stage=$2 WHERE id=$1 AND status NOT IN ('stopped','failed')`,
		workloadID, stage)
	return err
}

// RecordEvent is the exported entry used by the control plane.
func (s *ServiceImpl) RecordEvent(ctx context.Context, workloadID, orgID uuid.UUID, stage, message string) error {
	return s.recordEvent(ctx, workloadID, orgID, stage, message)
}

// RecordStageEvent is called from the agent gateway when an agent reports a
// granular launch stage (image pull / build / container start). It resolves the
// org from the workload so the gateway doesn't need to. Node-scoped: a stage
// report for a workload not assigned to the reporting node is rejected (C1).
func (s *ServiceImpl) RecordStageEvent(ctx context.Context, nodeID, workloadID uuid.UUID, stage string) error {
	var orgID uuid.UUID
	var assigned *uuid.UUID
	if err := s.db.QueryRow(ctx,
		`SELECT org_id, node_id FROM workloads WHERE id=$1`, workloadID).Scan(&orgID, &assigned); err != nil {
		return err
	}
	if assigned == nil || *assigned != nodeID {
		return ErrNotFound
	}
	return s.recordEvent(ctx, workloadID, orgID, stage, "")
}

// ListEvents returns the lifecycle timeline for a workload, oldest first. Scoped to
// orgID — a workload in another org returns ErrNotFound (no cross-tenant timeline read).
func (s *ServiceImpl) ListEvents(ctx context.Context, orgID, workloadID uuid.UUID) ([]WorkloadEvent, error) {
	if _, err := s.Get(ctx, orgID, workloadID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, stage, message, ts FROM workload_events WHERE workload_id=$1 ORDER BY ts, id`, workloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkloadEvent{}
	for rows.Next() {
		var e WorkloadEvent
		if err := rows.Scan(&e.ID, &e.Stage, &e.Message, &e.TS); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
