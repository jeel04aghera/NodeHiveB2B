package config

import "testing"

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
