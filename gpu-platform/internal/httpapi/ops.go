package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/ops"
)

// Handlers for the enterprise operations features: reservations (F4),
// cost alerts (F5), and budgets (F6). All mounted under /api/v1.

// ── F4: Reservations ──────────────────────────────────────────────────────────

func (a *API) listReservations(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	out, err := a.ops.ListReservations(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list reservations")
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) createReservation(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		GPUModel  string  `json:"gpu_model"`
		GPUCount  int     `json:"gpu_count"`
		StartAt   string  `json:"start_at"`
		EndAt     string  `json:"end_at"`
		ProjectID *string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	start, err1 := time.Parse(time.RFC3339, body.StartAt)
	end, err2 := time.Parse(time.RFC3339, body.EndAt)
	if err1 != nil || err2 != nil {
		writeErr(w, 400, "validation", "start_at and end_at must be RFC3339 timestamps")
		return
	}
	if body.GPUModel == "" {
		body.GPUModel = "any"
	}
	req := ops.CreateReservationReq{
		GPUModel: body.GPUModel, GPUCount: body.GPUCount, StartAt: start, EndAt: end,
	}
	if body.ProjectID != nil && *body.ProjectID != "" {
		if pid, err := uuid.Parse(*body.ProjectID); err == nil {
			req.ProjectID = &pid
		}
	}
	res, err := a.ops.CreateReservation(r.Context(), u.OrgID, u.ID, req)
	if errors.Is(err, ops.ErrOverbooked) {
		writeErr(w, 409, "overbooked", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	writeJSON(w, 201, res)
}

func (a *API) cancelReservation(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	if err := a.ops.CancelReservation(r.Context(), u.OrgID, id); err != nil {
		writeErr(w, 500, "internal", "could not cancel reservation")
		return
	}
	w.WriteHeader(204)
}

// ── F6: Budgets ─────────────────────────────────────────────────────────────

func (a *API) listBudgets(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	out, err := a.ops.ListBudgets(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list budgets")
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) setBudget(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		ScopeType string  `json:"scope_type"`
		ScopeID   *string `json:"scope_id"`
		Amount    float64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if body.ScopeType == "" {
		body.ScopeType = "organization"
	}
	if body.ScopeType != "organization" && body.ScopeType != "department" && body.ScopeType != "project" {
		writeErr(w, 400, "validation", "scope_type must be organization, department or project")
		return
	}
	if body.Amount < 0 {
		writeErr(w, 400, "validation", "amount must be >= 0")
		return
	}
	req := ops.SetBudgetReq{ScopeType: body.ScopeType, Amount: body.Amount}
	if body.ScopeType != "organization" && body.ScopeID != nil && *body.ScopeID != "" {
		sid, err := uuid.Parse(*body.ScopeID)
		if err != nil {
			writeErr(w, 400, "validation", "invalid scope_id")
			return
		}
		req.ScopeID = &sid
	}
	if err := a.ops.SetBudget(r.Context(), u.OrgID, req); err != nil {
		writeErr(w, 500, "internal", "could not save budget")
		return
	}
	w.WriteHeader(204)
}

func (a *API) deleteBudget(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	if err := a.ops.DeleteBudget(r.Context(), u.OrgID, id); err != nil {
		writeErr(w, 500, "internal", "could not delete budget")
		return
	}
	w.WriteHeader(204)
}

// ── F5: Cost alerts ───────────────────────────────────────────────────────────

func (a *API) listAlerts(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	includeAcked := r.URL.Query().Get("all") == "true"
	out, err := a.ops.ListAlerts(r.Context(), u.OrgID, includeAcked)
	if err != nil {
		writeErr(w, 500, "internal", "could not list alerts")
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	if err := a.ops.AcknowledgeAlert(r.Context(), u.OrgID, id); err != nil {
		writeErr(w, 500, "internal", "could not acknowledge alert")
		return
	}
	w.WriteHeader(204)
}

func (a *API) listAlertRules(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	out, err := a.ops.ListRules(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list alert rules")
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) createAlertRule(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		Type      string  `json:"type"`
		Threshold float64 `json:"threshold"`
		ScopeID   *string `json:"scope_id"`
		Severity  string  `json:"severity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	req := ops.CreateRuleReq{Type: body.Type, Threshold: body.Threshold, Severity: body.Severity}
	if body.ScopeID != nil && *body.ScopeID != "" {
		sid, err := uuid.Parse(*body.ScopeID)
		if err != nil {
			writeErr(w, 400, "validation", "invalid scope_id")
			return
		}
		req.ScopeID = &sid
	}
	id, err := a.ops.CreateRule(r.Context(), u.OrgID, req)
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"id": id})
}

func (a *API) toggleAlertRule(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if err := a.ops.SetRuleEnabled(r.Context(), u.OrgID, id, body.Enabled); err != nil {
		writeErr(w, 500, "internal", "could not update rule")
		return
	}
	w.WriteHeader(204)
}

func (a *API) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	if err := a.ops.DeleteRule(r.Context(), u.OrgID, id); err != nil {
		writeErr(w, 500, "internal", "could not delete rule")
		return
	}
	w.WriteHeader(204)
}
