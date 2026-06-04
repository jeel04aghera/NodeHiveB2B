package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodehive/gpu-platform/internal/domain"
)

type ServiceImpl struct{ db *pgxpool.Pool }

func NewService(db *pgxpool.Pool) Service { return &ServiceImpl{db: db} }

func (s *ServiceImpl) Record(ctx context.Context, e domain.AuditLog) error {
	meta := e.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO audit_logs (org_id, actor_type, actor_id, action, target_type, target_id, metadata, ip)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8::inet)`,
		e.OrgID, e.ActorType, e.ActorID, e.Action, e.TargetType, e.TargetID, meta, nilIP(e.IP))
	return err
}

func (s *ServiceImpl) Query(ctx context.Context, orgID uuid.UUID, from, to time.Time, limit int) ([]domain.AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, actor_type, actor_id, action, target_type, target_id, metadata, coalesce(ip::text,''), ts
		   FROM audit_logs WHERE org_id=$1 AND ts BETWEEN $2 AND $3
		  ORDER BY ts DESC LIMIT $4`,
		orgID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.AuditLog, 0)
	for rows.Next() {
		var a domain.AuditLog
		if err := rows.Scan(&a.ID, &a.OrgID, &a.ActorType, &a.ActorID, &a.Action,
			&a.TargetType, &a.TargetID, &a.Metadata, &a.IP, &a.TS); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func nilIP(ip string) *string {
	if ip == "" {
		return nil
	}
	return &ip
}
