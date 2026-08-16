package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
)

// ── Deployment config (dev-mode visibility) ───────────────────────────────────

// config tells the frontend whether this deployment is running on synthetic
// (development) GPUs so it can show an unmistakable banner / badges. It also
// advertises the optional capabilities this deployment actually has wired up, so
// the UI can hide affordances that would only ever fail:
//
//   - self_topup_enabled mirrors BILLING_ALLOW_SELF_TOPUP. Off means there is no
//     payment provider and POST /billing/credits/topup answers 403; the billing
//     page renders the "contact your operator" state instead of dead buttons.
//   - email_verification_enabled is true only when an email provider (Resend) is
//     configured. Off means a verification link can never reach the user's inbox,
//     so the UI must not ask them to verify. Configure RESEND_API_KEY +
//     INVITE_FROM_EMAIL and the prompt returns on its own — nothing to re-code.
func (a *API) config(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	gpus, _ := a.inventory.ListGPUs(r.Context(), u.OrgID, "")
	synthetic, total := 0, len(gpus)
	for _, g := range gpus {
		if strings.Contains(strings.ToLower(g.Model), "synthetic") || strings.HasPrefix(g.UUID, "GPU-DEV") {
			synthetic++
		}
	}
	devMode := synthetic > 0
	writeJSON(w, 200, map[string]any{
		"dev_mode":                   devMode,
		"synthetic_gpu_count":        synthetic,
		"total_gpu_count":            total,
		"disclaimer":                 "Synthetic GPUs are simulated for development. Metrics are generated, not measured. Not production hardware.",
		"self_topup_enabled":         a.allowSelfTopup,
		"email_verification_enabled": a.emailEnabled(),
	})
}

// ── Templates ─────────────────────────────────────────────────────────────────

func templateResponse(t domain.Template) map[string]any {
	r := map[string]any{
		"id":                     t.ID.String(),
		"name":                   t.Name,
		"description":            t.Description,
		"base_image":             t.BaseImage,
		"software":               t.Software,
		"version":                t.Version,
		"tags":                   t.Tags,
		"default_expose_ssh":     t.DefaultExposeSSH,
		"default_expose_jupyter": t.DefaultExposeJupyter,
		"built_in":               t.BuiltIn,
		"created_at":             t.CreatedAt.UTC().Format(time.RFC3339),
	}
	if t.OrgID != nil {
		r["org_id"] = t.OrgID.String()
	}
	return r
}

func (a *API) listTemplates(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	tpls, err := a.nodes.ListTemplates(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list templates")
		return
	}
	out := make([]map[string]any, 0, len(tpls))
	for _, t := range tpls {
		out = append(out, templateResponse(t))
	}
	writeJSON(w, 200, out)
}

func (a *API) getTemplate(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	t, err := a.nodes.GetTemplate(r.Context(), u.OrgID, id)
	if err != nil {
		writeErr(w, 404, "not_found", "template not found")
		return
	}
	writeJSON(w, 200, templateResponse(t))
}

func (a *API) createTemplate(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		Name                 string                    `json:"name"`
		Description          string                    `json:"description"`
		BaseImage            string                    `json:"base_image"`
		Software             []domain.TemplateSoftware `json:"software"`
		Version              string                    `json:"version"`
		Tags                 []string                  `json:"tags"`
		DefaultExposeSSH     bool                      `json:"default_expose_ssh"`
		DefaultExposeJupyter bool                      `json:"default_expose_jupyter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if body.Name == "" || body.BaseImage == "" {
		writeErr(w, 400, "validation", "name and base_image are required")
		return
	}
	t, err := a.nodes.CreateTemplate(r.Context(), u.OrgID, domain.Template{
		Name:                 body.Name,
		Description:          body.Description,
		BaseImage:            body.BaseImage,
		Software:             body.Software,
		Version:              body.Version,
		Tags:                 body.Tags,
		DefaultExposeSSH:     body.DefaultExposeSSH,
		DefaultExposeJupyter: body.DefaultExposeJupyter,
	})
	if err != nil {
		writeErr(w, 500, "internal", "could not create template")
		return
	}
	a.userEvent(r, u, "template.create", "template", t.ID.String(),
		map[string]any{"name": t.Name, "base_image": t.BaseImage})
	writeJSON(w, 201, templateResponse(t))
}

// ── Departments ─────────────────────────────────────────────────────────────

func (a *API) listDepartments(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	depts, err := a.nodes.ListDepartments(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list departments")
		return
	}
	writeJSON(w, 200, depts)
}

func (a *API) createDepartment(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeErr(w, 400, "validation", "name is required")
		return
	}
	d, err := a.nodes.CreateDepartment(r.Context(), u.OrgID, body.Name, body.Description)
	if err != nil {
		writeErr(w, 500, "internal", "could not create department")
		return
	}
	a.userEvent(r, u, "department.create", "department", d.ID.String(),
		map[string]any{"name": body.Name})
	writeJSON(w, 201, d)
}

func (a *API) assignUserDepartment(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	uid := parseUUID(w, r, "id")
	if uid == uuid.Nil {
		return
	}
	var body struct {
		DepartmentID *string `json:"department_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var dept *uuid.UUID
	if body.DepartmentID != nil && *body.DepartmentID != "" {
		d, err := uuid.Parse(*body.DepartmentID)
		if err != nil {
			writeErr(w, 400, "validation", "invalid department_id")
			return
		}
		dept = &d
	}
	if err := a.nodes.AssignUserDepartment(r.Context(), u.OrgID, uid, dept); err != nil {
		writeErr(w, 500, "internal", "could not assign department")
		return
	}
	meta := map[string]any{}
	if dept != nil {
		meta["department_id"] = dept.String()
	}
	a.userEvent(r, u, "user.assign_department", "user", uid.String(), meta)
	w.WriteHeader(204)
}

// ── Enrollment tokens (list / revoke) ─────────────────────────────────────────

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	toks, err := a.identity.ListEnrollmentTokens(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list tokens")
		return
	}
	writeJSON(w, 200, toks)
}

func (a *API) revokeToken(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	if err := a.identity.RevokeEnrollmentToken(r.Context(), u.OrgID, id); err != nil {
		writeErr(w, 404, "not_found", err.Error())
		return
	}
	a.userEvent(r, u, "enrollment_token.revoke", "enrollment_token", id.String(), nil)
	w.WriteHeader(204)
}

// ── Chargeback CSV export ─────────────────────────────────────────────────────

func (a *API) chargebackCSV(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	q := r.URL.Query()
	from := parseTime(q.Get("from"), firstOfMonth())
	to := parseTime(q.Get("to"), time.Now())
	groupBy := q.Get("group_by")
	if groupBy == "" {
		groupBy = "department"
	}
	report, err := a.billing.Chargeback(r.Context(), u.OrgID, from, to, groupBy)
	if err != nil {
		writeErr(w, 500, "internal", "could not generate report")
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=chargeback-%s-%s.csv", report.GroupBy, time.Now().Format("20060102")))
	w.WriteHeader(200)
	fmt.Fprintf(w, "%s,gpu_hours,utilization_pct,amount,currency\n", report.GroupBy)
	for _, row := range report.Rows {
		fmt.Fprintf(w, "%s,%.2f,%.1f,%.4f,%s\n",
			csvEscape(row.GroupKey), row.GPUHours, row.UtilPct, row.Amount, row.Currency)
	}
	fmt.Fprintf(w, "TOTAL,,,%.4f,%s\n", report.Total, report.Currency)
}

func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
