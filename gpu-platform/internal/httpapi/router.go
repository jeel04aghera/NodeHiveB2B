// Package httpapi exposes the V1 REST surface consumed by the frontend.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/billing"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/email"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/inventory"
	"github.com/nodehive/gpu-platform/internal/nodes"
	"github.com/nodehive/gpu-platform/internal/ops"
	"github.com/nodehive/gpu-platform/internal/policy"
	"github.com/nodehive/gpu-platform/internal/telemetry"
	"github.com/nodehive/gpu-platform/internal/workloads"
)

type API struct {
	nodes     *nodes.Service
	identity  identity.Service
	inventory inventory.Service
	workloads workloads.Service
	telemetry telemetry.Service
	billing   billing.Service
	audit     audit.Service
	policy    policy.Service
	ops       *ops.Service

	// Auth/OAuth (Phase 1) — set via options; zero values disable the feature.
	google        *identity.GoogleOAuth
	appBaseURL    string   // frontend origin the OAuth callback redirects back to
	corsOrigins   []string // explicit allow-list; empty => permissive "*" (dev, no creds)
	secureCookies bool     // Secure + SameSite=None (cross-origin prod); false => Lax (local)

	// Sessions (Phase 2). refreshCookieTTL is the refresh-cookie Max-Age (0 => 30d default).
	refreshCookieTTL time.Duration

	// Email (Phase 3.5) — invitation delivery. nil => console fallback (dev).
	email email.Sender

	// allowSelfTopup permits org admins to credit their own balance via the API.
	// Default FALSE: until a payment provider is integrated, self-topup is free money —
	// production grants go through the operator. Dev/self-host enable it via config.
	allowSelfTopup bool
}

// Option configures optional API features without breaking existing NewRouter callers.
type Option func(*API)

// WithGoogleOAuth enables "Sign in with Google" (nil = disabled).
func WithGoogleOAuth(g *identity.GoogleOAuth) Option { return func(a *API) { a.google = g } }

// WithAppBaseURL sets the frontend origin the OAuth callback redirects back to.
func WithAppBaseURL(u string) Option { return func(a *API) { a.appBaseURL = u } }

// WithCORSOrigins sets the explicit CORS allow-list (enables credentialed requests).
func WithCORSOrigins(o []string) Option { return func(a *API) { a.corsOrigins = o } }

// WithSecureCookies emits Secure + SameSite=None cookies (cross-origin production).
func WithSecureCookies(b bool) Option { return func(a *API) { a.secureCookies = b } }

// WithRefreshCookieTTL sets the refresh-token cookie lifetime (default 30 days). Should
// match the service's REFRESH_TOKEN_TTL so the cookie and the server session expire together.
func WithRefreshCookieTTL(d time.Duration) Option { return func(a *API) { a.refreshCookieTTL = d } }

// WithEmailSender wires the invitation email sender (console fallback used if unset).
func WithEmailSender(s email.Sender) Option { return func(a *API) { a.email = s } }

// WithSelfTopup enables the self-serve credit top-up endpoint (dev/self-host only).
func WithSelfTopup(b bool) Option { return func(a *API) { a.allowSelfTopup = b } }

