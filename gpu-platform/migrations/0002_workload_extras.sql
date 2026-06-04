-- +goose Up
-- +goose StatementBegin
ALTER TABLE workloads
  ADD COLUMN IF NOT EXISTS expose_ssh     boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS expose_jupyter boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS ssh_password   text,
  ADD COLUMN IF NOT EXISTS logs           text;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workloads
  DROP COLUMN IF EXISTS expose_ssh,
  DROP COLUMN IF EXISTS expose_jupyter,
  DROP COLUMN IF EXISTS ssh_password,
  DROP COLUMN IF EXISTS logs;
-- +goose StatementEnd
