-- +goose Up
-- +goose StatementBegin
-- V1 initial schema. Single Postgres. Every table carries org_id from day one so
-- multi-org is a later config change, not a schema rewrite. gen_random_uuid() is
-- built into PostgreSQL 13+; citext is used for case-insensitive emails.
CREATE EXTENSION IF NOT EXISTS citext;
-- +goose StatementEnd

-- ── Identity ────────────────────────────────────────────────────────────────
-- +goose StatementBegin
CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    settings    jsonb NOT NULL DEFAULT '{}'::jsonb, -- idle_threshold_pct, idle_duration_min, currency, default_rate
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email         citext NOT NULL,
    password_hash text NOT NULL,
    name          text NOT NULL DEFAULT '',
    role          text NOT NULL DEFAULT 'user' CHECK (role IN ('admin','user')),
    status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, email)              -- login is scoped to an org
);
CREATE INDEX idx_users_org ON users (org_id);   -- list users in an org
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE projects (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)               -- chargeback cost-center, unique per org
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE enrollment_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,    -- never store the raw token
    created_by uuid REFERENCES users(id),
    expires_at timestamptz NOT NULL,
    max_uses   int NOT NULL DEFAULT 1,
    uses       int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- ── Fleet ───────────────────────────────────────────────────────────────────
