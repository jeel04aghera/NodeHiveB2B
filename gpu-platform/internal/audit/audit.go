// Package audit is the Audit module.
// Owns tables: audit_logs (append-only, hash-chained — see migration 0016).
// Consumes events: subscribes to all domain events for a security trail; also
// called directly by modules for explicit actions (e.g. workload stopped by admin).
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

// QueryFilter narrows an org's audit trail. Zero values are ignored. Action is a
// prefix match ("workload" finds workload.launch, workload.stop, …); Q is a free-text
// match across action, actor, target and metadata.
type QueryFilter struct {
	From, To   time.Time
	Action     string
	ActorID    string
	TargetType string
	TargetID   string
	Q          string
	Limit      int // default 100, max 500
	Offset     int
}

type Service interface {
	Record(ctx context.Context, e domain.AuditLog) error
	// Query returns the matching page (newest first) and the total match count.
	Query(ctx context.Context, orgID uuid.UUID, f QueryFilter) ([]domain.AuditLog, int, error)
	// VerifyChain re-walks the hash chain; a non-empty result means the audit log
	// was tampered with (returned ids are the first broken rows).
	VerifyChain(ctx context.Context) ([]int64, error)
}
