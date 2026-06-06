// Package config loads and validates configuration for the control plane and the
// agent from the environment (see .env.example). No framework — env vars + defaults.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---- Control plane ----

type DBConfig struct{ URL string }
type HTTPConfig struct{ Addr string }
type GRPCConfig struct {
	Addr     string
	Insecure bool   // dev only: serve gRPC without TLS
	CertFile string // TLS server certificate (PEM)
	KeyFile  string // TLS private key (PEM)
}
type AuthConfig struct {
	JWTSecret  string
	SessionTTL time.Duration // legacy single-token TTL; also the access-token fallback
	// AccessTokenTTL is the short-lived Bearer access-token lifetime. Defaults to
	// SessionTTL when ACCESS_TOKEN_TTL is unset, so existing deploys aren't silently
	// changed; set ACCESS_TOKEN_TTL=15m for the recommended short-lived posture.
	AccessTokenTTL time.Duration
	// RefreshTokenTTL is the long-lived refresh-token (session) lifetime.
	RefreshTokenTTL time.Duration
}

// GoogleOAuthConfig holds the credentials for "Sign in with Google". OAuth is enabled
// only when ClientID, ClientSecret and RedirectURL are all set.
type GoogleOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string // API callback, e.g. https://<api>/api/v1/auth/google/callback
}

func (g GoogleOAuthConfig) Enabled() bool {
	return g.ClientID != "" && g.ClientSecret != "" && g.RedirectURL != ""
}

// EmailConfig holds transactional-email (Resend) settings for invitation delivery. Email
// is enabled only when both the API key and from-address are set; otherwise a console
// fallback logs the email and the raw invite URL is still returned (dev behavior).
type EmailConfig struct {
	ResendAPIKey string
	FromEmail    string
}

func (e EmailConfig) Enabled() bool { return e.ResendAPIKey != "" && e.FromEmail != "" }

type Config struct {
	Env                string // "development" or "production" (default). Gates dev bootstrap + secret checks.
	DB                 DBConfig
	HTTP               HTTPConfig
	GRPC               GRPCConfig
	Auth               AuthConfig
	Google             GoogleOAuthConfig
	Email              EmailConfig
	AppBaseURL         string   // frontend origin the OAuth callback redirects back to
	CORSAllowedOrigins []string // explicit allow-list; empty => permissive "*" (dev only, no credentials)
	DevOrgSlug         string   // org that dev nodes enroll into (matches scripts/seed_dev.sql)
	DevEnrollmentToken string   // if set AND dev mode, control plane seeds this token on startup
	DevBootstrapAdmin  string   // email:password to seed on first run (dev mode only)
}

func Load() (Config, error) {
	c := Config{
		// Default to production so a deploy that forgets to set ENV fails CLOSED:
		// no dev credentials are seeded and a weak/placeholder JWT_SECRET is rejected.
		Env:  strings.ToLower(env("ENV", "production")),
		DB:   DBConfig{URL: env("DATABASE_URL", "postgres://gpu:gpu@localhost:5432/gpu?sslmode=disable")},
		HTTP: HTTPConfig{Addr: env("HTTP_ADDR", ":8080")},
		GRPC: GRPCConfig{
			Addr:     env("GRPC_ADDR", ":9090"),
			Insecure: envBool("GRPC_INSECURE", true),
			CertFile: env("GRPC_CERT_FILE", ""),
			KeyFile:  env("GRPC_KEY_FILE", ""),
		},
		Auth: newAuthConfig(),
		Google: GoogleOAuthConfig{
			ClientID:     env("GOOGLE_CLIENT_ID", ""),
			ClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
			RedirectURL:  env("GOOGLE_REDIRECT_URL", ""),
		},
		Email: EmailConfig{
			ResendAPIKey: env("RESEND_API_KEY", ""),
			FromEmail:    env("INVITE_FROM_EMAIL", ""),
		},
		AppBaseURL:         strings.TrimRight(env("APP_BASE_URL", ""), "/"),
		CORSAllowedOrigins: splitNonEmpty(env("CORS_ALLOWED_ORIGINS", "")),
		DevOrgSlug:         env("DEV_ORG_SLUG", "dev"),
		DevEnrollmentToken: env("DEV_ENROLLMENT_TOKEN", "dev-enroll-token"),
		DevBootstrapAdmin:  env("DEV_BOOTSTRAP_ADMIN", "admin@dev.local:admin123"),
	}
	return c, c.Validate()
}

