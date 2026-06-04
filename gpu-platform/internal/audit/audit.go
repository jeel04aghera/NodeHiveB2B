// Package audit is the Audit module.
// Owns tables: audit_logs (append-only).
// Consumes events: subscribes to all domain events for a security trail; also
// called directly by modules for explicit actions (e.g. workload stopped by admin).
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type Service interface {
	Record(ctx context.Context, e domain.AuditLog) error
	Query(ctx context.Context, orgID uuid.UUID, from, to time.Time, limit int) ([]domain.AuditLog, error)
}

type Store interface {
	Insert(ctx context.Context, e domain.AuditLog) error
	Query(ctx context.Context, orgID uuid.UUID, from, to time.Time, limit int) ([]domain.AuditLog, error)
}
