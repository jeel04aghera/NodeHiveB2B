-- +goose Up
-- +goose StatementBegin
-- Records when a workload was asked to stop, so the stuck-stopping sweep can
-- grant a grace period measured from the stop request (not from creation, which
-- made any workload older than the grace window instantly eligible to be marked
-- failed on a clean stop).
ALTER TABLE workloads
  ADD COLUMN IF NOT EXISTS stopping_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workloads
  DROP COLUMN IF EXISTS stopping_at;
-- +goose StatementEnd
