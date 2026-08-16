package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/email"
	"github.com/nodehive/gpu-platform/internal/identity"
	"github.com/nodehive/gpu-platform/internal/nodes"
)

// ── API keys ──────────────────────────────────────────────────────────────────

// blockServiceAccounts refuses requests from service-account principals on
// credential-management endpoints: a machine identity must not be able to mint
// further credentials (no self-replication / privilege persistence).
func (a *API) blockServiceAccounts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userFromCtx(r).IsServiceAccount {
			writeErr(w, 403, "forbidden", "service accounts cannot manage credentials")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// listAPIKeys: members see their own personal keys; admins see every org key
// (personal + service-account) for credential inventory.
func (a *API) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var owner *uuid.UUID
	if !u.Role.AtLeast(domain.RoleAdmin) {
		owner = &u.ID
	}
	keys, err := a.identity.ListAPIKeys(r.Context(), u.OrgID, owner)
	if err != nil {
		writeErr(w, 500, "internal", "could not list API keys")
		return
	}
	writeJSON(w, 200, keys)
}

func (a *API) createAPIKey(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Name             string  `json:"name"`
		TTLDays          int     `json:"ttl_days"` // 0 = never expires
		ServiceAccountID *string `json:"service_account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	req := identity.APIKeyRequest{
		Name:      body.Name,
		CreatedBy: u.ID,
		TTL:       time.Duration(body.TTLDays) * 24 * time.Hour,
	}
	ownerKind := "user"
	if body.ServiceAccountID != nil && *body.ServiceAccountID != "" {
		// Machine keys are an admin power.
		if !u.Role.AtLeast(domain.RoleAdmin) {
			writeErr(w, 403, "forbidden", "only admins can create service-account keys")
			return
		}
		said, err := uuid.Parse(*body.ServiceAccountID)
		if err != nil {
			writeErr(w, 400, "validation", "invalid service_account_id")
			return
		}
		req.ServiceAccountID = &said
		ownerKind = "service_account"
	} else {
		// Personal key: acts as the caller, with the caller's current role.
		req.OwnerUserID = &u.ID
	}
	raw, key, err := a.identity.CreateAPIKey(r.Context(), u.OrgID, req)
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	a.userEvent(r, u, "api_key.create", "api_key", key.ID.String(), map[string]any{
		"name": key.Name, "owner": ownerKind, "prefix": key.Prefix, "ttl_days": body.TTLDays,
	})
	// The raw key appears exactly once, in this response.
	writeJSON(w, 201, map[string]any{"key": raw, "api_key": key})
}

func (a *API) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	var owner *uuid.UUID
	if !u.Role.AtLeast(domain.RoleAdmin) {
		owner = &u.ID // members may only revoke their own keys
	}
	if err := a.identity.RevokeAPIKey(r.Context(), u.OrgID, id, owner); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			writeErr(w, 404, "not_found", "API key not found")
			return
		}
		writeErr(w, 500, "internal", "could not revoke API key")
		return
	}
	a.userEvent(r, u, "api_key.revoke", "api_key", id.String(), nil)
	w.WriteHeader(204)
}

// ── Service accounts ──────────────────────────────────────────────────────────

func (a *API) listServiceAccounts(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	sas, err := a.identity.ListServiceAccounts(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list service accounts")
		return
	}
	writeJSON(w, 200, sas)
}

func (a *API) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Role        string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	sa, err := a.identity.CreateServiceAccount(r.Context(), u.OrgID, u.ID,
		body.Name, body.Description, domain.Role(body.Role))
	if errors.Is(err, identity.ErrServiceAccountExists) {
		writeErr(w, 409, "conflict", err.Error())
		return
	}
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	a.userEvent(r, u, "service_account.create", "service_account", sa.ID.String(),
		map[string]any{"name": sa.Name, "role": string(sa.Role)})
	writeJSON(w, 201, sa)
}

func (a *API) updateServiceAccount(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	var body struct {
		Disabled *bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Disabled == nil {
		writeErr(w, 400, "validation", "disabled (boolean) is required")
		return
	}
	if err := a.identity.SetServiceAccountDisabled(r.Context(), u.OrgID, id, *body.Disabled); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			writeErr(w, 404, "not_found", "service account not found")
			return
		}
		writeErr(w, 500, "internal", "could not update service account")
		return
	}
	a.userEvent(r, u, "service_account.update", "service_account", id.String(),
		map[string]any{"disabled": *body.Disabled})
	w.WriteHeader(204)
}

// ── Projects (Phase 6: isolation) ─────────────────────────────────────────────

func (a *API) getProject(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	p, err := a.nodes.GetProject(r.Context(), u.OrgID, id)
	if errors.Is(err, nodes.ErrNotFound) {
		writeErr(w, 404, "not_found", "project not found")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not load project")
		return
	}
	members, _ := a.nodes.ListProjectMembers(r.Context(), u.OrgID, id)
	writeJSON(w, 200, map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description,
		"visibility": p.Visibility, "archived_at": p.ArchivedAt,
		"created_at": p.CreatedAt, "members": members,
	})
}

func (a *API) updateProject(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Visibility  *string `json:"visibility"`
		Archived    *bool   `json:"archived"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if body.Archived != nil {
		if err := a.nodes.SetProjectArchived(r.Context(), u.OrgID, id, *body.Archived); err != nil {
			if errors.Is(err, nodes.ErrNotFound) {
				writeErr(w, 404, "not_found", "project not found")
				return
			}
			writeErr(w, 500, "internal", "could not archive project")
			return
		}
		a.userEvent(r, u, "project.archive", "project", id.String(),
			map[string]any{"archived": *body.Archived})
	}
	p, err := a.nodes.UpdateProject(r.Context(), u.OrgID, id, body.Name, body.Description, body.Visibility)
	if errors.Is(err, nodes.ErrNotFound) {
		writeErr(w, 404, "not_found", "project not found")
		return
	}
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	a.userEvent(r, u, "project.update", "project", id.String(), map[string]any{
		"visibility": p.Visibility,
	})
	writeJSON(w, 200, map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description,
		"visibility": p.Visibility, "archived_at": p.ArchivedAt, "created_at": p.CreatedAt,
	})
}

