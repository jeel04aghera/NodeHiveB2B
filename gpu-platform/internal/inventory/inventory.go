// Package inventory is the Fleet/Inventory module.
// Owns tables: gpu_nodes, gpus, agent_heartbeats.
// Produces events: events.InventoryUpdated. Consumes: none (driven by the agent gateway).
package inventory

import (
	"context"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type Service interface {
	// UpsertInventory is called by the agent gateway on connect / hardware change.
	UpsertInventory(ctx context.Context, nodeID uuid.UUID, node domain.Node, gpus []domain.GPU) error
	RecordHeartbeat(ctx context.Context, hb domain.AgentHeartbeat) error
	SweepOffline(ctx context.Context) error // mark nodes offline past the liveness window

	ListNodes(ctx context.Context, orgID uuid.UUID) ([]domain.Node, error)
	GetNode(ctx context.Context, id uuid.UUID) (domain.Node, []domain.GPU, error)
	ListGPUs(ctx context.Context, orgID uuid.UUID, status domain.GPUStatus) ([]domain.GPU, error)
	DeregisterNode(ctx context.Context, id uuid.UUID) error
}

type Store interface {
	UpsertNode(ctx context.Context, n domain.Node) (uuid.UUID, error)
	UpsertGPUs(ctx context.Context, nodeID uuid.UUID, gpus []domain.GPU) error
	SetNodeStatus(ctx context.Context, id uuid.UUID, status domain.NodeStatus) error
	InsertHeartbeat(ctx context.Context, hb domain.AgentHeartbeat) error
	ListNodes(ctx context.Context, orgID uuid.UUID) ([]domain.Node, error)
	GetNode(ctx context.Context, id uuid.UUID) (domain.Node, error)
	ListGPUsByNode(ctx context.Context, nodeID uuid.UUID) ([]domain.GPU, error)
	ListGPUs(ctx context.Context, orgID uuid.UUID, status domain.GPUStatus) ([]domain.GPU, error)
	DeleteNode(ctx context.Context, id uuid.UUID) error
}
