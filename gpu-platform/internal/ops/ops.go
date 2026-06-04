// Package ops implements the enterprise operations features that sit on top of the
// core platform: capacity reservations (F4), cost alerts (F5), and budgets (F6).
// All money is stored/compared in INR (cost_records are USD; we convert at usdToINR
// to match the product's display currency).
package ops

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const usdToINR = 83.0

type Service struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Service { return &Service{db: db} }

// ───────────────────────── helpers ─────────────────────────

func monthBounds(now time.Time) (time.Time, time.Time, int, int) {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	next := first.AddDate(0, 1, 0)
	daysInMonth := int(next.Sub(first).Hours() / 24)
	dayOfMonth := now.Day()
	return first, next, dayOfMonth, daysInMonth
}

// scopeSpendINR returns this-month spend (INR) for an org/department/project scope.
func (s *Service) scopeSpendINR(ctx context.Context, orgID uuid.UUID, scopeType string, scopeID *uuid.UUID) float64 {
	first, _, _, _ := monthBounds(time.Now())
	var usd *float64
	switch scopeType {
	case "project":
		_ = s.db.QueryRow(ctx,
			`SELECT sum(amount) FROM cost_records WHERE org_id=$1 AND project_id=$2 AND period_start >= $3`,
			orgID, scopeID, first).Scan(&usd)
	case "department":
		_ = s.db.QueryRow(ctx,
			`SELECT sum(c.amount) FROM cost_records c JOIN workloads w ON w.id=c.workload_id
			  WHERE c.org_id=$1 AND w.department_id=$2 AND c.period_start >= $3`,
			orgID, scopeID, first).Scan(&usd)
	default: // organization
		_ = s.db.QueryRow(ctx,
			`SELECT sum(amount) FROM cost_records WHERE org_id=$1 AND period_start >= $2`,
			orgID, first).Scan(&usd)
	}
	if usd == nil {
		return 0
	}
	return *usd * usdToINR
}
