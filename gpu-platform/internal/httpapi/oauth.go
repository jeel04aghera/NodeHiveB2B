package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

const oauthStateCookie = "nh_oauth_state"

// googleStart begins the Google authorization-code flow: it sets a CSRF state cookie
// and redirects the browser to Google's consent screen.
func (a *API) googleStart(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		writeErr(w, 404, "not_found", "Google sign-in is not enabled")
		return
	}
	state := randomState()
	a.setCookie(w, oauthStateCookie, state, 10*time.Minute)
	http.Redirect(w, r, a.google.AuthCodeURL(state), http.StatusFound)
}

// googleCallback validates state, exchanges the code for a verified profile, upserts
// the user, and redirects back to the frontend with a session token.
func (a *API) googleCallback(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		writeErr(w, 404, "not_found", "Google sign-in is not enabled")
		return
	}
	// CSRF: the state in the URL must match the one we set in the cookie.
	st := r.URL.Query().Get("state")
	c, err := r.Cookie(oauthStateCookie)
	if err != nil || st == "" || c.Value != st {
		a.oauthFail(w, r, "invalid_state")
		return
	}
	a.clearCookie(w, oauthStateCookie)

	code := r.URL.Query().Get("code")
	if code == "" {
		a.oauthFail(w, r, "missing_code")
		return
	}
	profile, err := a.google.Exchange(r.Context(), code)
	if err != nil {
		a.oauthFail(w, r, "exchange_failed")
		return
	}
	token, _, err := a.identity.UpsertGoogleUser(r.Context(),
		profile.Sub, profile.Email, profile.Name, profile.Picture, profile.EmailVerified)
	if err != nil {
		a.oauthFail(w, r, "account_error")
		return
	}
	// Deliver the token to the SPA. Cross-origin: hand it back in the URL fragment
	// (never sent to servers/logs); the SPA reads it and decides onboarding vs dashboard.
	if a.appBaseURL != "" {
		http.Redirect(w, r, a.appBaseURL+"/auth/callback#token="+token, http.StatusFound)
		return
	}
	writeJSON(w, 200, map[string]any{"token": token})
}

func (a *API) oauthFail(w http.ResponseWriter, r *http.Request, reason string) {
	if a.appBaseURL != "" {
		http.Redirect(w, r, a.appBaseURL+"/login?error="+reason, http.StatusFound)
		return
	}
	writeErr(w, 400, "oauth_error", reason)
}

// onboardingCreateOrg lets a pre-onboarding user create their organization and become
// its admin. Reachable by authenticated users who have no org yet.
func (a *API) onboardingCreateOrg(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if u.Onboarded() {
		writeErr(w, 409, "already_onboarded", "you already belong to an organization")
		return
	}
	var body struct {
		OrgName string `json:"org_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OrgName == "" {
		writeErr(w, 400, "validation", "org_name is required")
		return
	}
	token, user, err := a.identity.CreateOrgForUser(r.Context(), u.ID, body.OrgName)
	if err != nil {
		writeErr(w, 400, "validation", err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"token": token, "user": userResponse(user)})
}

// setCookie writes a cookie with attributes matching the deployment topology:
// cross-origin production → Secure + SameSite=None; local dev → Lax.
func (a *API) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	c := &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true,
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

func (a *API) clearCookie(w http.ResponseWriter, name string) {
	c := &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, MaxAge: -1}
	if a.secureCookies {
		c.Secure = true
		c.SameSite = http.SameSiteNoneMode
	} else {
		c.SameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, c)
}

func randomState() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
