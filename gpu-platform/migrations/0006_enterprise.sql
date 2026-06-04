-- +goose Up
-- +goose StatementBegin
-- F1/F2: workload lifecycle events + current stage for progress tracking.
CREATE TABLE workload_events (
    id          bigserial PRIMARY KEY,
    workload_id uuid NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    stage       text NOT NULL,
    message     text NOT NULL DEFAULT '',
    ts          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_workload_events ON workload_events (workload_id, ts);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS stage text NOT NULL DEFAULT '';
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS queued_at timestamptz;
-- backfill stage from status for existing rows
UPDATE workloads SET stage = CASE
  WHEN status='running' THEN 'ready'
  WHEN status='stopped' THEN 'stopped'
  WHEN status='failed' THEN 'failed'
  ELSE 'preparing' END;
-- +goose StatementEnd

-- +goose StatementBegin
-- F3: allow 'queued' status.
ALTER TABLE workloads DROP CONSTRAINT IF EXISTS workloads_status_check;
ALTER TABLE workloads ADD CONSTRAINT workloads_status_check
  CHECK (status IN ('queued','pending','running','stopping','stopped','failed'));
-- +goose StatementEnd

-- +goose StatementBegin
-- F4: capacity reservations.
CREATE TABLE reservations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id uuid REFERENCES projects(id) ON DELETE SET NULL,
    user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    gpu_model  text NOT NULL,
    gpu_count  int  NOT NULL CHECK (gpu_count > 0),
    start_at   timestamptz NOT NULL,
    end_at     timestamptz NOT NULL,
    status     text NOT NULL DEFAULT 'upcoming' CHECK (status IN ('upcoming','active','expired','cancelled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (end_at > start_at)
);
CREATE INDEX idx_reservations_org ON reservations (org_id, start_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- F6: budgets (organization / department / project), monthly.
CREATE TABLE budgets (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    scope_type text NOT NULL CHECK (scope_type IN ('organization','department','project')),
    scope_id   uuid,  -- NULL for organization scope
    amount     numeric(14,2) NOT NULL CHECK (amount >= 0),  -- INR
    period     text NOT NULL DEFAULT 'monthly' CHECK (period IN ('monthly')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_budgets_scope ON budgets (org_id, scope_type, coalesce(scope_id, '00000000-0000-0000-0000-000000000000'::uuid));
-- +goose StatementEnd

-- +goose StatementBegin
-- F5: cost alert rules + raised alerts.
CREATE TABLE alert_rules (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    type       text NOT NULL CHECK (type IN ('project_spend','department_spend','workload_runtime','idle_workload','budget_utilization')),
    threshold  numeric NOT NULL,
    scope_id   uuid,
    severity   text NOT NULL DEFAULT 'warning' CHECK (severity IN ('info','warning','critical')),
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alerts (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    rule_id    uuid REFERENCES alert_rules(id) ON DELETE SET NULL,
    severity   text NOT NULL CHECK (severity IN ('info','warning','critical')),
    title      text NOT NULL,
    message    text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active','acknowledged')),
    dedup_key  text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_alerts_org ON alerts (org_id, created_at DESC);
CREATE UNIQUE INDEX idx_alerts_dedup ON alerts (org_id, dedup_key) WHERE status='active' AND dedup_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS alert_rules;
DROP TABLE IF EXISTS budgets;
DROP TABLE IF EXISTS reservations;
ALTER TABLE workloads DROP CONSTRAINT IF EXISTS workloads_status_check;
ALTER TABLE workloads ADD CONSTRAINT workloads_status_check CHECK (status IN ('pending','running','stopping','stopped','failed'));
ALTER TABLE workloads DROP COLUMN IF EXISTS stage;
ALTER TABLE workloads DROP COLUMN IF EXISTS queued_at;
DROP TABLE IF EXISTS workload_events;
-- +goose StatementEnd
