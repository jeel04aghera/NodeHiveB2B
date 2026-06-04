package ops

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// F5 — cost alerts. A small rules engine: admins define AlertRules; a background
// evaluator (EvaluateAll, called on a ticker from the control plane) checks each
// enabled rule against real spend/runtime data and raises an Alert when a
// threshold is crossed. Active alerts are deduplicated by dedup_key so the
// evaluator is idempotent — re-running it never spams duplicates.

type AlertRule struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"` // project_spend|department_spend|workload_runtime|idle_workload|budget_utilization
	Threshold float64 `json:"threshold"`
	ScopeID   *string `json:"scope_id,omitempty"`
	ScopeName string  `json:"scope_name,omitempty"`
	Severity  string  `json:"severity"`
	Enabled   bool    `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type Alert struct {
	ID        string    `json:"id"`
	RuleID    *string   `json:"rule_id,omitempty"`
	Severity  string    `json:"severity"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ruleLabels gives the UI a human description per rule type and the unit of its
// threshold. Kept here so backend and frontend agree on semantics.
var ruleUnit = map[string]string{
	"project_spend":      "₹ this month",
	"department_spend":   "₹ this month",
	"workload_runtime":   "hours running",
	"idle_workload":      "hours idle",
	"budget_utilization": "% of budget",
}

func validRuleType(t string) bool { _, ok := ruleUnit[t]; return ok }

// ───────────────────────── rule CRUD ─────────────────────────

type CreateRuleReq struct {
	Type      string     `json:"type"`
	Threshold float64    `json:"threshold"`
	ScopeID   *uuid.UUID `json:"scope_id,omitempty"`
	Severity  string     `json:"severity"`
}

func (s *Service) CreateRule(ctx context.Context, orgID uuid.UUID, req CreateRuleReq) (string, error) {
	if !validRuleType(req.Type) {
		return "", fmt.Errorf("unknown alert type %q", req.Type)
	}
	if req.Threshold <= 0 {
		return "", fmt.Errorf("threshold must be positive")
	}
	sev := req.Severity
	if sev != "info" && sev != "warning" && sev != "critical" {
		sev = "warning"
	}
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO alert_rules (org_id, type, threshold, scope_id, severity)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		orgID, req.Type, req.Threshold, req.ScopeID, sev).Scan(&id)
	return id, err
}

func (s *Service) ListRules(ctx context.Context, orgID uuid.UUID) ([]AlertRule, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, type, threshold, scope_id, severity, enabled, created_at
		   FROM alert_rules WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		var r AlertRule
		var scopeID *uuid.UUID
		if err := rows.Scan(&r.ID, &r.Type, &r.Threshold, &scopeID, &r.Severity, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		if scopeID != nil {
			id := scopeID.String()
			r.ScopeID = &id
			r.ScopeName = s.alertScopeName(ctx, r.Type, scopeID)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) SetRuleEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool) error {
	_, err := s.db.Exec(ctx, `UPDATE alert_rules SET enabled=$3 WHERE id=$1 AND org_id=$2`, id, orgID, enabled)
	return err
}

func (s *Service) DeleteRule(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM alert_rules WHERE id=$1 AND org_id=$2`, id, orgID)
	return err
}

// alertScopeName resolves a project/department name for display.
func (s *Service) alertScopeName(ctx context.Context, ruleType string, scopeID *uuid.UUID) string {
	switch ruleType {
	case "project_spend":
		return s.scopeName(ctx, "project", scopeID)
	case "department_spend":
		return s.scopeName(ctx, "department", scopeID)
	}
	return ""
}

// ───────────────────────── alerts (raised) ─────────────────────────

func (s *Service) ListAlerts(ctx context.Context, orgID uuid.UUID, includeAcked bool) ([]Alert, error) {
	q := `SELECT id, rule_id, severity, title, message, status, created_at
	        FROM alerts WHERE org_id=$1`
	if !includeAcked {
		q += ` AND status='active'`
	}
	q += ` ORDER BY created_at DESC LIMIT 100`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		var ruleID *uuid.UUID
		if err := rows.Scan(&a.ID, &ruleID, &a.Severity, &a.Title, &a.Message, &a.Status, &a.CreatedAt); err != nil {
			return nil, err
		}
		if ruleID != nil {
			id := ruleID.String()
			a.RuleID = &id
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) AcknowledgeAlert(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE alerts SET status='acknowledged' WHERE id=$1 AND org_id=$2`, id, orgID)
	return err
}

