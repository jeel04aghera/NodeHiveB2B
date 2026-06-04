-- +goose Up
-- +goose StatementBegin
-- credit_ledger: the org's real prepaid credit balance. Append-only; each row
-- carries the running balance after it is applied. Amounts are in the product's
-- display currency (INR); workload charges convert from USD cost_records at debit
-- time. A negative delta is a charge (consumed by workload usage); positive is a
-- grant or top-up.
CREATE TABLE credit_ledger (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    delta       numeric(14,4) NOT NULL,
    balance     numeric(14,4) NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('grant','topup','charge','adjustment')),
    description text NOT NULL DEFAULT '',
    workload_id uuid REFERENCES workloads(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_credit_ledger_org ON credit_ledger (org_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Seed every existing org with a starter grant so the balance UI is populated.
INSERT INTO credit_ledger (org_id, delta, balance, kind, description)
SELECT id, 50000.0000, 50000.0000, 'grant', 'Welcome credit'
FROM organizations;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS credit_ledger;
-- +goose StatementEnd
