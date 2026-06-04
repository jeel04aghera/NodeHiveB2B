-- +goose Up
-- +goose StatementBegin
-- Store the requested GPU type so queued workloads can be promoted against the
-- right capacity later (F3).
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS requested_gpu_type text NOT NULL DEFAULT 'any';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workloads DROP COLUMN IF EXISTS requested_gpu_type;
-- +goose StatementEnd
