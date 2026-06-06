-- +goose Up
-- Phase 2: server-side session records backing refresh-token rotation, revocation,
-- "logout everywhere" and active-device management. Raw refresh tokens are NEVER
-- stored — only their SHA-256 hash (same discipline as enrollment_tokens). The
-- existing stateless Bearer access-token path is unchanged; sessions are additive.
-- +goose StatementBegin
CREATE TABLE user_sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash text NOT NULL UNIQUE,            -- sha256(raw); raw never stored
    device_name        text NOT NULL DEFAULT '',
    browser            text NOT NULL DEFAULT '',
    os                 text NOT NULL DEFAULT '',
    ip_address         text NOT NULL DEFAULT '',
    user_agent         text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    last_active_at     timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    revoked_at         timestamptz                       -- NULL = active
);
-- List a user's active sessions (the common query: WHERE user_id AND revoked_at IS NULL).
CREATE INDEX idx_user_sessions_user ON user_sessions (user_id) WHERE revoked_at IS NULL;
-- Sweep expired-but-not-revoked sessions cheaply.
CREATE INDEX idx_user_sessions_expires ON user_sessions (expires_at) WHERE revoked_at IS NULL;
-- (refresh lookup by hash is served by the UNIQUE constraint's implicit index.)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_sessions;
-- +goose StatementEnd