func NewRouter(
	nodesSvc *nodes.Service,
	identitySvc identity.Service,
	inventorySvc inventory.Service,
	workloadsSvc workloads.Service,
	telemetrySvc telemetry.Service,
	billingSvc billing.Service,
	auditSvc audit.Service,
	policySvc policy.Service,
	opsSvc *ops.Service,
	opts ...Option,
) http.Handler {
	a := &API{
		nodes:     nodesSvc,
		identity:  identitySvc,
		inventory: inventorySvc,
		workloads: workloadsSvc,
		telemetry: telemetrySvc,
		billing:   billingSvc,
		audit:     auditSvc,
		policy:    policySvc,
		ops:       opsSvc,
	}
	for _, opt := range opts {
		opt(a)
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(a.corsMiddleware)

	// Agent distribution (public): one-line installer + prebuilt binaries.
	r.Get("/install.sh", a.installScript)
	r.Handle("/dist/*", http.StripPrefix("/dist/", http.FileServer(http.Dir(agentDistDir()))))

	// Per-IP limiters for credential-sensitive endpoints (H3). Sized to never bother a
	// human (or the SPA's single-flight refresh) while killing brute force: bcrypt at
	// 20 attempts/min/IP makes online password guessing useless, and 30/min on the
	// token-probe endpoints makes invite/join-code guessing (2^48+ space) intractable.
	loginLimiter := newRateLimiter(20, time.Minute)
	registerLimiter := newRateLimiter(10, time.Minute)
	refreshLimiter := newRateLimiter(60, time.Minute)
	tokenProbeLimiter := newRateLimiter(30, time.Minute)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/healthz", a.health)

		// Auth (public)
		r.With(loginLimiter.middleware).Post("/auth/login", a.login)
		r.With(registerLimiter.middleware).Post("/auth/register", a.register)
		// Refresh + logout are public: gated by the HttpOnly refresh cookie, not a Bearer
		// token (the access token may already be expired when these are called).
		r.With(refreshLimiter.middleware).Post("/auth/refresh", a.refresh)
		r.Post("/auth/logout", a.logout)

		// Invite signup (no org) + public invitation preview (token-gated).
		r.With(registerLimiter.middleware).Post("/auth/register-pending", a.registerPending)
		r.With(tokenProbeLimiter.middleware).Get("/auth/invitations/{token}", a.previewInvitation)

		// Google OAuth (public). Handlers no-op with 404 when OAuth isn't configured.
		r.Get("/auth/google/start", a.googleStart)
		r.With(loginLimiter.middleware).Get("/auth/google/callback", a.googleCallback)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(a.authMiddleware)

			// Pre-onboarding users (no org yet) may ONLY reach these:
			r.Get("/me", a.me)
			r.Post("/onboarding/organization", a.onboardingCreateOrg)
			// Join an existing org instead of creating one (invite token / join code).
			// Token/code redemption is rate-limited: these accept guessable secrets.
			r.With(tokenProbeLimiter.middleware).Post("/onboarding/accept-invitation", a.acceptInvitation)
			r.With(tokenProbeLimiter.middleware).Post("/onboarding/join-code", a.joinViaCode)

			// Active session management (available to every authenticated user, incl.
			// pre-onboarding — they still have devices/sessions to manage).
			r.Get("/auth/sessions", a.listSessions)
			r.Delete("/auth/sessions/{id}", a.revokeSession)
			r.Post("/auth/sessions/revoke-all", a.revokeAllSessions)

			// Everything below requires the user to belong to an org (post-onboarding).
			r.Group(func(r chi.Router) {
				r.Use(a.requireOrg)
				r.Get("/config", a.config) // deployment mode (dev banner, synthetic flags)

				// Nodes
				r.Get("/nodes", a.listNodes)
				r.Get("/nodes/{id}", a.getNode)
				r.Get("/nodes/{id}/gpus", a.listNodeGPUs)

				// GPUs
				r.Get("/gpus", a.listGPUs)

				// Templates (environment registry)
				r.Get("/templates", a.listTemplates)
				r.Get("/templates/{id}", a.getTemplate)
				r.Post("/templates", a.createTemplate) // admin

				// Workloads
				r.Get("/workloads", a.listWorkloads)
				r.Post("/workloads", a.launchWorkload)
				r.Get("/workloads/{id}", a.getWorkload)
				r.Post("/workloads/{id}/stop", a.stopWorkload)
				r.Get("/workloads/{id}/events", a.workloadEvents)
				r.Get("/workloads/{id}/logs", a.workloadLogs)
				r.Get("/queue", a.queue) // F3 — GPU queue dashboard

				// Metrics
				r.Get("/metrics/summary", a.fleetSummary)
				r.Get("/metrics/utilization", a.utilization)
				r.Get("/metrics/idle", a.idleReport)

				// Billing
				r.Get("/billing/chargeback", a.chargeback)
				r.Get("/billing/chargeback.csv", a.chargebackCSV)
				r.Get("/billing/rates", a.listRates)
				r.Post("/billing/rates", a.setRate)
				r.Get("/billing/credits", a.creditSummary)
				r.Get("/billing/ledger", a.creditLedger)
				r.Post("/billing/credits/topup", a.topupCredits)

				// Projects
				r.Get("/projects", a.listProjects)
				r.Post("/projects", a.createProject)

				// Departments (org structure)
				r.Get("/departments", a.listDepartments)
				r.Post("/departments", a.createDepartment) // admin

				// Organization membership, roles, invitations, join codes (Phase 3).
				// Read is available to any member; mutations require admin (owner ≥ admin).
				r.Get("/org/members", a.listMembers)
				r.Get("/org/invitations", a.listInvitations)
				r.Get("/org/join-codes", a.listJoinCodes)
				// Any member can leave their own org (last owner is blocked in the service).
				r.Post("/org/leave", a.leaveOrg)
				r.Group(func(r chi.Router) {
					r.Use(a.requireRole(domain.RoleAdmin))
					r.Patch("/org/members/{userId}/role", a.changeMemberRole)
					r.Delete("/org/members/{userId}", a.removeMember)
					r.Post("/org/invitations", a.createInvitation)
					r.Post("/org/invitations/{id}/resend", a.resendInvitation)
					r.Delete("/org/invitations/{id}", a.revokeInvitation)
					r.Post("/org/join-codes", a.createJoinCode)
					r.Delete("/org/join-codes/{id}", a.revokeJoinCode)
				})
				r.Group(func(r chi.Router) {
					r.Use(a.requireRole(domain.RoleOwner))
					r.Post("/org/transfer-ownership", a.transferOwnership)
				})

				// Users (admin only)
				r.Get("/users", a.listUsers)
				r.Post("/users", a.createUser)
				r.Patch("/users/{id}/department", a.assignUserDepartment) // admin

				// Enrollment tokens (admin only)
				r.Get("/enrollment-tokens", a.listTokens)
				r.Post("/enrollment-tokens", a.issueToken)
				r.Delete("/enrollment-tokens/{id}", a.revokeToken)

				// Audit
				r.Get("/audit-logs", a.auditLogs)

				// Reservations (F4)
				r.Get("/reservations", a.listReservations)
				r.Post("/reservations", a.createReservation)
				r.Delete("/reservations/{id}", a.cancelReservation)

				// Budgets (F6)
				r.Get("/budgets", a.listBudgets)
				r.Post("/budgets", a.setBudget)           // admin
				r.Delete("/budgets/{id}", a.deleteBudget) // admin

				// Cost alerts (F5)
				r.Get("/alerts", a.listAlerts)
				r.Post("/alerts/{id}/ack", a.acknowledgeAlert)
				r.Get("/alert-rules", a.listAlertRules)
				r.Post("/alert-rules", a.createAlertRule)        // admin
				r.Patch("/alert-rules/{id}", a.toggleAlertRule)  // admin
				r.Delete("/alert-rules/{id}", a.deleteAlertRule) // admin
			}) // end requireOrg subgroup
		}) // end authenticated group
	})
	return r
}

