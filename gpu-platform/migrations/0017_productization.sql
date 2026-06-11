-- 0017_productization.sql — Phase 6: API keys, service accounts, project-level
-- isolation, email verification / password reset tokens.
--
-- Secrets discipline (same as enrollment tokens / refresh sessions / invites):
-- raw API keys and email tokens are NEVER stored — only their SHA-256. The key's
-- prefix is kept for display ("nhk_3f9a…") so users can match a leaked key to a row.

-- +goose Up

-- Service accounts: org-scoped, API-only principals with a fixed role. They can
-- never password-login (no users row); they authenticate exclusively via API keys.
-- Role is capped below owner — a machine identity must not own an organization.
-- +goose StatementBegin
CREATE TABLE service_accounts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    role        text NOT NULL DEFAULT 'member' CHECK (role IN ('admin','member')),
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz,
    UNIQUE (org_id, name)
);
-- +goose StatementEnd

-- API keys: owned by EITHER a user (personal key, acts as that user) OR a service
-- account. Hashed at rest; optional expiry; revocation is a tombstone (the row is
-- kept for the audit trail / last_used forensics).
-- +goose StatementBegin
CREATE TABLE api_keys (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name               text NOT NULL,
    prefix             text NOT NULL,            -- first chars of the raw key, display only
    key_hash           text NOT NULL UNIQUE,     -- sha256(raw)
    user_id            uuid REFERENCES users(id) ON DELETE CASCADE,
    service_account_id uuid REFERENCES service_accounts(id) ON DELETE CASCADE,
    created_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz,
    last_used_at       timestamptz,
    revoked_at         timestamptz,
    CHECK (num_nonnulls(user_id, service_account_id) = 1)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_api_keys_org ON api_keys (org_id, created_at DESC);
-- +goose StatementEnd

-- Project-level isolation foundation. visibility:
--   'open'       — any org member can use/see it (existing behavior, the default)
--   'restricted' — only project members (and org admins/owners) can launch into it
--                  or see its workloads. This is the client-isolation primitive.
-- +goose StatementBegin
ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS visibility  text NOT NULL DEFAULT 'open' CHECK (visibility IN ('open','restricted')),
    ADD COLUMN IF NOT EXISTS created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS archived_at timestamptz;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE project_members (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
-- +goose StatementEnd

-- Every workload belongs to a project from now on: give each org a Default
-- project and adopt the orphans. (The launch path also assigns it on the fly.)
-- +goose StatementBegin
INSERT INTO projects (org_id, name, description)
SELECT id, 'Default', 'Default project for workloads launched without an explicit project'
  FROM organizations
ON CONFLICT (org_id, name) DO NOTHING;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE workloads w
   SET project_id = p.id
  FROM projects p
 WHERE w.project_id IS NULL AND p.org_id = w.org_id AND p.name = 'Default';
-- +goose StatementEnd

-- Email verification + password reset tokens (single-use, hashed, expiring).
-- +goose StatementBegin
CREATE TABLE auth_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('verify_email','password_reset')),
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_auth_tokens_user ON auth_tokens (user_id, kind, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS project_members;
ALTER TABLE projects
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS visibility,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS archived_at;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS service_accounts;
-- +goose StatementEnd