func (a *API) listProjectMembers(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	members, err := a.nodes.ListProjectMembers(r.Context(), u.OrgID, id)
	if err != nil {
		writeErr(w, 500, "internal", "could not list project members")
		return
	}
	writeJSON(w, 200, members)
}

func (a *API) addProjectMember(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	target, err := uuid.Parse(body.UserID)
	if err != nil {
		writeErr(w, 400, "validation", "invalid user_id")
		return
	}
	if err := a.nodes.AddProjectMember(r.Context(), u.OrgID, id, target, u.ID); err != nil {
		if errors.Is(err, nodes.ErrNotFound) {
			writeErr(w, 404, "not_found", "project or user not found in this organization")
			return
		}
		writeErr(w, 500, "internal", "could not add project member")
		return
	}
	a.userEvent(r, u, "project.member_add", "project", id.String(),
		map[string]any{"user_id": body.UserID})
	w.WriteHeader(204)
}

func (a *API) removeProjectMember(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id := parseUUID(w, r, "id")
	if id == uuid.Nil {
		return
	}
	target, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeErr(w, 400, "validation", "invalid user id")
		return
	}
	if err := a.nodes.RemoveProjectMember(r.Context(), u.OrgID, id, target); err != nil {
		if errors.Is(err, nodes.ErrNotFound) {
			writeErr(w, 404, "not_found", "membership not found")
			return
		}
		writeErr(w, 500, "internal", "could not remove project member")
		return
	}
	a.userEvent(r, u, "project.member_remove", "project", id.String(),
		map[string]any{"user_id": target.String()})
	w.WriteHeader(204)
}

// canViewWorkload applies project-level isolation on single-workload reads:
// the launcher and org admins always see it; others need project visibility.
func (a *API) canViewWorkload(r *http.Request, u domain.User, wl domain.Workload) bool {
	if wl.UserID == u.ID || u.Role.AtLeast(domain.RoleAdmin) || wl.ProjectID == nil {
		return true
	}
	ok, err := a.nodes.CanViewProject(r.Context(), u.OrgID, *wl.ProjectID, u.ID, false)
	return err == nil && ok
}

// ── Realtime (SSE) ────────────────────────────────────────────────────────────

// eventStream is the Server-Sent Events feed of org-scoped change hints.
// EventSource cannot set headers, so auth also accepts ?token= (access token or
// API key); the URL is not logged with its query string. Events carry only a
// topic — the SPA re-fetches the queries it already owns (see internal/events).
func (a *API) eventStream(w http.ResponseWriter, r *http.Request) {
	if a.events == nil {
		writeErr(w, 404, "not_found", "realtime not configured")
		return
	}
	token := bearerToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		writeErr(w, 401, "unauthorized", "missing token")
		return
	}
	u, err := a.authenticateToken(r.Context(), token)
	if err != nil {
		writeErr(w, 401, "unauthorized", "invalid or expired token")
		return
	}
	if !u.Onboarded() {
		writeErr(w, 409, "onboarding_required", "create or join an organization to continue")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "internal", "streaming unsupported")
		return
	}
	noteUser(r.Context(), u)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering
	w.WriteHeader(200)
	// Client reconnect backoff + an immediate comment so EventSource fires 'open'.
	fmt.Fprint(w, "retry: 3000\n: connected\n\n")
	flusher.Flush()

	ch, cancel := a.events.Subscribe(u.OrgID)
	defer cancel()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// Comment line keeps the connection alive through proxies/edges.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev := <-ch:
			payload, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "event: change\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// authenticateToken resolves either a JWT access token or an nhk_ API key.
