package nodes

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nodehive/gpu-platform/internal/domain"
)

// NodeDetail is the full node view for the detail page.
type NodeDetail struct {
	ID               uuid.UUID         `json:"id"`
	Hostname         string            `json:"hostname"`
	Status           string            `json:"status"`
	Health           string            `json:"health"` // healthy | stale | offline
	OS               string            `json:"os"`
	Kernel           string            `json:"kernel"`
	CPUModel         string            `json:"cpu_model"`
	CPUCores         int               `json:"cpu_cores"`
	RAMMB            int64             `json:"ram_mb"`
	NvidiaDriver     string            `json:"nvidia_driver"`
	CUDAVersion      string            `json:"cuda_version"`
	AgentVersion     string            `json:"agent_version"`
	EnrolledAt       time.Time         `json:"enrolled_at"`
	LastSeenAt       *time.Time        `json:"last_seen_at"`
	Synthetic        bool              `json:"synthetic"`
	GPUs             []domain.GPU      `json:"gpus"`
	RunningWorkloads []NodeWorkloadRef `json:"running_workloads"`
}

type NodeWorkloadRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	UserEmail string    `json:"user_email"`
	GPUCount  int       `json:"gpu_count"`
}

func (s *Service) NodeDetail(ctx context.Context, orgID, id uuid.UUID) (NodeDetail, error) {
	var d NodeDetail
	err := s.repo.pool.QueryRow(ctx, `
		SELECT id, hostname, status, os, kernel, cpu_model, cpu_cores, ram_mb,
		       nvidia_driver, cuda_version, agent_version, enrolled_at, last_seen_at
		  FROM gpu_nodes WHERE id=$1 AND org_id=$2`, id, orgID).
		Scan(&d.ID, &d.Hostname, &d.Status, &d.OS, &d.Kernel, &d.CPUModel, &d.CPUCores,
			&d.RAMMB, &d.NvidiaDriver, &d.CUDAVersion, &d.AgentVersion, &d.EnrolledAt, &d.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeDetail{}, ErrNotFound
	}
	if err != nil {
		return NodeDetail{}, err
	}

	// Health: online + seen within 60s = healthy; online but stale = stale; else offline.
	d.Health = "offline"
	if d.Status == "online" {
		d.Health = "healthy"
		if d.LastSeenAt != nil && time.Since(*d.LastSeenAt) > 60*time.Second {
			d.Health = "stale"
		}
	}

	// GPUs on this node.
	grows, err := s.repo.pool.Query(ctx, `
		SELECT id, node_id, org_id, gpu_index, uuid, model, memory_mb, mig_enabled, status, created_at, updated_at
		  FROM gpus WHERE node_id=$1 ORDER BY gpu_index`, id)
	if err != nil {
		return NodeDetail{}, err
	}
	defer grows.Close()
	d.GPUs = []domain.GPU{}
	for grows.Next() {
		var g domain.GPU
		if err := grows.Scan(&g.ID, &g.NodeID, &g.OrgID, &g.Index, &g.UUID, &g.Model,
			&g.MemoryMB, &g.MIGEnabled, &g.Status, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return NodeDetail{}, err
		}
		if strings.Contains(strings.ToLower(g.Model), "synthetic") {
			d.Synthetic = true
		}
		d.GPUs = append(d.GPUs, g)
	}

	// Active workloads placed on this node.
	wrows, err := s.repo.pool.Query(ctx, `
		SELECT w.id, w.name, w.status, coalesce(u.email,''), w.requested_gpu_count
		  FROM workloads w LEFT JOIN users u ON u.id = w.user_id
		 WHERE w.node_id=$1 AND w.status IN ('pending','running','stopping')
		 ORDER BY w.created_at DESC`, id)
	if err != nil {
		return NodeDetail{}, err
	}
	defer wrows.Close()
	d.RunningWorkloads = []NodeWorkloadRef{}
	for wrows.Next() {
		var ref NodeWorkloadRef
		if err := wrows.Scan(&ref.ID, &ref.Name, &ref.Status, &ref.UserEmail, &ref.GPUCount); err != nil {
			return NodeDetail{}, err
		}
		d.RunningWorkloads = append(d.RunningWorkloads, ref)
	}
	return d, nil
}
