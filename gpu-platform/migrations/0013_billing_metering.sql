-- +goose Up
-- Phase 2 (Billing P0): periodic metering watermark + credit ledger integrity.

-- +goose StatementBegin
-- metered_until is the end of the last billed time slice for a workload. NULL means
-- nothing has been billed yet (the first slice starts at started_at). Both the
-- periodic metering sweep and final settlement advance it under a row lock
-- (SELECT ... FOR UPDATE), making billing exactly-once per slice and restart-safe:
-- the watermark lives in the database, so a control plane crash between a workload
-- stopping and its usage being recorded is recovered by the next sweep instead of
-- silently losing the charge.
ALTER TABLE workloads ADD COLUMN IF NOT EXISTS metered_until timestamptz;
-- +goose StatementEnd

-- +goose StatementBegin
-- Candidate scan for the metering sweep (active or terminal-but-unsettled workloads).
CREATE INDEX IF NOT EXISTS idx_workloads_metering
    ON workloads (status) WHERE started_at IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Stable total order for the credit ledger. The ledger's "current balance" is the
-- balance column of the latest row; ordering by (created_at, id) was ambiguous for
-- same-timestamp inserts because id is a random uuid. seq is assigned in insert
-- order, so "latest" is now well-defined. Existing rows are backfilled by Postgres
-- when the identity column is added.
ALTER TABLE credit_ledger ADD COLUMN IF NOT EXISTS seq bigint GENERATED ALWAYS AS IDENTITY;
CREATE INDEX IF NOT EXISTS idx_credit_ledger_org_seq ON credit_ledger (org_id, seq DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_credit_ledger_org_seq;
ALTER TABLE credit_ledger DROP COLUMN IF EXISTS seq;
DROP INDEX IF EXISTS idx_workloads_metering;
ALTER TABLE workloads DROP COLUMN IF EXISTS metered_until;
-- +goose StatementEnd
