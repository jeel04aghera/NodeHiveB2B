package config

import (
	"testing"
	"time"
)

func base() Config {
	return Config{
		DB:   DBConfig{URL: "postgres://x"},
		HTTP: HTTPConfig{Addr: ":8080"},
		GRPC: GRPCConfig{Addr: ":9090", Insecure: true},
		Auth: AuthConfig{JWTSecret: "a-strong-secret-of-some-length"},
	}
}

// S2: production must reject a missing or placeholder JWT secret, regardless of
// GRPC_INSECURE (Railway runs insecure gRPC).
func TestValidate_JWTSecret(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		secret  string
		wantErr bool
	}{
		{"prod empty secret", "production", "", true},
		{"prod placeholder dev-only", "production", "dev-only-change-me", true},
		{"prod placeholder change-me-in-production", "production", "change-me-in-production", true},
		{"prod short secret", "production", "tooshort", true},
		{"prod strong secret", "production", "8f3c9b1d2e4a6c7f0b5d8e1a2c4f6b8d", false},
		{"default env (unset) is production -> placeholder rejected", "", "dev-only-change-me", true},
		{"dev allows weak secret", "development", "dev-only-change-me", false},
		{"dev allows empty secret", "development", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			c.Env = tc.env
			c.Auth.JWTSecret = tc.secret
			err := c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// Phase 2: access-token TTL falls back to SESSION_TTL when ACCESS_TOKEN_TTL is unset
// (so existing deploys aren't silently changed); ACCESS_TOKEN_TTL overrides it when set.
// REFRESH_TOKEN_TTL defaults to 30 days.
func TestAuthTTLResolution(t *testing.T) {
	t.Run("defaults: access falls back to SESSION_TTL default (24h), refresh 30d", func(t *testing.T) {
		a := newAuthConfig() // no env set
		if a.SessionTTL != 24*time.Hour {
			t.Errorf("SessionTTL=%v want 24h", a.SessionTTL)
		}
		if a.AccessTokenTTL != a.SessionTTL {
			t.Errorf("AccessTokenTTL=%v should fall back to SessionTTL=%v", a.AccessTokenTTL, a.SessionTTL)
		}
		if a.RefreshTokenTTL != 30*24*time.Hour {
			t.Errorf("RefreshTokenTTL=%v want 30d", a.RefreshTokenTTL)
		}
	})

	t.Run("ACCESS_TOKEN_TTL overrides; SESSION_TTL still independent", func(t *testing.T) {
		t.Setenv("SESSION_TTL", "12h")
		t.Setenv("ACCESS_TOKEN_TTL", "15m")
		t.Setenv("REFRESH_TOKEN_TTL", "720h")
		a := newAuthConfig()
		if a.SessionTTL != 12*time.Hour {
			t.Errorf("SessionTTL=%v want 12h", a.SessionTTL)
		}
		if a.AccessTokenTTL != 15*time.Minute {
			t.Errorf("AccessTokenTTL=%v want 15m", a.AccessTokenTTL)
		}
		if a.RefreshTokenTTL != 720*time.Hour {
			t.Errorf("RefreshTokenTTL=%v want 720h", a.RefreshTokenTTL)
		}
	})

	t.Run("only SESSION_TTL set -> access inherits it (backward compat)", func(t *testing.T) {
		t.Setenv("SESSION_TTL", "8h")
		a := newAuthConfig()
		if a.AccessTokenTTL != 8*time.Hour {
			t.Errorf("AccessTokenTTL=%v should inherit SESSION_TTL=8h", a.AccessTokenTTL)
		}
	})
}

// S3: dev bootstrap must be off unless ENV=development. main.go gates the DEV_*
// seeding on IsDev(), so this is the switch that prevents prod backdoor credentials.
func TestIsDev(t *testing.T) {
	cases := map[string]bool{
		"":            false, // default/unset == production
		"production":  false,
		"prod":        false,
		"staging":     false,
		"development": true,
		"dev":         true,
	}
	for env, want := range cases {
		if got := (Config{Env: env}).IsDev(); got != want {
			t.Errorf("IsDev(%q)=%v, want %v", env, got, want)
		}
	}
}