// raiseAlert inserts an active alert unless one with the same dedup_key is already
// active (enforced by a partial unique index). ON CONFLICT DO NOTHING makes the
// evaluator idempotent.
func (s *Service) raiseAlert(ctx context.Context, orgID uuid.UUID, ruleID *uuid.UUID, severity, title, message, dedupKey string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO alerts (org_id, rule_id, severity, title, message, dedup_key)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (org_id, dedup_key) WHERE status='active' AND dedup_key IS NOT NULL DO NOTHING`,
		orgID, ruleID, severity, title, message, dedupKey)
	return err
}

// ───────────────────────── evaluation ─────────────────────────

// EvaluateAll checks every enabled rule across all orgs and raises alerts. Called
// from a background ticker. Best-effort: an error on one rule never aborts the rest.
func (s *Service) EvaluateAll(ctx context.Context) (int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, org_id, type, threshold, scope_id, severity FROM alert_rules WHERE enabled=true`)
	if err != nil {
		return 0, err
	}
	type rule struct {
		id, orgID uuid.UUID
		typ       string
		threshold float64
		scopeID   *uuid.UUID
		severity  string
	}
	var rules []rule
	for rows.Next() {
		var r rule
		if err := rows.Scan(&r.id, &r.orgID, &r.typ, &r.threshold, &r.scopeID, &r.severity); err != nil {
			rows.Close()
			return 0, err
		}
		rules = append(rules, r)
	}
	rows.Close()

	month := time.Now().Format("2006-01")
	raised := 0
	for _, r := range rules {
		rid := r.id
		switch r.typ {
		case "project_spend":
			if r.scopeID == nil {
				continue
			}
			spend := s.scopeSpendINR(ctx, r.orgID, "project", r.scopeID)
			if spend >= r.threshold {
				name := s.scopeName(ctx, "project", r.scopeID)
				if s.raiseAlert(ctx, r.orgID, &rid, r.severity,
					fmt.Sprintf("Project %q over spend threshold", name),
					fmt.Sprintf("Month-to-date spend ₹%.0f exceeds the ₹%.0f threshold.", spend, r.threshold),
					fmt.Sprintf("project_spend:%s:%s", r.scopeID, month)) == nil {
					raised++
				}
			}
		case "department_spend":
			if r.scopeID == nil {
				continue
			}
			spend := s.scopeSpendINR(ctx, r.orgID, "department", r.scopeID)
			if spend >= r.threshold {
				name := s.scopeName(ctx, "department", r.scopeID)
				if s.raiseAlert(ctx, r.orgID, &rid, r.severity,
					fmt.Sprintf("Department %q over spend threshold", name),
					fmt.Sprintf("Month-to-date spend ₹%.0f exceeds the ₹%.0f threshold.", spend, r.threshold),
					fmt.Sprintf("department_spend:%s:%s", r.scopeID, month)) == nil {
					raised++
				}
			}
		case "budget_utilization":
			raised += s.evalBudgetUtilization(ctx, r.orgID, &rid, r.severity, r.threshold)
		case "workload_runtime":
			raised += s.evalWorkloadRuntime(ctx, r.orgID, &rid, r.severity, r.threshold)
		case "idle_workload":
			raised += s.evalIdleWorkload(ctx, r.orgID, &rid, r.severity, r.threshold)
		}
	}
	return raised, nil
}

