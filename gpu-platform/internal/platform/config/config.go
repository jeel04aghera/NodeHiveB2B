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
	SessionTTL time.Duration
}

type Config struct {
	DB                 DBConfig
	HTTP               HTTPConfig
	GRPC               GRPCConfig
	Auth               AuthConfig
	DevOrgSlug         string // org that dev nodes enroll into (matches scripts/seed_dev.sql)
	DevEnrollmentToken string // if set, control plane seeds this token on startup
	DevBootstrapAdmin  string // email:password to seed on first run
}

func Load() (Config, error) {
	c := Config{
		DB:   DBConfig{URL: env("DATABASE_URL", "postgres://gpu:gpu@localhost:5432/gpu?sslmode=disable")},
		HTTP: HTTPConfig{Addr: env("HTTP_ADDR", ":8080")},
		GRPC: GRPCConfig{
			Addr:     env("GRPC_ADDR", ":9090"),
			Insecure: envBool("GRPC_INSECURE", true),
			CertFile: env("GRPC_CERT_FILE", ""),
			KeyFile:  env("GRPC_KEY_FILE", ""),
		},
		Auth: AuthConfig{JWTSecret: env("JWT_SECRET", "dev-only-change-me"), SessionTTL: envDur("SESSION_TTL", 24*time.Hour)},
		DevOrgSlug:         env("DEV_ORG_SLUG", "dev"),
		DevEnrollmentToken: env("DEV_ENROLLMENT_TOKEN", "dev-enroll-token"),
		DevBootstrapAdmin:  env("DEV_BOOTSTRAP_ADMIN", "admin@dev.local:admin123"),
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.DB.URL == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if c.HTTP.Addr == "" || c.GRPC.Addr == "" {
		return fmt.Errorf("config: HTTP_ADDR and GRPC_ADDR are required")
	}
	if !c.GRPC.Insecure && c.Auth.JWTSecret == "dev-only-change-me" {
		return fmt.Errorf("config: set a real JWT_SECRET outside insecure dev mode")
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