// newAuthConfig resolves the auth/session TTLs. SESSION_TTL stays the legacy single-token
// lifetime and the access-token fallback; ACCESS_TOKEN_TTL (recommended 15m) overrides the
// access-token lifetime when set; REFRESH_TOKEN_TTL governs refresh sessions (default 30d).
func newAuthConfig() AuthConfig {
	sessionTTL := envDur("SESSION_TTL", 24*time.Hour)
	return AuthConfig{
		JWTSecret:       env("JWT_SECRET", "dev-only-change-me"),
		SessionTTL:      sessionTTL,
		AccessTokenTTL:  envDur("ACCESS_TOKEN_TTL", sessionTTL),
		RefreshTokenTTL: envDur("REFRESH_TOKEN_TTL", 30*24*time.Hour),
	}
}

// IsDev reports whether the control plane runs in development mode. ONLY in dev are
// the DEV_* bootstrap admin/token seeded and the JWT secret allowed to be weak.
func (c Config) IsDev() bool {
	return c.Env == "development" || c.Env == "dev"
}

// weakJWTSecrets are placeholder values that must never protect a real deployment.
var weakJWTSecrets = map[string]bool{
	"":                        true,
	"dev-only-change-me":      true,
	"change-me-in-production": true,
	"change-me":               true,
	"changeme":                true,
	"secret":                  true,
	"password":                true,
}

func (c Config) Validate() error {
	if c.DB.URL == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if c.HTTP.Addr == "" || c.GRPC.Addr == "" {
		return fmt.Errorf("config: HTTP_ADDR and GRPC_ADDR are required")
	}
	// Production must run with a strong, explicitly-set JWT secret. Empty or any known
	// placeholder is rejected at startup (fail closed) so tokens can't be forged with a
	// public secret. Independent of GRPC_INSECURE (Railway runs insecure gRPC).
	if !c.IsDev() {
		if weakJWTSecrets[c.Auth.JWTSecret] {
			return fmt.Errorf("config: JWT_SECRET is missing or a known placeholder; set a strong secret (ENV=development to bypass for local dev)")
		}
		if len(c.Auth.JWTSecret) < 16 {
			return fmt.Errorf("config: JWT_SECRET must be at least 16 characters in production")
		}
	}
	return nil
}

// ---- Agent ----

type AgentConfig struct {
	ServerAddr        string
	EnrollmentToken   string
	CredentialPath    string
	Insecure          bool
	CACertFile        string // pin the server CA (empty = use system roots)
	DevMode           bool   // synthetic GPUs/metrics (no NVIDIA). Workloads still run as real Docker containers.
	GPUPassthrough    bool   // pass --gpus to docker run (disable on machines without NVIDIA)
	AdvertiseHost     string // host:port advertised in SSH/Jupyter endpoints (empty = hostname)
	HeartbeatInterval time.Duration
}

func LoadAgent() (AgentConfig, error) {
	c := AgentConfig{
		ServerAddr:        env("AGENT_SERVER_URL", "localhost:9090"),
		EnrollmentToken:   env("AGENT_ENROLLMENT_TOKEN", ""),
		CredentialPath:    env("AGENT_CREDENTIAL_PATH", defaultCredPath()),
		Insecure:          envBool("AGENT_INSECURE", true),
		CACertFile:        env("AGENT_CA_CERT", ""),
		DevMode:           envBool("AGENT_DEV_MODE", false),
		GPUPassthrough:    envBool("AGENT_GPU_PASSTHROUGH", true),
		AdvertiseHost:     env("AGENT_ADVERTISE_HOST", ""),
		HeartbeatInterval: envDur("HEARTBEAT_INTERVAL", 30*time.Second),
	}
	return c, c.Validate()
}

func (c AgentConfig) Validate() error {
	if c.ServerAddr == "" {
		return fmt.Errorf("agent config: AGENT_SERVER_URL is required")
	}
	if c.CredentialPath == "" {
		return fmt.Errorf("agent config: AGENT_CREDENTIAL_PATH is required")
	}
	if c.HeartbeatInterval <= 0 {
		return fmt.Errorf("agent config: HEARTBEAT_INTERVAL must be > 0")
	}
	return nil
}

func defaultCredPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home + "/.gpu-agent/credential.json"
	}
	return "./gpu-agent-credential.json"
}

// ---- helpers ----

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// splitNonEmpty splits a comma-separated list, trimming spaces and dropping blanks.
func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}
