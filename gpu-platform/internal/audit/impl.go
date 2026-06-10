package audit

import (
	"context"
	"fmt"
	"strings"
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

func (s *ServiceImpl) Query(ctx context.Context, orgID uuid.UUID, f QueryFilter) ([]domain.AuditLog, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	if f.To.IsZero() {
		f.To = time.Now()
	}
	if f.From.IsZero() {
		// Searchable history default: the last 30 days (was a fixed 24h window).
		f.From = f.To.Add(-30 * 24 * time.Hour)
	}

	where := []string{"org_id=$1", "ts BETWEEN $2 AND $3"}
	args := []any{orgID, f.From, f.To}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Action != "" {
		add("action LIKE $%d", likePrefix(f.Action))
	}
	if f.ActorID != "" {
		add("actor_id = $%d", f.ActorID)
	}
	if f.TargetType != "" {
		add("target_type = $%d", f.TargetType)
	}
	if f.TargetID != "" {
		add("target_id = $%d", f.TargetID)
	}
	if f.Q != "" {
		add("(action ILIKE $%[1]d OR actor_id ILIKE $%[1]d OR target_id ILIKE $%[1]d OR target_type ILIKE $%[1]d OR metadata::text ILIKE $%[1]d)",
			"%"+escapeLike(f.Q)+"%")
	}
	cond := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRow(ctx,
		"SELECT count(*) FROM audit_logs WHERE "+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(ctx, fmt.Sprintf(
		`SELECT id, org_id, actor_type, actor_id, action, target_type, target_id, metadata, coalesce(ip::text,''), ts
		   FROM audit_logs WHERE %s
		  ORDER BY ts DESC, id DESC LIMIT $%d OFFSET $%d`, cond, len(args)-1, len(args)),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]domain.AuditLog, 0)
	for rows.Next() {
		var a domain.AuditLog
		if err := rows.Scan(&a.ID, &a.OrgID, &a.ActorType, &a.ActorID, &a.Action,
			&a.TargetType, &a.TargetID, &a.Metadata, &a.IP, &a.TS); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (s *ServiceImpl) VerifyChain(ctx context.Context) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT bad_id FROM audit_verify_chain()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bad []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		bad = append(bad, id)
	}
	return bad, rows.Err()
}

// likePrefix builds the prefix pattern for the Action filter.
func likePrefix(s string) string { return escapeLike(s) + "%" }

// escapeLike neutralizes LIKE metacharacters in user-supplied search terms.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func nilIP(ip string) *string {
	if ip == "" {
		return nil
	}
	return &ip
}