// ── Auth ──────────────────────────────────────────────────────────────────────

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	token, user, err := a.identity.Login(r.Context(), body.Email, body.Password)
	if errors.Is(err, identity.ErrInvalidCredentials) {
		writeErr(w, 401, "unauthorized", "invalid email or password")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "login failed")
		return
	}
	a.startSession(w, r, user.ID) // additive: also sets the refresh cookie
	writeJSON(w, 200, map[string]any{
		"token": token,
		"user":  userResponse(user),
	})
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrgName  string `json:"org_name"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	token, user, err := a.identity.Register(r.Context(), body.OrgName, body.Email, body.Name, body.Password)
	if errors.Is(err, identity.ErrUserExists) {
		writeErr(w, 409, "conflict", "an account with that email already exists")
		return
	}
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	a.startSession(w, r, user.ID) // additive: also sets the refresh cookie
	writeJSON(w, 201, map[string]any{
		"token": token,
		"user":  userResponse(user),
	})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	writeJSON(w, 200, userResponse(u))
}

// ── Nodes ─────────────────────────────────────────────────────────────────────

type NodeResponse struct {
	ID           string  `json:"id"`
	Hostname     string  `json:"hostname"`
	Status       string  `json:"status"`
	OS           string  `json:"os,omitempty"`
	NvidiaDriver string  `json:"nvidia_driver,omitempty"`
	CUDAVersion  string  `json:"cuda_version,omitempty"`
	AgentVersion string  `json:"agent_version,omitempty"`
	CPUCores     int     `json:"cpu_cores"`
	RAMMB        int64   `json:"ram_mb"`
	GPUCount     int     `json:"gpu_count"`
	EnrolledAt   string  `json:"enrolled_at"`
	LastSeenAt   *string `json:"last_seen_at"`
}

func (a *API) listNodes(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	views, err := a.nodes.List(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list nodes")
		return
	}
	out := make([]NodeResponse, 0, len(views))
	for _, v := range views {
		out = append(out, toNodeResponse(v))
	}
	writeJSON(w, 200, out)
}

func (a *API) getNode(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	d, err := a.nodes.NodeDetail(r.Context(), u.OrgID, id)
	if errors.Is(err, nodes.ErrNotFound) {
		writeErr(w, 404, "not_found", "node not found")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not get node")
		return
	}
	gpus := make([]map[string]any, 0, len(d.GPUs))
	for _, g := range d.GPUs {
		gpus = append(gpus, gpuResponse(g))
	}
	writeJSON(w, 200, map[string]any{
		"id":                d.ID.String(),
		"hostname":          d.Hostname,
		"status":            d.Status,
		"health":            d.Health,
		"os":                d.OS,
		"kernel":            d.Kernel,
		"cpu_model":         d.CPUModel,
		"cpu_cores":         d.CPUCores,
		"ram_mb":            d.RAMMB,
		"nvidia_driver":     d.NvidiaDriver,
		"cuda_version":      d.CUDAVersion,
		"agent_version":     d.AgentVersion,
		"enrolled_at":       d.EnrolledAt.UTC().Format(time.RFC3339),
		"last_seen_at":      nodeLastSeen(d.LastSeenAt),
		"synthetic":         d.Synthetic,
		"gpu_count":         len(d.GPUs),
		"gpus":              gpus,
		"running_workloads": d.RunningWorkloads,
	})
}

func nodeLastSeen(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func (a *API) listNodeGPUs(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	gpus, err := a.inventory.ListGPUs(r.Context(), u.OrgID, "")
	if err != nil {
		writeErr(w, 500, "internal", "could not list gpus")
		return
	}
	// Filter to this node
	var out []any
	for _, g := range gpus {
		if g.NodeID == id {
			out = append(out, gpuResponse(g))
		}
	}
	writeJSON(w, 200, out)
}

// ── GPUs ──────────────────────────────────────────────────────────────────────

func (a *API) listGPUs(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	status := domain.GPUStatus(r.URL.Query().Get("status"))
	gpus, err := a.inventory.ListGPUs(r.Context(), u.OrgID, status)
	if err != nil {
		writeErr(w, 500, "internal", "could not list gpus")
		return
	}
	out := make([]any, 0, len(gpus))
	for _, g := range gpus {
		out = append(out, gpuResponse(g))
	}
	writeJSON(w, 200, out)
}

// ── Workloads ─────────────────────────────────────────────────────────────────

func (a *API) listWorkloads(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	f := workloads.ListFilter{
		Status: domain.WorkloadState(r.URL.Query().Get("status")),
	}
	list, err := a.workloads.List(r.Context(), u.OrgID, f)
	if err != nil {
		writeErr(w, 500, "internal", "could not list workloads")
		return
	}
	out := make([]any, 0, len(list))
	for _, wl := range list {
		out = append(out, workloadResponse(wl, canViewWorkloadSecrets(u, wl)))
	}
	writeJSON(w, 200, out)
}

func (a *API) launchWorkload(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Name           string  `json:"name"`
		TemplateID     string  `json:"template_id"` // template UUID (real registry)
		Image          string  `json:"image"`       // optional ad-hoc image override
		GPUType        string  `json:"gpu_type"`
		GPUCount       int     `json:"gpu_count"`
		ProjectID      *string `json:"project_id"`
		DepartmentID   *string `json:"department_id"`
		IdleTimeoutSec *int    `json:"idle_timeout_sec"`
		ExposeSSH      *bool   `json:"expose_ssh"`
		ExposeJupyter  *bool   `json:"expose_jupyter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if body.Name == "" {
		writeErr(w, 400, "validation", "name is required")
		return
	}
	if body.GPUCount <= 0 {
		body.GPUCount = 1
	}

	req := workloads.LaunchRequest{
		UserID:         u.ID,
		Name:           body.Name,
		GPUType:        body.GPUType,
		GPUCount:       body.GPUCount,
		IdleTimeoutSec: body.IdleTimeoutSec,
		Image:          body.Image,
	}

	// Resolve template_id. A UUID is a registry template (PyTorch/TensorFlow/…).
	// A non-UUID value is treated as an ad-hoc container image for backward
	// compatibility with older clients that sent the image string as template_id —
	// launch must never hard-fail just because the template registry is in use.
	if body.TemplateID != "" {
		if tid, err := uuid.Parse(body.TemplateID); err == nil {
			tpl, err := a.nodes.GetTemplate(r.Context(), u.OrgID, tid)
			if err != nil {
				writeErr(w, 400, "validation", "unknown template")
				return
			}
			req.TemplateID = &tpl.ID
			req.Image = tpl.BaseImage
			req.ExposeSSH = tpl.DefaultExposeSSH
			req.ExposeJupyter = tpl.DefaultExposeJupyter
		} else if req.Image == "" {
			// Legacy / ad-hoc: template_id is actually a container image reference.
			req.Image = body.TemplateID
		}
	}
	// Explicit overrides win over template defaults.
	if body.ExposeSSH != nil {
		req.ExposeSSH = *body.ExposeSSH
	}
	if body.ExposeJupyter != nil {
		req.ExposeJupyter = *body.ExposeJupyter
	}
	if req.Image == "" {
		writeErr(w, 400, "validation", "template_id or image is required")
		return
	}

	if body.ProjectID != nil && *body.ProjectID != "" {
		if pid, err := uuid.Parse(*body.ProjectID); err == nil {
			req.ProjectID = &pid
		}
	}
	// Department: explicit, else inherit the launching user's department.
	if body.DepartmentID != nil && *body.DepartmentID != "" {
		if did, err := uuid.Parse(*body.DepartmentID); err == nil {
			req.DepartmentID = &did
		}
	} else if u.DepartmentID != nil {
		req.DepartmentID = u.DepartmentID
	}

	wl, err := a.workloads.Launch(r.Context(), u.OrgID, req)
	if errors.Is(err, workloads.ErrNoGPUsAvailable) {
		writeErr(w, 409, "no_capacity", "no GPUs available matching request")
		return
	}
	if errors.Is(err, workloads.ErrInvalidRequest) {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	// Billing admission refusals (402): the request was valid but the org can't pay.
	if errors.Is(err, billing.ErrInsufficientCredit) {
		writeErr(w, 402, "insufficient_credit", "your organization has no remaining credit; add credits to launch workloads")
		return
	}
	if errors.Is(err, billing.ErrBudgetExceeded) {
		writeErr(w, 402, "budget_exceeded", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not launch workload")
		return
	}

	go func() {
		_ = a.audit.Record(context.Background(), domain.AuditLog{
			OrgID: u.OrgID, ActorType: "user", ActorID: u.ID.String(),
			Action: "workload.launch", TargetType: "workload", TargetID: wl.ID.String(),
		})
	}()
	// The launcher is the owner — they always see their own SSH credential.
	writeJSON(w, 201, workloadResponse(wl, true))
}

func (a *API) getWorkload(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	d, err := a.workloads.Detail(r.Context(), u.OrgID, id)
	if errors.Is(err, workloads.ErrNotFound) {
		writeErr(w, 404, "not_found", "workload not found")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not get workload")
		return
	}
	resp := workloadResponse(d.Workload, canViewWorkloadSecrets(u, d.Workload))
	resp["gpus"] = d.GPUs
	resp["runtime_seconds"] = d.RuntimeSeconds
	resp["runtime_cost"] = d.RuntimeCost
	resp["currency"] = d.Currency

	// Resolve human-readable owner / department / template names for the detail page.
	resp["owner"] = a.resolveUserLabel(r.Context(), u.OrgID, d.Workload.UserID)
	if d.Workload.DepartmentID != nil {
		resp["department"] = a.resolveDepartmentName(r.Context(), u.OrgID, *d.Workload.DepartmentID)
	}
	if d.Workload.TemplateID != nil {
		if tpl, err := a.nodes.GetTemplate(r.Context(), u.OrgID, *d.Workload.TemplateID); err == nil {
			resp["template"] = tpl.Name
			resp["template_version"] = tpl.Version
		}
	}
	writeJSON(w, 200, resp)
}

// resolveUserLabel returns "Name <email>" (or just email) for a user id.
func (a *API) resolveUserLabel(ctx context.Context, orgID, userID uuid.UUID) string {
	users, err := a.identity.ListUsers(ctx, orgID)
	if err != nil {
		return ""
	}
	for _, us := range users {
		if us.ID == userID {
			if us.Name != "" {
				return us.Name + " <" + us.Email + ">"
			}
			return us.Email
		}
	}
	return ""
}

func (a *API) resolveDepartmentName(ctx context.Context, orgID, deptID uuid.UUID) string {
	depts, err := a.nodes.ListDepartments(ctx, orgID)
	if err != nil {
		return ""
	}
	for _, d := range depts {
		if d.ID == deptID {
			return d.Name
		}
	}
	return ""
}

func (a *API) stopWorkload(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	reason := domain.StopUser
	if u.Role.AtLeast(domain.RoleAdmin) {
		if r.URL.Query().Get("reason") == "admin" {
			reason = domain.StopAdmin
		}
	}
	// Org-scoped ownership gate: a workload outside the caller's org is invisible
	// (404) and cannot be cancelled or stopped (cross-tenant write prevention).
	wl, err := a.workloads.Get(r.Context(), u.OrgID, id)
	if errors.Is(err, workloads.ErrNotFound) {
		writeErr(w, 404, "not_found", "workload not found")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not load workload")
		return
	}
	// A queued workload has no container yet — cancel it out of the queue (F3).
	if wl.Status == domain.WorkloadQueued {
		if err := a.workloads.CancelQueued(r.Context(), id); err != nil {
			writeErr(w, 500, "internal", "could not cancel queued workload")
			return
		}
		w.WriteHeader(204)
		return
	}
	if err := a.workloads.Stop(r.Context(), id, reason); err != nil {
		writeErr(w, 500, "internal", "could not stop workload")
		return
	}
	go func() {
		_ = a.audit.Record(context.Background(), domain.AuditLog{
			OrgID: u.OrgID, ActorType: "user", ActorID: u.ID.String(),
			Action: "workload.stop", TargetType: "workload", TargetID: id.String(),
			Metadata: map[string]any{"reason": string(reason)},
		})
	}()
	w.WriteHeader(204)
}

func (a *API) workloadLogs(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	wl, err := a.workloads.Get(r.Context(), u.OrgID, id)
	if err != nil {
		writeErr(w, 404, "not_found", "workload not found")
		return
	}
	if !canViewWorkloadSecrets(u, wl) {
		writeErr(w, 403, "forbidden", "only the workload owner or an admin can view logs")
		return
	}
	writeJSON(w, 200, map[string]string{"logs": wl.Logs})
}

func (a *API) workloadEvents(w http.ResponseWriter, r *http.Request) {
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	u := userFromCtx(r)
	events, err := a.workloads.ListEvents(r.Context(), u.OrgID, id)
	if errors.Is(err, workloads.ErrNotFound) {
		writeErr(w, 404, "not_found", "workload not found")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not query events")
		return
	}
	writeJSON(w, 200, events)
}

func (a *API) queue(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	stats, err := a.workloads.ListQueue(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not load queue")
		return
	}
	writeJSON(w, 200, stats)
}

// ── Metrics ───────────────────────────────────────────────────────────────────

func (a *API) fleetSummary(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	sum, err := a.telemetry.FleetSummary(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not get summary")
		return
	}
	writeJSON(w, 200, map[string]any{
		"gpu_total":        sum.GPUTotal,
		"gpus_idle":        sum.GPUsIdle,
		"avg_util_pct":     sum.AvgUtilPct,
		"idle_cost_24h":    sum.IdleCost24h,
		"workloads_active": sum.WorkloadsActive,
	})
}

func (a *API) utilization(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	q := r.URL.Query()
	from := parseTime(q.Get("from"), time.Now().Add(-24*time.Hour))
	to := parseTime(q.Get("to"), time.Now())
	scope := q.Get("scope")
	if scope == "" {
		scope = "fleet"
	}
	var scopeID uuid.UUID
	if idStr := q.Get("id"); idStr != "" {
		scopeID, _ = uuid.Parse(idStr)
	}

	pts, err := a.telemetry.Utilization(r.Context(), u.OrgID, telemetry.UtilQuery{
		Scope:    scope,
		ID:       scopeID,
		From:     from,
		To:       to,
		Interval: 5 * time.Minute,
	})
	if err != nil {
		writeErr(w, 500, "internal", "could not get utilization")
		return
	}
	out := make([]map[string]any, 0, len(pts))
	for _, p := range pts {
		out = append(out, map[string]any{
			"ts":       p.TS.UTC().Format(time.RFC3339),
			"util_pct": p.UtilPct,
			"mem_pct":  p.MemPct,
		})
	}
	writeJSON(w, 200, out)
}

// ── Billing ───────────────────────────────────────────────────────────────────

func (a *API) chargeback(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	q := r.URL.Query()
	from := parseTime(q.Get("from"), firstOfMonth())
	to := parseTime(q.Get("to"), time.Now())
	groupBy := q.Get("group_by")
	if groupBy == "" {
		groupBy = "project"
	}
	report, err := a.billing.Chargeback(r.Context(), u.OrgID, from, to, groupBy)
	if err != nil {
		writeErr(w, 500, "internal", "could not generate report")
		return
	}
	writeJSON(w, 200, report)
}

func (a *API) listRates(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	rates, err := a.billing.ListRates(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list rates")
		return
	}
	out := make([]any, 0, len(rates))
	for _, rc := range rates {
		out = append(out, rateCardResponse(rc))
	}
	writeJSON(w, 200, out)
}

func (a *API) setRate(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		GPUModel       string  `json:"gpu_model"`
		RatePerGPUHour float64 `json:"rate_per_gpu_hour"`
		Currency       string  `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if body.Currency == "" {
		body.Currency = "USD"
	}
	rc, err := a.billing.SetRate(r.Context(), u.OrgID, body.GPUModel, body.RatePerGPUHour, body.Currency)
	if err != nil {
		writeErr(w, 500, "internal", "could not set rate")
		return
	}
	writeJSON(w, 201, rateCardResponse(rc))
}

func (a *API) creditSummary(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	sum, err := a.billing.CreditSummary(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not load credit balance")
		return
	}
	writeJSON(w, 200, sum)
}

func (a *API) creditLedger(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	entries, err := a.billing.Ledger(r.Context(), u.OrgID, limit)
	if err != nil {
		writeErr(w, 500, "internal", "could not load ledger")
		return
	}
	writeJSON(w, 200, entries)
}

func (a *API) topupCredits(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !a.allowSelfTopup {
		writeErr(w, 403, "topup_disabled",
			"self-serve top-ups are disabled on this deployment; contact your provider to add credits")
		return
	}
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if body.Description == "" {
		body.Description = "Credit top-up"
	}
	balance, err := a.billing.AddCredit(r.Context(), u.OrgID, body.Amount, "topup", body.Description)
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"balance": balance})
}

// ── Projects ──────────────────────────────────────────────────────────────────

func (a *API) listProjects(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	rows, err := a.nodes.ListProjects(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list projects")
		return
	}
	writeJSON(w, 200, rows)
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeErr(w, 400, "validation", "name is required")
		return
	}
	p, err := a.nodes.CreateProject(r.Context(), u.OrgID, body.Name)
	if err != nil {
		writeErr(w, 500, "internal", "could not create project")
		return
	}
	writeJSON(w, 201, p)
}

// ── Users ─────────────────────────────────────────────────────────────────────

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	users, err := a.identity.ListUsers(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list users")
		return
	}
	out := make([]any, 0, len(users))
	for _, usr := range users {
		out = append(out, userResponse(usr))
	}
	writeJSON(w, 200, out)
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		Email        string  `json:"email"`
		Name         string  `json:"name"`
		Role         string  `json:"role"`
		DepartmentID *string `json:"department_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	role := domain.RoleMember
	if body.Role == "admin" {
		role = domain.RoleAdmin
	}
	usr, err := a.identity.CreateUser(r.Context(), u.OrgID, body.Email, body.Name, role)
	if err != nil {
		writeErr(w, 500, "internal", "could not create user")
		return
	}
	if body.DepartmentID != nil && *body.DepartmentID != "" {
		if did, perr := uuid.Parse(*body.DepartmentID); perr == nil {
			_ = a.nodes.AssignUserDepartment(r.Context(), u.OrgID, usr.ID, &did)
			usr.DepartmentID = &did
		}
	}
	go func() {
		_ = a.audit.Record(context.Background(), domain.AuditLog{
			OrgID: u.OrgID, ActorType: "user", ActorID: u.ID.String(),
			Action: "user.create", TargetType: "user", TargetID: usr.ID.String(),
			Metadata: map[string]any{"email": usr.Email, "role": string(usr.Role)},
		})
	}()
	writeJSON(w, 201, userResponse(usr))
}

// ── Enrollment tokens ─────────────────────────────────────────────────────────

func (a *API) issueToken(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if !u.Role.AtLeast(domain.RoleAdmin) {
		writeErr(w, 403, "forbidden", "admin only")
		return
	}
	var body struct {
		Description string `json:"description"`
		TTLDays     int    `json:"ttl_days"`
		MaxUses     int    `json:"max_uses"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ttl := 365 * 24 * time.Hour
	if body.TTLDays > 0 {
		ttl = time.Duration(body.TTLDays) * 24 * time.Hour
	}
	maxUses := 100
	if body.MaxUses > 0 {
		maxUses = body.MaxUses
	}
	raw, err := a.identity.IssueEnrollmentTokenDesc(r.Context(), u.OrgID, u.ID, body.Description, ttl, maxUses)
	if err != nil {
		writeErr(w, 500, "internal", "could not issue token")
		return
	}
	go func() {
		_ = a.audit.Record(context.Background(), domain.AuditLog{
			OrgID: u.OrgID, ActorType: "user", ActorID: u.ID.String(),
			Action: "enrollment_token.issue", TargetType: "enrollment_token",
			Metadata: map[string]any{"description": body.Description},
		})
	}()
	writeJSON(w, 201, map[string]string{"token": raw})
}

// ── Audit ─────────────────────────────────────────────────────────────────────

func (a *API) auditLogs(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	from := parseTime(r.URL.Query().Get("from"), time.Now().Add(-24*time.Hour))
	to := parseTime(r.URL.Query().Get("to"), time.Now())
	logs, err := a.audit.Query(r.Context(), u.OrgID, from, to, 100)
	if err != nil {
		writeErr(w, 500, "internal", "could not query audit logs")
		return
	}
	writeJSON(w, 200, logs)
}

func (a *API) idleReport(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	report, err := a.policy.IdleReport(r.Context(), u.OrgID,
		time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		writeErr(w, 500, "internal", "could not evaluate idle")
		return
	}
	type finding struct {
		GPUID      string  `json:"gpu_id"`
		IdleSec    int64   `json:"idle_seconds"`
		IdleCost   float64 `json:"idle_cost"`
		WorkloadID *string `json:"workload_id,omitempty"`
	}
	out := make([]finding, 0, len(report.IdleGPUs))
	for _, f := range report.IdleGPUs {
		fi := finding{GPUID: f.GPUID.String(), IdleSec: f.IdleSeconds, IdleCost: f.IdleCost}
		if f.WorkloadID != nil {
			s := f.WorkloadID.String()
			fi.WorkloadID = &s
		}
		out = append(out, fi)
	}
	writeJSON(w, 200, map[string]any{
		"total_idle_cost": report.TotalIdleCost,
		"gpu_count":       len(out),
		"findings":        out,
	})
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// ── Auth middleware ───────────────────────────────────────────────────────────

type ctxUserKey struct{}

func (a *API) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeErr(w, 401, "unauthorized", "missing bearer token")
			return
		}
		u, err := a.identity.Authenticate(r.Context(), token)
		if err != nil {
			writeErr(w, 401, "unauthorized", "invalid or expired token")
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

func userFromCtx(r *http.Request) domain.User {
	u, _ := r.Context().Value(ctxUserKey{}).(domain.User)
	return u
}

// setUser is defined in context.go as withUser — alias here for readability.

// requireOrg blocks pre-onboarding users (no org yet) from the main application.
// Per the onboarding contract they may only reach /me and the onboarding endpoints;
// everything else returns 409 until they create or join an organization.
func (a *API) requireOrg(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !userFromCtx(r).Onboarded() {
			writeErr(w, 409, "onboarding_required", "create or join an organization to continue")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware reflects an allowed origin and enables credentialed (cookie) requests.
// Behavior by configuration:
//   - explicit allow-list (production): echo the request Origin if permitted + Allow-Credentials.
//   - no allow-list, dev (insecure cookies): reflect the request Origin + Allow-Credentials so
//     a cross-origin localhost frontend can use the refresh-cookie flow without extra config.
//   - no allow-list, production (misconfigured): fall back to "*" WITHOUT credentials — never
//     reflect an arbitrary origin with credentials in prod (that would defeat CORS).
// "*" and credentials are mutually exclusive per the CORS spec, so they are never combined.
func (a *API) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		w.Header().Set("Vary", "Origin")
		switch {
		case len(a.corsOrigins) > 0:
			if origin != "" && a.originAllowed(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		case !a.secureCookies && origin != "":
			// Dev convenience only (Secure cookies disabled => local dev): allow any origin
			// WITH credentials so cookie auth works from localhost:3000 → localhost:8080.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		default:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Max-Age", "600") // cache preflight 10m
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) originAllowed(origin string) bool {
	for _, o := range a.corsOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

// ── Response helpers ──────────────────────────────────────────────────────────

func userResponse(u domain.User) map[string]any {
	r := map[string]any{
		"id":            u.ID.String(),
		"org_id":        nil, // null = pre-onboarding (set below when present)
		"email":         u.Email,
		"name":          u.Name,
		"role":          u.Role,
		"avatar_url":    u.AvatarURL,
		"auth_provider": u.AuthProvider,
		"onboarded":     u.Onboarded(),
	}
	if u.Onboarded() {
		r["org_id"] = u.OrgID.String()
	}
	if u.DepartmentID != nil {
		r["department_id"] = u.DepartmentID.String()
	}
	return r
}

func gpuResponse(g domain.GPU) map[string]any {
	synthetic := strings.Contains(strings.ToLower(g.Model), "synthetic") ||
		strings.HasPrefix(g.UUID, "GPU-DEV")
	return map[string]any{
		"id":          g.ID.String(),
		"node_id":     g.NodeID.String(),
		"index":       g.Index,
		"uuid":        g.UUID,
		"model":       g.Model,
		"memory_mb":   g.MemoryMB,
		"mig_enabled": g.MIGEnabled,
		"status":      g.Status,
		"synthetic":   synthetic,
		"updated_at":  g.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// canViewWorkloadSecrets gates per-workload sensitive material (SSH password, container
// logs): only the workload's owner and org admins/owners may see them. Other members
// still see the workload itself (name/status/endpoints) — just not the credentials (H1).
func canViewWorkloadSecrets(u domain.User, w domain.Workload) bool {
	return w.UserID == u.ID || u.Role.AtLeast(domain.RoleAdmin)
}

func workloadResponse(w domain.Workload, includeSecrets bool) map[string]any {
	r := map[string]any{
		"id":                  w.ID.String(),
		"name":                w.Name,
		"image":               w.Image,
		"status":              w.Status,
		"stage":               w.Stage,
		"requested_gpu_count": w.RequestedGPUCount,
		"expose_ssh":          w.ExposeSSH,
		"expose_jupyter":      w.ExposeJupyter,
		"ssh_endpoint":        w.SSHEndpoint,
		"jupyter_endpoint":    w.JupyterEndpoint,
		"container_id":        w.ContainerID,
		"created_at":          w.CreatedAt.UTC().Format(time.RFC3339),
	}
	if includeSecrets {
		r["ssh_password"] = w.SSHPassword
		r["logs"] = w.Logs
	}
	if w.NodeID != nil {
		r["node_id"] = w.NodeID.String()
	}
	if w.ProjectID != nil {
		r["project_id"] = w.ProjectID.String()
	}
	if w.DepartmentID != nil {
		r["department_id"] = w.DepartmentID.String()
	}
	if w.TemplateID != nil {
		r["template_id"] = w.TemplateID.String()
	}
	if w.StartedAt != nil {
		r["started_at"] = w.StartedAt.UTC().Format(time.RFC3339)
	}
	if w.StoppedAt != nil {
		r["stopped_at"] = w.StoppedAt.UTC().Format(time.RFC3339)
	}
	return r
}

func rateCardResponse(rc domain.RateCard) map[string]any {
	r := map[string]any{
		"id":                rc.ID.String(),
		"gpu_model":         rc.GPUModel,
		"rate_per_gpu_hour": rc.RatePerGPUHour,
		"currency":          rc.Currency,
		"effective_from":    rc.EffectiveFrom.UTC().Format(time.RFC3339),
	}
	if rc.EffectiveTo != nil {
		r["effective_to"] = rc.EffectiveTo.UTC().Format(time.RFC3339)
	}
	return r
}

func toNodeResponse(v nodes.NodeView) NodeResponse {
	r := NodeResponse{
		ID:           v.ID.String(),
		Hostname:     v.Hostname,
		Status:       v.Status,
		OS:           v.OS,
		NvidiaDriver: v.NvidiaDriver,
		CUDAVersion:  v.CUDAVersion,
		AgentVersion: v.AgentVersion,
		GPUCount:     v.GPUCount,
		EnrolledAt:   v.EnrolledAt.UTC().Format(time.RFC3339),
	}
	if v.LastSeenAt != nil {
		s := v.LastSeenAt.UTC().Format(time.RFC3339)
		r.LastSeenAt = &s
	}
	return r
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": errCode, "message": msg}})
}

func parseUUID(w http.ResponseWriter, r *http.Request, param string) uuid.UUID {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		writeErr(w, 400, "validation", "invalid "+param)
		return uuid.Nil
	}
	return id
}

func parseTime(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return def
	}
	return t
}

func firstOfMonth() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}
