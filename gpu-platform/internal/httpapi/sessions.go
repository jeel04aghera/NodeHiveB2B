package httpapi

import (
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/identity"
)

// refreshCookie is the HttpOnly cookie carrying the raw refresh token. It is scoped to
// /api/v1/auth so it is sent only to the refresh + logout endpoints (never to the rest
// of the API), limiting its exposure surface.
const refreshCookie = "nh_refresh"
const refreshCookiePath = "/api/v1/auth"

// setRefreshCookie writes the refresh token cookie with attributes matching the topology:
// cross-origin production → Secure + SameSite=None; local dev → Lax. HttpOnly always.
func (a *API) setRefreshCookie(w http.ResponseWriter, raw string, ttl time.Duration) {
	c := &http.Cookie{
		Name: refreshCookie, Value: raw, Path: refreshCookiePath, HttpOnly: true,
		MaxAge: int(ttl.Seconds()), Expires: time.Now().Add(ttl),
	}
	if a.secureCookies {
		c.Secure = true
		c.SameSite = http.SameSiteNoneMode
	} else {
		c.SameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, c)
}

func (a *API) clearRefreshCookie(w http.ResponseWriter) {
	c := &http.Cookie{Name: refreshCookie, Value: "", Path: refreshCookiePath, HttpOnly: true, MaxAge: -1}
	if a.secureCookies {
		c.Secure = true
		c.SameSite = http.SameSiteNoneMode
	} else {
		c.SameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, c)
}

func readRefreshCookie(r *http.Request) string {
	c, err := r.Cookie(refreshCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// deviceFromRequest extracts device-tracking context from the request. IP comes from
// middleware.RealIP (which normalizes X-Forwarded-For); browser/OS are derived from the UA.
func deviceFromRequest(r *http.Request) identity.DeviceInfo {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	return identity.DeviceInfo{
		UserAgent: r.UserAgent(),
		IPAddress: ip,
	}
}

// startSession creates a refresh session for a freshly-authenticated user and sets the
// refresh cookie. Best-effort: a session failure must not break the login response (the
// access token still works), so we log nothing here and let the caller proceed.
func (a *API) startSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	raw, _, err := a.identity.CreateSession(r.Context(), userID, deviceFromRequest(r))
	if err != nil {
		return
	}
	a.setRefreshCookie(w, raw, a.refreshTTL())
}

// refreshTTL is the cookie lifetime for the refresh token. Mirrors the service default
// (30 days) unless overridden via WithRefreshCookieTTL.
func (a *API) refreshTTL() time.Duration {
	if a.refreshCookieTTL > 0 {
		return a.refreshCookieTTL
	}
	return 30 * 24 * time.Hour
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// refresh rotates the refresh token and returns a new short-lived access token. Public
// (no Bearer required) but gated by the HttpOnly refresh cookie.
func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	raw := readRefreshCookie(r)
	if raw == "" {
		writeErr(w, 401, "unauthorized", "no refresh session")
		return
	}
	access, newRaw, user, _, err := a.identity.RefreshSession(r.Context(), raw, deviceFromRequest(r))
	if err != nil {
		// Invalidate the (now useless) cookie so the client stops retrying with it.
		a.clearRefreshCookie(w)
		writeErr(w, 401, "unauthorized", "session expired, please sign in again")
		return
	}
	a.setRefreshCookie(w, newRaw, a.refreshTTL())
	writeJSON(w, 200, map[string]any{"token": access, "user": userResponse(user)})
}

// logout revokes the current session and clears the refresh cookie. Idempotent.
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if raw := readRefreshCookie(r); raw != "" {
		_ = a.identity.RevokeSessionByRefresh(r.Context(), raw)
	}
	a.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// listSessions returns the caller's active sessions, marking the current one.
func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	sessions, err := a.identity.ListSessions(r.Context(), u.ID)
	if err != nil {
		writeErr(w, 500, "internal", "could not list sessions")
		return
	}
	if currentID, err := a.identity.SessionIDByRefresh(r.Context(), readRefreshCookie(r)); err == nil {
		for i := range sessions {
			if sessions[i].ID == currentID {
				sessions[i].Current = true
			}
		}
	}
	writeJSON(w, 200, sessions)
}

// revokeSession revokes one of the caller's sessions by id (cannot touch others').
func (a *API) revokeSession(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, 400, "validation", "invalid session id")
		return
	}
	if err := a.identity.RevokeSession(r.Context(), u.ID, id); err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			writeErr(w, 404, "not_found", "session not found")
			return
		}
		writeErr(w, 500, "internal", "could not revoke session")
		return
	}
	// If the caller revoked their own current session, drop the cookie too.
	if currentID, err := a.identity.SessionIDByRefresh(r.Context(), readRefreshCookie(r)); err == nil && currentID == id {
		a.clearRefreshCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeAllSessions revokes every session for the caller EXCEPT their current one (so
// they stay signed in here while logging out every other device).
func (a *API) revokeAllSessions(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var except *uuid.UUID
	if currentID, err := a.identity.SessionIDByRefresh(r.Context(), readRefreshCookie(r)); err == nil {
		except = &currentID
	}
	if err := a.identity.RevokeAllSessions(r.Context(), u.ID, except); err != nil {
		writeErr(w, 500, "internal", "could not revoke sessions")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
