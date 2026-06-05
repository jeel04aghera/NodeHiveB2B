// Package domain holds the core V1 entities and their enums — the shared
// vocabulary of the control plane. Modules depend on domain, not on each other.
// There is no behavior here beyond invariant-guarding helpers; persistence lives
// in each module's Store, orchestration in each module's Service.
package domain

import (
	"time"

	"github.com/google/uuid"
)

type (
	Role          string
	NodeStatus    string
	GPUStatus     string
	WorkloadState string
	StopReason    string
	UsageSource   string
)

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"

	NodeOnline   NodeStatus = "online"
	NodeOffline  NodeStatus = "offline"
	NodeDegraded NodeStatus = "degraded"

	GPUHealthy   GPUStatus = "healthy"
	GPUUnhealthy GPUStatus = "unhealthy"
	GPUInUse     GPUStatus = "in_use"
	GPUIdle      GPUStatus = "idle"

	WorkloadQueued   WorkloadState = "queued"
	WorkloadPending  WorkloadState = "pending"
	WorkloadRunning  WorkloadState = "running"
	WorkloadStopping WorkloadState = "stopping"
	WorkloadStopped  WorkloadState = "stopped"
	WorkloadFailed   WorkloadState = "failed"

	StopUser        StopReason = "user"
	StopIdleReclaim StopReason = "idle_reclaim"
	StopAdmin       StopReason = "admin"
	StopFailure     StopReason = "failure"

	SourceWorkload UsageSource = "workload"
	SourceIdle     UsageSource = "idle"
)

// OrgSettings is stored as jsonb on organizations.
type OrgSettings struct {
	IdleThresholdPct float64 `json:"idle_threshold_pct"`
	IdleDurationMin  int     `json:"idle_duration_min"`
	Currency         string  `json:"currency"`
	DefaultRate      float64 `json:"default_rate"`
}

type Organization struct {
	ID        uuid.UUID
	Name      string
	Slug      string
	Settings  OrgSettings
	CreatedAt time.Time
}

type User struct {
	ID            uuid.UUID
	OrgID         uuid.UUID // uuid.Nil = pre-onboarding (Google user with no org yet)
	DepartmentID  *uuid.UUID
	Email         string
	PasswordHash  string
	Name          string
	Role          Role
	Status        string
	AvatarURL     string
	EmailVerified bool
	AuthProvider  string // 'password' | 'google'
	LastLoginAt   *time.Time
	CreatedAt     time.Time
}

// Onboarded reports whether the user has joined/created an organization.
func (u User) Onboarded() bool { return u.OrgID != uuid.Nil }

// EnrollmentToken is an agent-enrollment credential record (raw token never stored).
type EnrollmentToken struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	Description string     `json:"description"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	MaxUses     int        `json:"max_uses"`
	Uses        int        `json:"uses"`
	ExpiresAt   time.Time  `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Status      string     `json:"status"` // active | expired | revoked | exhausted (computed)
}

type Project struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Department is the org structure unit. Users and workloads belong to a
// department; chargeback rolls up by department.
type Department struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// TemplateSoftware is one entry in a template's installed-software list.
type TemplateSoftware struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Template defines a launchable environment (base image + what's installed).
// OrgID nil ⇒ a built-in template available to every org.
type Template struct {
	ID                   uuid.UUID          `json:"id"`
	OrgID                *uuid.UUID         `json:"org_id,omitempty"`
	Name                 string             `json:"name"`
	Description          string             `json:"description"`
	BaseImage            string             `json:"base_image"`
	Software             []TemplateSoftware `json:"software"`
	Version              string             `json:"version"`
	Tags                 []string           `json:"tags"`
	DefaultExposeSSH     bool               `json:"default_expose_ssh"`
	DefaultExposeJupyter bool               `json:"default_expose_jupyter"`
	Enabled              bool               `json:"enabled"`
	BuiltIn              bool               `json:"built_in"`
	CreatedAt            time.Time          `json:"created_at"`
}

// Node is the aggregate root for the Fleet context (Node + its GPUs).
type Node struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	Hostname     string
	Status       NodeStatus
	OS           string
	Kernel       string
	CPUModel     string
	CPUCores     int
	RAMMB        int64
	NvidiaDriver string
	CUDAVersion  string
	AgentVersion string
	Labels       map[string]string
	EnrolledAt   time.Time
	LastSeenAt   *time.Time
}

type GPU struct {
	ID         uuid.UUID
	NodeID     uuid.UUID
	OrgID      uuid.UUID
	Index      int
	UUID       string // NVIDIA GPU UUID — stable identity
	Model      string
	MemoryMB   int64
	MIGEnabled bool
	Status     GPUStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Workload is the aggregate root for the Workloads context. It is the only thing
// the platform will ever auto-stop (idle reclaim acts on managed workloads only).
type Workload struct {
	ID                uuid.UUID
	OrgID             uuid.UUID
	ProjectID         *uuid.UUID
	DepartmentID      *uuid.UUID
	TemplateID        *uuid.UUID
	UserID            uuid.UUID
	NodeID            *uuid.UUID
	Name              string
	Image             string // resolved base image that actually runs
	ContainerID       string // docker container ref (nodehive-<id>)
	RequestedGPUCount int
	Status            WorkloadState
	Stage             string // deployment stage for the progress tracker (F2)
	IdleTimeoutSec    *int
	ExposeSSH         bool
	ExposeJupyter     bool
	SSHPassword       string // stored plaintext for V1 self-hosted
	SSHEndpoint       string
	JupyterEndpoint   string
	Logs              string // last ~100 lines of container output
	StartedAt         *time.Time
	StoppedAt         *time.Time
	StopReason        StopReason
	CreatedAt         time.Time
}

// UsageRecord is immutable and append-only — the metering source of truth.
type UsageRecord struct {
	ID          int64
	OrgID       uuid.UUID
	WorkloadID  *uuid.UUID
	GPUID       *uuid.UUID
	ProjectID   *uuid.UUID
	UserID      *uuid.UUID
	PeriodStart time.Time
	PeriodEnd   time.Time
	GPUSeconds  int64
	AvgUtilPct  float32
	MaxMemMB    int32
	Source      UsageSource
	CreatedAt   time.Time
}

type CostRecord struct {
	ID             int64
	OrgID          uuid.UUID
	UsageRecordID  *int64
	RateCardID     *uuid.UUID
	ProjectID      *uuid.UUID
	UserID         *uuid.UUID
	PeriodStart    time.Time
	PeriodEnd      time.Time
	GPUSeconds     int64
	RatePerGPUHour float64
	Currency       string
	Amount         float64
	CreatedAt      time.Time
}

type RateCard struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	GPUModel       string
	RatePerGPUHour float64
	Currency       string
	EffectiveFrom  time.Time
	EffectiveTo    *time.Time
}

type AgentHeartbeat struct {
	ID       int64
	OrgID    uuid.UUID
	NodeID   uuid.UUID
	TS       time.Time
	Status   string
	AgentVer string
	Summary  map[string]any
}

type AuditLog struct {
	ID         int64          `json:"id"`
	OrgID      uuid.UUID      `json:"org_id"`
	ActorType  string         `json:"actor_type"` // user | agent | system
	ActorID    string         `json:"actor_id"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Metadata   map[string]any `json:"metadata"`
	IP         string         `json:"ip"`
	TS         time.Time      `json:"ts"`
}