func (a *API) authenticateToken(ctx context.Context, token string) (domain.User, error) {
	if strings.HasPrefix(token, identity.APIKeyScheme) {
		return a.identity.AuthenticateAPIKey(ctx, token)
	}
	return a.identity.Authenticate(ctx, token)
}

// notifyMembers publishes the members realtime topic (membership/invite changes).
func (a *API) notifyMembers(orgID uuid.UUID) {
	if a.events != nil {
		a.events.PublishTopics(orgID, "members")
	}
}

// ── Email verification & password reset ───────────────────────────────────────

// requestEmailVerification (re)sends the verification email for the caller.
func (a *API) requestEmailVerification(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if u.IsServiceAccount {
		writeErr(w, 403, "forbidden", "service accounts have no email")
		return
	}
	raw, user, err := a.identity.RequestEmailVerification(r.Context(), u.ID)
	if errors.Is(err, identity.ErrAlreadyVerified) {
		writeErr(w, 409, "already_verified", "your email is already verified")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not create verification token")
		return
	}
	verifyURL := a.appBaseURL + "/verify-email?token=" + raw
	a.dispatchAuthEmail(email.BuildVerifyEmail(user.Email, verifyURL))
	a.userEvent(r, u, "auth.email_verify_requested", "user", u.ID.String(), nil)
	resp := map[string]any{"sent": true}
	if !a.emailEnabled() {
		// Dev fallback (same contract as invitations): surface the link directly.
		resp["token"] = raw
		resp["verify_url"] = verifyURL
	}
	writeJSON(w, 202, resp)
}

func (a *API) confirmEmailVerification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		writeErr(w, 400, "validation", "token is required")
		return
	}
	user, err := a.identity.ConfirmEmailVerification(r.Context(), body.Token)
	if errors.Is(err, identity.ErrAuthTokenInvalid) {
		writeErr(w, 400, "invalid_token", "this verification link is invalid or has expired")
		return
	}
	if err != nil {
		writeErr(w, 500, "internal", "could not verify email")
		return
	}
	a.auditEvent(r, domain.AuditLog{
		OrgID: user.OrgID, ActorType: "user", ActorID: user.ID.String(),
		Action: "auth.email_verified", TargetType: "user", TargetID: user.ID.String(),
	})
	writeJSON(w, 200, map[string]any{"verified": true, "user": userResponse(user)})
}

// requestPasswordReset always answers 202 with the same body — whether or not the
// email exists — so the endpoint cannot be used for account enumeration.
func (a *API) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeErr(w, 400, "validation", "email is required")
		return
	}
	resp := map[string]any{"sent": true}
	raw, user, err := a.identity.RequestPasswordReset(r.Context(), body.Email)
	if err == nil {
		resetURL := a.appBaseURL + "/reset-password?token=" + raw
		a.dispatchAuthEmail(email.BuildPasswordResetEmail(user.Email, resetURL))
		a.auditEvent(r, domain.AuditLog{
			OrgID: user.OrgID, ActorType: "user", ActorID: user.ID.String(),
			Action: "auth.password_reset_requested", TargetType: "user", TargetID: user.ID.String(),
		})
		if a.email == nil || !a.email.Enabled() {
			resp["token"] = raw // dev fallback only
			resp["reset_url"] = resetURL
		}
	} else if !errors.Is(err, identity.ErrNotFound) {
		writeErr(w, 500, "internal", "could not process request")
		return
	}
	writeJSON(w, 202, resp)
}

func (a *API) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		writeErr(w, 400, "validation", "token and new_password are required")
		return
	}
	user, err := a.identity.ConfirmPasswordReset(r.Context(), body.Token, body.NewPassword)
	if errors.Is(err, identity.ErrAuthTokenInvalid) {
		writeErr(w, 400, "invalid_token", "this reset link is invalid or has expired")
		return
	}
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	a.auditEvent(r, domain.AuditLog{
		OrgID: user.OrgID, ActorType: "user", ActorID: user.ID.String(),
		Action: "auth.password_reset", TargetType: "user", TargetID: user.ID.String(),
	})
	// All sessions are revoked server-side; the user signs in with the new password.
	writeJSON(w, 200, map[string]any{"reset": true})
}

// sendVerificationEmail kicks off the verify flow for a fresh signup (async,
// best-effort: a failed send never breaks registration; the user can resend).
func (a *API) sendVerificationEmail(user domain.User) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		raw, fresh, err := a.identity.RequestEmailVerification(ctx, user.ID)
		if err != nil {
			return // already verified (e.g. Google) or transient — resend is self-service
		}
		a.dispatchAuthEmail(email.BuildVerifyEmail(fresh.Email, a.appBaseURL+"/verify-email?token="+raw))
	}()
}

// dispatchAuthEmail fire-and-forgets a transactional auth email (console in dev).
func (a *API) dispatchAuthEmail(msg email.Message) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if a.email != nil {
			if err := a.email.Send(ctx, msg); err != nil {
				a.logger().Warn("auth email send failed", "to", msg.To, "subject", msg.Subject, "err", err)
			}
		}
	}()
}
