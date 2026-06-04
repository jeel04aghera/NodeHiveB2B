package ops

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type BudgetView struct {
	ID         string  `json:"id"`
	ScopeType  string  `json:"scope_type"` // organization | department | project
	ScopeID    *string `json:"scope_id,omitempty"`
	ScopeName  string  `json:"scope_name"`
	Amount     float64 `json:"amount"`      // INR
	Spend      float64 `json:"spend"`       // INR, month-to-date
	Forecast   float64 `json:"forecast"`    // INR, projected full month
	Remaining  float64 `json:"remaining"`   // INR
	BurnPerDay float64 `json:"burn_per_day"` // INR/day
	UsedPct    float64 `json:"used_pct"`
	Status     string  `json:"status"` // safe | at_risk | exceeded
}

func budgetStatus(spend, forecast, amount float64) string {
	if amount <= 0 {
		return "safe"
	}
	if spend >= amount {
		return "exceeded"
	}
	if forecast > amount || spend/amount >= 0.8 {
		return "at_risk"
	}
	return "safe"
}

func (s *Service) scopeName(ctx context.Context, scopeType string, scopeID *uuid.UUID) string {
	if scopeID == nil {
		return "Organization"
	}
	var name string
	switch scopeType {
	case "department":
		_ = s.db.QueryRow(ctx, `SELECT name FROM departments WHERE id=$1`, scopeID).Scan(&name)
	case "project":
		_ = s.db.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1`, scopeID).Scan(&name)
	}
	if name == "" {
		name = "(unknown)"
	}
	return name
}

func (s *Service) ListBudgets(ctx context.Context, orgID uuid.UUID) ([]BudgetView, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, scope_type, scope_id, amount FROM budgets WHERE org_id=$1 ORDER BY scope_type`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	_, _, day, daysInMonth := monthBounds(time.Now())
	out := []BudgetView{}
	for rows.Next() {
		var b BudgetView
		var scopeID *uuid.UUID
		if err := rows.Scan(&b.ID, &b.ScopeType, &scopeID, &b.Amount); err != nil {
			return nil, err
		}
		b.Spend = s.scopeSpendINR(ctx, orgID, b.ScopeType, scopeID)
		if day > 0 {
			b.BurnPerDay = b.Spend / float64(day)
			b.Forecast = b.BurnPerDay * float64(daysInMonth)
		}
		b.Remaining = b.Amount - b.Spend
		if b.Amount > 0 {
			b.UsedPct = b.Spend / b.Amount * 100
		}
		b.Status = budgetStatus(b.Spend, b.Forecast, b.Amount)
		b.ScopeName = s.scopeName(ctx, b.ScopeType, scopeID)
		if scopeID != nil {
			id := scopeID.String()
			b.ScopeID = &id
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type SetBudgetReq struct {
	ScopeType string     `json:"scope_type"`
	ScopeID   *uuid.UUID `json:"scope_id,omitempty"`
	Amount    float64    `json:"amount"`
}

// SetBudget upserts a budget for a scope (one per org+scope_type+scope_id).
func (s *Service) SetBudget(ctx context.Context, orgID uuid.UUID, req SetBudgetReq) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO budgets (org_id, scope_type, scope_id, amount)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (org_id, scope_type, coalesce(scope_id, '00000000-0000-0000-0000-000000000000'::uuid))
		 DO UPDATE SET amount=EXCLUDED.amount, updated_at=now()`,
		orgID, req.ScopeType, req.ScopeID, req.Amount)
	return err
}

func (s *Service) DeleteBudget(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM budgets WHERE id=$1 AND org_id=$2`, id, orgID)
	return err
}
