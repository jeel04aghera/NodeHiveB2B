package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GoogleProfile is the verified identity returned by Google's userinfo endpoint.
type GoogleProfile struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// GoogleOAuth performs the authorization-code exchange against Google. The exchange
// func is injectable so HTTP handlers can be tested without contacting Google.
type GoogleOAuth struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	exchange     func(ctx context.Context, code string) (GoogleProfile, error)
}

// NewGoogleOAuth wires the real Google exchange. Returns nil if not configured.
func NewGoogleOAuth(clientID, clientSecret, redirectURL string) *GoogleOAuth {
	if clientID == "" || clientSecret == "" || redirectURL == "" {
		return nil
	}
	g := &GoogleOAuth{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL}
	g.exchange = g.realExchange
	return g
}

// AuthCodeURL builds the Google consent URL for the given CSRF state.
func (g *GoogleOAuth) AuthCodeURL(state string) string {
	q := url.Values{
		"client_id":     {g.ClientID},
		"redirect_uri":  {g.RedirectURL},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"access_type":   {"online"},
		"prompt":        {"select_account"},
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + q.Encode()
}

// Exchange turns an authorization code into a verified profile.
func (g *GoogleOAuth) Exchange(ctx context.Context, code string) (GoogleProfile, error) {
	return g.exchange(ctx, code)
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func (g *GoogleOAuth) realExchange(ctx context.Context, code string) (GoogleProfile, error) {
	// 1) Trade the code for tokens (server-to-server, TLS, with the client secret).
	form := url.Values{
		"code":          {code},
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"redirect_uri":  {g.RedirectURL},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return GoogleProfile{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return GoogleProfile{}, fmt.Errorf("google token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GoogleProfile{}, fmt.Errorf("google token exchange: status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		return GoogleProfile{}, fmt.Errorf("google token exchange: invalid response")
	}

	// 2) Fetch the verified profile with the freshly-issued access token.
	ureq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return GoogleProfile{}, err
	}
	ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uresp, err := httpClient.Do(ureq)
	if err != nil {
		return GoogleProfile{}, fmt.Errorf("google userinfo: %w", err)
	}
	defer uresp.Body.Close()
	if uresp.StatusCode != http.StatusOK {
		return GoogleProfile{}, fmt.Errorf("google userinfo: status %d", uresp.StatusCode)
	}
	var p GoogleProfile
	if err := json.NewDecoder(uresp.Body).Decode(&p); err != nil {
		return GoogleProfile{}, fmt.Errorf("google userinfo: decode: %w", err)
	}
	if p.Sub == "" || p.Email == "" {
		return GoogleProfile{}, fmt.Errorf("google userinfo: missing sub/email")
	}
	return p, nil
}
