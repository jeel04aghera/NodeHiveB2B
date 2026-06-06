-- +goose Up
-- Phase 3: organization memberships, roles (owner/admin/member), email invitations and
-- shareable join codes. SINGLE-ACTIVE-ORG is preserved: users.org_id remains the scoping
-- source of truth and each user has exactly one membership; organization_members is the
-- authoritative record of role and the groundwork for multi-org later. Raw invite tokens
-- and join codes are never stored — only their SHA-256 hash.

-- 1) Migrate users.role from the old {admin,user} domain to {owner,admin,member}.
--    'user' -> 'member'; the earliest-created admin in each org becomes 'owner' (the
--    de-facto creator), other admins stay 'admin'.
-- +goose StatementBegin
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE users SET role = 'member' WHERE role = 'user';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE users u SET role = 'owner'
  FROM (SELECT DISTINCT ON (org_id) id
          FROM users
         WHERE role = 'admin' AND org_id IS NOT NULL
         ORDER BY org_id, created_at, id) firsts
 WHERE u.id = firsts.id;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'member';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('owner','admin','member'));
-- +goose StatementEnd

-- 2) organization_members — authoritative membership + role record. One per (org,user).
-- +goose StatementBegin
CREATE TABLE organization_members (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       text NOT NULL DEFAULT 'member' CHECK (role IN ('owner','admin','member')),
    invited_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, user_id)
);
CREATE INDEX idx_org_members_org  ON organization_members (org_id);
CREATE INDEX idx_org_members_user ON organization_members (user_id);
-- Backfill from the existing single-org assignment so every current user is a member.
INSERT INTO organization_members (org_id, user_id, role)
  SELECT org_id, id, role FROM users WHERE org_id IS NOT NULL
  ON CONFLICT (org_id, user_id) DO NOTHING;
-- +goose StatementEnd

-- 3) organization_invitations — pending email invites. Raw token hashed; one open invite
--    per (org,email) enforced by a partial unique index.
-- +goose StatementBegin
CREATE TABLE organization_invitations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email       citext NOT NULL,
    role        text NOT NULL DEFAULT 'member' CHECK (role IN ('admin','member')),
    token_hash  text NOT NULL UNIQUE,             -- sha256(raw); raw never stored
    invited_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    revoked_at  timestamptz
);
CREATE INDEX idx_org_invites_org ON organization_invitations (org_id);
CREATE UNIQUE INDEX idx_org_invites_pending
  ON organization_invitations (org_id, email)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;
-- +goose StatementEnd

-- 4) organization_join_codes — shareable self-join codes, bounded by expiry + max uses.
-- +goose StatementBegin
CREATE TABLE organization_join_codes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    code_hash   text NOT NULL UNIQUE,             -- sha256(raw); raw never stored
    description text NOT NULL DEFAULT '',
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    max_uses    int  NOT NULL DEFAULT 0,          -- 0 = unlimited
    uses        int  NOT NULL DEFAULT 0,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_org_join_codes_org ON organization_join_codes (org_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS organization_join_codes;
DROP TABLE IF EXISTS organization_invitations;
DROP TABLE IF EXISTS organization_members;
-- +goose StatementEnd
-- Revert users.role to the legacy {admin,user} domain.
-- +goose StatementBegin
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE users SET role = 'admin' WHERE role = 'owner';
UPDATE users SET role = 'user'  WHERE role = 'member';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'user';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin','user'));
-- +goose StatementEnd
