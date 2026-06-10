-- +goose Up
-- +goose StatementBegin
-- Phase 3 (agent transport): the control plane's self-signed CA for agent gRPC TLS.
-- Persisted in the database because Railway containers are ephemeral — the CA must
-- survive redeploys or every agent's pinned trust anchor would break on each deploy.
-- Single row (id=1). The leaf/server certificate is NOT stored: it is minted from
-- this CA at every boot (fresh validity window, current SANs).
CREATE TABLE transport_ca (
    id         smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    cert_pem   text NOT NULL,
    key_pem    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transport_ca;
-- +goose StatementEnd
