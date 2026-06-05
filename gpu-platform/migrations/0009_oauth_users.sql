-- +goose Up
-- Google OAuth support (Phase 1). Adds provider columns to users and relaxes two
-- NOT NULLs so a Google identity can exist BEFORE it joins/creates an org
-- (pre-onboarding) and so OAuth-only users need no password. Single-org-per-user
-- is preserved: a user still has at most one org_id.
-- +goose StatementBegin
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS google_sub     text,
  ADD COLUMN IF NOT EXISTS avatar_url     text    NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS email_verified boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS auth_provider  text    NOT NULL DEFAULT 'password'
    CHECK (auth_provider IN ('password','google'));
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE users ALTER COLUMN org_id DROP NOT NULL;
-- One Google account maps to exactly one user (no duplicate identities).
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_google_sub ON users (google_sub) WHERE google_sub IS NOT NULL;
-- Pre-onboarding users (no org yet) are unique by email. Existing org users are
-- unmatched by this partial index, so their UNIQUE(org_id,email) is unchanged.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_no_org ON users (email) WHERE org_id IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_users_email_no_org;
DROP INDEX IF EXISTS idx_users_google_sub;
-- Re-tightening NOT NULL only succeeds when no pre-onboarding/OAuth-only rows exist
-- (true on a fresh DB / CI down-to-0). Backfill before rolling back in production.
UPDATE users SET password_hash = '' WHERE password_hash IS NULL;
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE users
  DROP COLUMN IF EXISTS auth_provider,
  DROP COLUMN IF EXISTS email_verified,
  DROP COLUMN IF EXISTS avatar_url,
  DROP COLUMN IF EXISTS google_sub;
-- org_id stays nullable on rollback: re-adding NOT NULL is unsafe if pre-onboarding
-- users exist. The previous binary simply never writes NULL org_id, so this is safe.
-- +goose StatementEnd