func (s *Service) evalBudgetUtilization(ctx context.Context, orgID uuid.UUID, ruleID *uuid.UUID, severity string, thresholdPct float64) int {
	budgets, err := s.ListBudgets(ctx, orgID)
	if err != nil {
		return 0
	}
	month := time.Now().Format("2006-01")
	n := 0
	for _, b := range budgets {
		if b.Amount <= 0 || b.UsedPct < thresholdPct {
			continue
		}
		if s.raiseAlert(ctx, orgID, ruleID, severity,
			fmt.Sprintf("%s budget %s used", b.ScopeName, pctLabel(b.UsedPct)),
			fmt.Sprintf("Spend ₹%.0f of ₹%.0f budget (forecast ₹%.0f).", b.Spend, b.Amount, b.Forecast),
			fmt.Sprintf("budget_utilization:%s:%s:%s", b.ScopeType, deref(b.ScopeID), month)) == nil {
			n++
		}
	}
	return n
}

func (s *Service) evalWorkloadRuntime(ctx context.Context, orgID uuid.UUID, ruleID *uuid.UUID, severity string, thresholdHours float64) int {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, extract(epoch from (now()-started_at))/3600.0
		   FROM workloads
		  WHERE org_id=$1 AND status='running' AND started_at IS NOT NULL
		    AND extract(epoch from (now()-started_at))/3600.0 >= $2`, orgID, thresholdHours)
	if err != nil {
		return 0
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, name string
		var hours float64
		if err := rows.Scan(&id, &name, &hours); err != nil {
			return n
		}
		if s.raiseAlert(ctx, orgID, ruleID, severity,
			fmt.Sprintf("Workload %q running %.1fh", name, hours),
			fmt.Sprintf("Has been running %.1f hours, over the %.0fh threshold. Check it is still needed.", hours, thresholdHours),
			fmt.Sprintf("workload_runtime:%s", id)) == nil {
			n++
		}
	}
	return n
}

// evalIdleWorkload flags running workloads whose attached GPUs have averaged near-zero
// utilization over the last hour for longer than the threshold (in hours) — wasted spend.
func (s *Service) evalIdleWorkload(ctx context.Context, orgID uuid.UUID, ruleID *uuid.UUID, severity string, thresholdHours float64) int {
	rows, err := s.db.Query(ctx,
		`SELECT w.id, w.name, extract(epoch from (now()-w.started_at))/3600.0 AS hours,
		        COALESCE(avg(m.util_pct),0) AS util
		   FROM workloads w
		   JOIN workload_gpus wg ON wg.workload_id=w.id AND wg.detached_at IS NULL
		   JOIN gpus g ON g.id=wg.gpu_id
		   LEFT JOIN gpu_metrics m ON m.gpu_id=g.id AND m.ts > now() - interval '1 hour'
		  WHERE w.org_id=$1 AND w.status='running' AND w.started_at IS NOT NULL
		  GROUP BY w.id, w.name, w.started_at
		 HAVING extract(epoch from (now()-w.started_at))/3600.0 >= $2
		    AND COALESCE(avg(m.util_pct),0) < 5`, orgID, thresholdHours)
	if err != nil {
		return 0
	}
	defer rows.Close()
	month := time.Now().Format("2006-01-02")
	n := 0
	for rows.Next() {
		var id, name string
		var hours, util float64
		if err := rows.Scan(&id, &name, &hours, &util); err != nil {
			return n
		}
		if s.raiseAlert(ctx, orgID, ruleID, severity,
			fmt.Sprintf("Workload %q idle %.1fh", name, hours),
			fmt.Sprintf("Average GPU utilization %.1f%% over the last hour while running %.1fh — likely idle, consider stopping.", util, hours),
			fmt.Sprintf("idle_workload:%s:%s", id, month)) == nil {
			n++
		}
	}
	return n
}

func deref(s *string) string {
	if s == nil {
		return "org"
	}
	return *s
}

// pctLabel renders a percentage with just enough precision to be meaningful for
// small values (so a 0.6% reading never collapses to a misleading "0%").
func pctLabel(p float64) string {
	if p > 0 && p < 1 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}
