package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/identity"
)

// requireRole gates a route on a minimum org role (owner > admin > member). Used on top of
// requireOrg, so the user is guaranteed to belong to an org by the time this runs.
func (a *API) requireRole(min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !userFromCtx(r).Role.AtLeast(min) {
				writeErr(w, 403, "forbidden", "your role does not permit this action")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeMembershipErr maps identity errors to HTTP statuses for the org-management endpoints.
func writeMembershipErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeErr(w, 403, "forbidden", err.Error())
	case errors.Is(err, identity.ErrLastOwner):
		writeErr(w, 409, "last_owner", err.Error())
	case errors.Is(err, identity.ErrAlreadyMember):
		writeErr(w, 409, "already_member", err.Error())
	case errors.Is(err, identity.ErrNotFound):
		writeErr(w, 404, "not_found", "not found")
	case errors.Is(err, identity.ErrInvitationInvalid):
		writeErr(w, 400, "invalid_invitation", err.Error())
	case errors.Is(err, identity.ErrJoinCodeInvalid):
		writeErr(w, 400, "invalid_join_code", err.Error())
	case errors.Is(err, identity.ErrUserExists):
		writeErr(w, 409, "conflict", "an account with that email already exists")
	default:
		writeErr(w, 500, "internal", "operation failed")
	}
}

// ── Members ────────────────────────────────────────────────────────────────────

func (a *API) listMembers(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	members, err := a.identity.ListMembers(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list members")
		return
	}
	writeJSON(w, 200, members)
}

func (a *API) changeMemberRole(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	target, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeErr(w, 400, "validation", "invalid user id")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	if err := a.identity.ChangeMemberRole(r.Context(), u.OrgID, u.ID, u.Role, target, domain.Role(body.Role)); err != nil {
		writeMembershipErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) removeMember(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	target, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		writeErr(w, 400, "validation", "invalid user id")
		return
	}
	if err := a.identity.RemoveMember(r.Context(), u.OrgID, u.ID, u.Role, target); err != nil {
		writeMembershipErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Invitations ─────────────────────────────────────────────────────────────────

func (a *API) listInvitations(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	invites, err := a.identity.ListInvitations(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list invitations")
		return
	}
	writeJSON(w, 200, invites)
}

func (a *API) createInvitation(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeErr(w, 400, "validation", "email is required")
		return
	}
	raw, inv, err := a.identity.CreateInvitation(r.Context(), u.OrgID, u.ID, body.Email, domain.Role(body.Role))
	if err != nil {
		writeMembershipErr(w, err)
		return
	}
	// No email infrastructure yet: return the shareable invite token (like enrollment
	// tokens) so the admin can send the accept link. The link is APP_BASE_URL/invite?token=…
	writeJSON(w, 201, map[string]any{"invitation": inv, "token": raw, "accept_url": a.inviteURL(raw)})
}

func (a *API) resendInvitation(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 400, "validation", "invalid invitation id")
		return
	}
	raw, inv, err := a.identity.ResendInvitation(r.Context(), u.OrgID, id)
	if err != nil {
		writeMembershipErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"invitation": inv, "token": raw, "accept_url": a.inviteURL(raw)})
}

func (a *API) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 400, "validation", "invalid invitation id")
		return
	}
	if err := a.identity.RevokeInvitation(r.Context(), u.OrgID, id); err != nil {
		writeMembershipErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) inviteURL(token string) string {
	if a.appBaseURL == "" {
		return ""
	}
	return a.appBaseURL + "/invite?token=" + token
}

// previewInvitation is public — it lets the accept page show who invited whom before login.
func (a *API) previewInvitation(w http.ResponseWriter, r *http.Request) {
	inv, orgName, err := a.identity.InvitationByToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeMembershipErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"org_name": orgName, "email": inv.Email, "role": inv.Role, "status": inv.Status,
	})
}

// acceptInvitation is reachable by authenticated pre-onboarding users (alongside onboarding).
func (a *API) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		writeErr(w, 400, "validation", "token is required")
		return
	}
	token, user, err := a.identity.AcceptInvitation(r.Context(), u.ID, body.Token)
	if err != nil {
		writeMembershipErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "user": userResponse(user)})
}

// ── Join codes ──────────────────────────────────────────────────────────────────

func (a *API) listJoinCodes(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	codes, err := a.identity.ListJoinCodes(r.Context(), u.OrgID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list join codes")
		return
	}
	writeJSON(w, 200, codes)
}

func (a *API) createJoinCode(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Description string `json:"description"`
		TTLDays     int    `json:"ttl_days"`
		MaxUses     int    `json:"max_uses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	ttl := time.Duration(body.TTLDays) * 24 * time.Hour
	raw, err := a.identity.CreateJoinCode(r.Context(), u.OrgID, u.ID, body.Description, ttl, body.MaxUses)
	if err != nil {
		writeErr(w, 500, "internal", "could not create join code")
		return
	}
	writeJSON(w, 201, map[string]any{"code": raw})
}

func (a *API) revokeJoinCode(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 400, "validation", "invalid join code id")
		return
	}
	if err := a.identity.RevokeJoinCode(r.Context(), u.OrgID, id); err != nil {
		writeMembershipErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// joinViaCode is reachable by authenticated pre-onboarding users.
func (a *API) joinViaCode(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		writeErr(w, 400, "validation", "code is required")
		return
	}
	token, user, err := a.identity.JoinViaCode(r.Context(), u.ID, body.Code)
	if err != nil {
		writeMembershipErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "user": userResponse(user)})
}

// ── Pending registration (invite signup, no org) ────────────────────────────────

func (a *API) registerPending(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "validation", "invalid JSON")
		return
	}
	token, user, err := a.identity.RegisterPending(r.Context(), body.Email, body.Name, body.Password)
	if errors.Is(err, identity.ErrUserExists) {
		writeErr(w, 409, "conflict", "an account with that email already exists")
		return
	}
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	a.startSession(w, r, user.ID) // additive: also sets the refresh cookie
	writeJSON(w, 201, map[string]any{"token": token, "user": userResponse(user)})
}
