-- +goose Up
-- Enterprise B2B additions: departments (org structure), a real template registry,
-- and workload/user linkage. No marketplace/provider concepts — this is a private
-- company cloud: departments own workloads, templates define environments.

-- ── Departments ───────────────────────────────────────────────────────────────
-- +goose StatementBegin
CREATE TABLE departments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);
CREATE INDEX idx_departments_org ON departments (org_id);
-- +goose StatementEnd

-- ── Template registry ─────────────────────────────────────────────────────────
-- org_id NULL = built-in template available to every org. software/tags are jsonb.
-- +goose StatementBegin
CREATE TABLE templates (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                 uuid REFERENCES organizations(id) ON DELETE CASCADE,
    name                   text NOT NULL,
    description            text NOT NULL DEFAULT '',
    base_image             text NOT NULL,
    software               jsonb NOT NULL DEFAULT '[]'::jsonb,  -- [{"name":"PyTorch","version":"2.1.0"}]
    version                text NOT NULL DEFAULT '',
    tags                   jsonb NOT NULL DEFAULT '[]'::jsonb,  -- ["cuda","pytorch"]
    default_expose_ssh     boolean NOT NULL DEFAULT true,
    default_expose_jupyter boolean NOT NULL DEFAULT false,
    enabled                boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_templates_org ON templates (org_id);
-- built-in templates (org_id NULL) are unique by name; per-org templates unique per org
CREATE UNIQUE INDEX idx_templates_global_name ON templates (name) WHERE org_id IS NULL;
CREATE UNIQUE INDEX idx_templates_org_name ON templates (org_id, name) WHERE org_id IS NOT NULL;
-- +goose StatementEnd

-- ── Linkage ───────────────────────────────────────────────────────────────────
-- +goose StatementBegin
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS department_id uuid REFERENCES departments(id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workloads
  ADD COLUMN IF NOT EXISTS department_id uuid REFERENCES departments(id),
  ADD COLUMN IF NOT EXISTS template_id   uuid REFERENCES templates(id),
  ADD COLUMN IF NOT EXISTS container_id  text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_workloads_department ON workloads (department_id);
-- +goose StatementEnd

-- ── Enrollment token lifecycle (revoke + usage tracking) ──────────────────────
-- +goose StatementBegin
ALTER TABLE enrollment_tokens
  ADD COLUMN IF NOT EXISTS description  text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS revoked_at   timestamptz,
  ADD COLUMN IF NOT EXISTS last_used_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE enrollment_tokens
  DROP COLUMN IF EXISTS description,
  DROP COLUMN IF EXISTS revoked_at,
  DROP COLUMN IF EXISTS last_used_at;
ALTER TABLE workloads
  DROP COLUMN IF EXISTS department_id,
  DROP COLUMN IF EXISTS template_id,
  DROP COLUMN IF EXISTS container_id;
ALTER TABLE users
  DROP COLUMN IF EXISTS department_id;
DROP TABLE IF EXISTS templates;
DROP TABLE IF EXISTS departments;
-- +goose StatementEnd
