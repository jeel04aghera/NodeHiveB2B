-- +goose Up
-- Phase 3.5: track email delivery for invitations (observability). Status starts 'pending';
-- the async sender flips it to 'sent'/'failed', or 'skipped' when the email provider is
-- disabled (dev). Invitation creation never depends on delivery — these are advisory.
-- +goose StatementBegin
ALTER TABLE organization_invitations
  ADD COLUMN IF NOT EXISTS delivery_status text NOT NULL DEFAULT 'pending'
    CHECK (delivery_status IN ('pending','sent','failed','skipped')),
  ADD COLUMN IF NOT EXISTS delivery_error  text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS delivered_at    timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE organization_invitations
  DROP COLUMN IF EXISTS delivered_at,
  DROP COLUMN IF EXISTS delivery_error,
  DROP COLUMN IF EXISTS delivery_status;
-- +goose StatementEnd
