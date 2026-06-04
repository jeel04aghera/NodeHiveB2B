package inventory

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

func (s *ServiceImpl) UpsertInventory(ctx context.Context, nodeID uuid.UUID, node domain.Node, gpus []domain.GPU) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE gpu_nodes SET
			hostname=$2, os=$3, kernel=$4, cpu_model=$5, cpu_cores=$6,
			ram_mb=$7, nvidia_driver=$8, cuda_version=$9, agent_version=$10,
			labels=$11, last_seen_at=now(), status='online'
		 WHERE id=$1`,
		nodeID, node.Hostname, node.OS, node.Kernel, node.CPUModel,
		node.CPUCores, node.RAMMB, node.NvidiaDriver, node.CUDAVersion,
		node.AgentVersion, labelsJSON(node.Labels)); err != nil {
		return fmt.Errorf("update node: %w", err)
	}

	for _, g := range gpus {
		if _, err := tx.Exec(ctx,
			`INSERT INTO gpus (node_id, org_id, gpu_index, uuid, model, memory_mb, mig_enabled, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, 'idle')
			 ON CONFLICT (uuid) DO UPDATE SET
			   node_id=$1, org_id=$2, model=$5, memory_mb=$6, mig_enabled=$7, updated_at=now()`,
			nodeID, node.OrgID, g.Index, g.UUID, g.Model, g.MemoryMB, g.MIGEnabled); err != nil {
			return fmt.Errorf("upsert gpu %s: %w", g.UUID, err)
		}
	}
	return tx.Commit(ctx)
}

func (s *ServiceImpl) RecordHeartbeat(ctx context.Context, hb domain.AgentHeartbeat) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO agent_heartbeats (org_id, node_id, ts, status, agent_version, summary)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		hb.OrgID, hb.NodeID, hb.TS, hb.Status, hb.AgentVer, hb.Summary)
	return err
}

func (s *ServiceImpl) SweepOffline(ctx context.Context) error {
	threshold := time.Now().Add(-90 * time.Second)
	_, err := s.db.Exec(ctx,
		`UPDATE gpu_nodes SET status='offline'
		 WHERE status='online' AND (last_seen_at IS NULL OR last_seen_at < $1)`,
		threshold)
	return err
}

func (s *ServiceImpl) ListNodes(ctx context.Context, orgID uuid.UUID) ([]domain.Node, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, hostname, status, os, kernel, cpu_model, cpu_cores, ram_mb,
		        nvidia_driver, cuda_version, agent_version, enrolled_at, last_seen_at
		   FROM gpu_nodes WHERE org_id=$1 ORDER BY hostname`,
		orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Node
	for rows.Next() {
		var n domain.Node
		if err := rows.Scan(&n.ID, &n.OrgID, &n.Hostname, &n.Status, &n.OS, &n.Kernel,
			&n.CPUModel, &n.CPUCores, &n.RAMMB, &n.NvidiaDriver, &n.CUDAVersion,
			&n.AgentVersion, &n.EnrolledAt, &n.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *ServiceImpl) GetNode(ctx context.Context, id uuid.UUID) (domain.Node, []domain.GPU, error) {
	var n domain.Node
	err := s.db.QueryRow(ctx,
		`SELECT id, org_id, hostname, status, os, kernel, cpu_model, cpu_cores, ram_mb,
		        nvidia_driver, cuda_version, agent_version, enrolled_at, last_seen_at
		   FROM gpu_nodes WHERE id=$1`, id).
		Scan(&n.ID, &n.OrgID, &n.Hostname, &n.Status, &n.OS, &n.Kernel,
			&n.CPUModel, &n.CPUCores, &n.RAMMB, &n.NvidiaDriver, &n.CUDAVersion,
			&n.AgentVersion, &n.EnrolledAt, &n.LastSeenAt)
	if err != nil {
		return domain.Node{}, nil, err
	}
	gpus, err := s.listGPUsByNode(ctx, id)
	return n, gpus, err
}

func (s *ServiceImpl) ListGPUs(ctx context.Context, orgID uuid.UUID, status domain.GPUStatus) ([]domain.GPU, error) {
	q := `SELECT id, node_id, org_id, gpu_index, uuid, model, memory_mb, mig_enabled, status, created_at, updated_at
	        FROM gpus WHERE org_id=$1`
	args := []any{orgID}
	if status != "" {
		q += " AND status=$2"
		args = append(args, status)
	}
	q += " ORDER BY model, gpu_index"
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGPUs(rows)
}

func (s *ServiceImpl) DeregisterNode(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM gpu_nodes WHERE id=$1`, id)
	return err
}

func (s *ServiceImpl) listGPUsByNode(ctx context.Context, nodeID uuid.UUID) ([]domain.GPU, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, node_id, org_id, gpu_index, uuid, model, memory_mb, mig_enabled, status, created_at, updated_at
		   FROM gpus WHERE node_id=$1 ORDER BY gpu_index`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGPUs(rows)
}

func scanGPUs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]domain.GPU, error) {
	var out []domain.GPU
	for rows.Next() {
		var g domain.GPU
		if err := rows.Scan(&g.ID, &g.NodeID, &g.OrgID, &g.Index, &g.UUID,
			&g.Model, &g.MemoryMB, &g.MIGEnabled, &g.Status, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func labelsJSON(labels map[string]string) []byte {
	if len(labels) == 0 {
		return []byte("{}")
	}
	b := []byte("{")
	first := true
	for k, v := range labels {
		if !first {
			b = append(b, ',')
		}
		b = append(b, fmt.Sprintf("%q:%q", k, v)...)
		first = false
	}
	b = append(b, '}')
	return b
}