-- +goose StatementBegin
CREATE TABLE gpu_nodes (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    fingerprint    text,                 -- stable hardware identity for idempotent re-enrollment
    hostname       text NOT NULL,
    status         text NOT NULL DEFAULT 'offline' CHECK (status IN ('online','offline','degraded')),
    os             text NOT NULL DEFAULT '',
    kernel         text NOT NULL DEFAULT '',
    cpu_model      text NOT NULL DEFAULT '',
    cpu_cores      int  NOT NULL DEFAULT 0,
    ram_mb         bigint NOT NULL DEFAULT 0,
    nvidia_driver  text NOT NULL DEFAULT '',
    cuda_version   text NOT NULL DEFAULT '',
    agent_version  text NOT NULL DEFAULT '',
    labels         jsonb NOT NULL DEFAULT '{}'::jsonb,
    enrolled_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz                       -- denormalized fast path for liveness
);
CREATE INDEX idx_nodes_org_status ON gpu_nodes (org_id, status);  -- "show my online nodes"
CREATE INDEX idx_nodes_last_seen  ON gpu_nodes (last_seen_at);    -- offline sweep job
CREATE UNIQUE INDEX idx_nodes_fingerprint ON gpu_nodes (fingerprint); -- idempotent re-enroll (NULLs allowed)
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE agent_credentials (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    node_id     uuid NOT NULL REFERENCES gpu_nodes(id) ON DELETE CASCADE,
    public_key  text NOT NULL UNIQUE,   -- or cert fingerprint
    issued_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
CREATE INDEX idx_agent_creds_node ON agent_credentials (node_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE gpus (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id    uuid NOT NULL REFERENCES gpu_nodes(id) ON DELETE CASCADE,
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, -- denormalized for fleet queries
    gpu_index  int  NOT NULL,
    uuid       text NOT NULL UNIQUE,    -- NVIDIA GPU UUID, stable identity
    model      text NOT NULL,
    memory_mb  bigint NOT NULL DEFAULT 0,
    mig_enabled boolean NOT NULL DEFAULT false,
    status     text NOT NULL DEFAULT 'healthy' CHECK (status IN ('healthy','unhealthy','in_use','idle')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_gpus_node       ON gpus (node_id);             -- node detail page
CREATE INDEX idx_gpus_org_status ON gpus (org_id, status);      -- fleet view / placement
-- +goose StatementEnd

-- ── Workloads ───────────────────────────────────────────────────────────────
-- +goose StatementBegin
CREATE TABLE workloads (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id          uuid REFERENCES projects(id),
    user_id             uuid NOT NULL REFERENCES users(id),       -- launcher
    node_id             uuid REFERENCES gpu_nodes(id),
    name                text NOT NULL,
    image               text NOT NULL,
    requested_gpu_count int  NOT NULL DEFAULT 1,
    status              text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','stopping','stopped','failed')),
    idle_timeout_sec    int,
    ssh_endpoint        text,
    jupyter_endpoint    text,
    started_at          timestamptz,
    stopped_at          timestamptz,
    stop_reason         text CHECK (stop_reason IN ('user','idle_reclaim','admin','failure')),
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_workloads_org_status ON workloads (org_id, status); -- active workloads list
CREATE INDEX idx_workloads_node       ON workloads (node_id);        -- reconcile per node
CREATE INDEX idx_workloads_user       ON workloads (user_id);
CREATE INDEX idx_workloads_project    ON workloads (project_id);     -- chargeback grouping
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE workload_gpus (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workload_id uuid NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    gpu_id      uuid NOT NULL REFERENCES gpus(id),
    attached_at timestamptz NOT NULL DEFAULT now(),
    detached_at timestamptz,
    UNIQUE (workload_id, gpu_id)
);
CREATE INDEX idx_workload_gpus_gpu ON workload_gpus (gpu_id);  -- "what is on this GPU"
-- +goose StatementEnd

-- ── Billing ─────────────────────────────────────────────────────────────────
-- +goose StatementBegin
CREATE TABLE rate_cards (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    gpu_model          text NOT NULL,
    rate_per_gpu_hour  numeric(12,4) NOT NULL,
    currency           text NOT NULL DEFAULT 'USD',
    effective_from     timestamptz NOT NULL DEFAULT now(),
    effective_to       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_rate_cards_lookup ON rate_cards (org_id, gpu_model, effective_from);
-- +goose StatementEnd

-- +goose StatementBegin
-- Immutable, append-only metering truth. INSERT-only in application code.
CREATE TABLE usage_records (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    workload_id  uuid REFERENCES workloads(id),
    gpu_id       uuid REFERENCES gpus(id),
    project_id   uuid REFERENCES projects(id),
    user_id      uuid REFERENCES users(id),
    period_start timestamptz NOT NULL,
    period_end   timestamptz NOT NULL,
    gpu_seconds  bigint NOT NULL,
    avg_util_pct real,
    max_mem_mb   int,
    source       text NOT NULL DEFAULT 'workload' CHECK (source IN ('workload','idle')),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_usage_org_period     ON usage_records (org_id, period_start);
CREATE INDEX idx_usage_workload       ON usage_records (workload_id);
CREATE INDEX idx_usage_project_period ON usage_records (project_id, period_start); -- chargeback rollup
-- +goose StatementEnd

-- +goose StatementBegin
-- Materialized money: usage x rate. Stored so reports don't change when a rate is edited.
CREATE TABLE cost_records (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id            uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    usage_record_id   bigint REFERENCES usage_records(id),
    rate_card_id      uuid REFERENCES rate_cards(id),
    project_id        uuid REFERENCES projects(id),
    user_id           uuid REFERENCES users(id),
    period_start      timestamptz NOT NULL,
    period_end        timestamptz NOT NULL,
    gpu_seconds       bigint NOT NULL,
    rate_per_gpu_hour numeric(12,4) NOT NULL,
    currency          text NOT NULL DEFAULT 'USD',
    amount            numeric(14,4) NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_cost_org_period     ON cost_records (org_id, period_start);
CREATE INDEX idx_cost_project_period ON cost_records (project_id, period_start);
-- +goose StatementEnd

-- ── Telemetry (partitioned, high-volume, short retention) ─────────────────────
-- +goose StatementBegin
-- Raw operational samples powering the utilization dashboard + idle detection.
-- Range-partitioned by ts; a scheduled job adds monthly partitions and drops old.
CREATE TABLE gpu_metrics (
    id           bigint GENERATED ALWAYS AS IDENTITY,
    org_id       uuid NOT NULL,
    gpu_id       uuid NOT NULL,
    node_id      uuid NOT NULL,
    ts           timestamptz NOT NULL,
    util_pct     real,
    mem_used_mb  int,
    power_w      real,
    temp_c       real,
    sm_clock_mhz int,
    ecc_errors   int,
    proc_count   int,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);
CREATE INDEX idx_gpu_metrics_gpu_ts ON gpu_metrics (gpu_id, ts DESC); -- time-series query
CREATE TABLE gpu_metrics_default PARTITION OF gpu_metrics DEFAULT;    -- catch-all until job creates monthly parts
-- +goose StatementEnd

-- +goose StatementBegin
-- Liveness history. gpu_nodes.last_seen_at is the fast path; this is the trail.
CREATE TABLE agent_heartbeats (
    id            bigint GENERATED ALWAYS AS IDENTITY,
    org_id        uuid NOT NULL,
    node_id       uuid NOT NULL,
    ts            timestamptz NOT NULL,
    status        text NOT NULL DEFAULT 'healthy',
    agent_version text NOT NULL DEFAULT '',
    summary       jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);
CREATE INDEX idx_heartbeats_node_ts ON agent_heartbeats (node_id, ts DESC);
CREATE TABLE agent_heartbeats_default PARTITION OF agent_heartbeats DEFAULT;
-- +goose StatementEnd

-- ── Audit ─────────────────────────────────────────────────────────────────────
-- +goose StatementBegin
CREATE TABLE audit_logs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id      uuid NOT NULL,
    actor_type  text NOT NULL CHECK (actor_type IN ('user','agent','system')),
    actor_id    text NOT NULL DEFAULT '',
    action      text NOT NULL,
    target_type text NOT NULL DEFAULT '',
    target_id   text NOT NULL DEFAULT '',
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip          inet,
    ts          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_org_ts     ON audit_logs (org_id, ts DESC);       -- "who did what, recently"
CREATE INDEX idx_audit_actor      ON audit_logs (actor_type, actor_id);  -- per-actor trail
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS agent_heartbeats;
DROP TABLE IF EXISTS gpu_metrics;
DROP TABLE IF EXISTS cost_records;
DROP TABLE IF EXISTS usage_records;
DROP TABLE IF EXISTS rate_cards;
DROP TABLE IF EXISTS workload_gpus;
DROP TABLE IF EXISTS workloads;
DROP TABLE IF EXISTS gpus;
DROP TABLE IF EXISTS agent_credentials;
DROP TABLE IF EXISTS gpu_nodes;
DROP TABLE IF EXISTS enrollment_tokens;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;
-- +goose StatementEnd
