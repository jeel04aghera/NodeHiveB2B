-- 0016_observability.sql — Phase 5: tamper-resistant, searchable audit log.
--
-- Tamper resistance has two layers:
--   1. Append-only enforcement: a trigger rejects every UPDATE/DELETE on audit_logs,
--      so even the application role cannot rewrite history (defense against bugs and
--      a stolen app credential; a superuser can still drop the trigger, which is the
--      documented residual risk — see docs/RUNBOOK.md).
--   2. Hash chain: each row stores sha256(prev_row_hash || canonical row fields).
--      Any in-place edit or deletion breaks every subsequent hash, so tampering is
--      detectable with audit_verify_chain(). Inserts serialize on an advisory lock;
--      audit writes are async and low-volume, so this is not a throughput concern.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE audit_logs
    ADD COLUMN IF NOT EXISTS prev_hash text,
    ADD COLUMN IF NOT EXISTS row_hash  text;
-- +goose StatementEnd

-- Canonical row hash. extract(epoch ...) keeps the timestamp encoding independent of
-- the session timezone; jsonb::text is deterministic for a stored value.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_row_hash(
    p_prev text, p_org uuid, p_actor_type text, p_actor_id text, p_action text,
    p_target_type text, p_target_id text, p_metadata jsonb, p_ip inet, p_ts timestamptz
) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT encode(sha256(convert_to(
        coalesce(p_prev,'') || '|' || p_org::text || '|' || p_actor_type || '|' ||
        p_actor_id || '|' || p_action || '|' || p_target_type || '|' || p_target_id ||
        '|' || p_metadata::text || '|' || coalesce(p_ip::text,'') || '|' ||
        extract(epoch from p_ts)::text, 'UTF8')), 'hex')
$$;
-- +goose StatementEnd

-- Backfill the chain over existing rows in insertion order.
-- +goose StatementBegin
DO $$
DECLARE
    r    record;
    prev text := NULL;
BEGIN
    FOR r IN SELECT * FROM audit_logs ORDER BY id LOOP
        UPDATE audit_logs
           SET prev_hash = prev,
               row_hash  = audit_row_hash(prev, r.org_id, r.actor_type, r.actor_id,
                                          r.action, r.target_type, r.target_id,
                                          r.metadata, r.ip, r.ts)
         WHERE id = r.id;
        SELECT row_hash INTO prev FROM audit_logs WHERE id = r.id;
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_chain_link() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    prev text;
BEGIN
    -- Serialize chain head reads: concurrent inserts would otherwise both link to the
    -- same predecessor. Advisory lock is transaction-scoped.
    PERFORM pg_advisory_xact_lock(hashtext('audit_logs_chain'));
    SELECT row_hash INTO prev FROM audit_logs ORDER BY id DESC LIMIT 1;
    NEW.prev_hash := prev;
    NEW.row_hash  := audit_row_hash(prev, NEW.org_id, NEW.actor_type, NEW.actor_id,
                                    NEW.action, NEW.target_type, NEW.target_id,
                                    NEW.metadata, NEW.ip, NEW.ts);
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_block_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only (% blocked)', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER trg_audit_chain BEFORE INSERT ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_chain_link();
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER trg_audit_append_only BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_block_mutation();
-- +goose StatementEnd
-- TRUNCATE bypasses row triggers; block it too.
-- +goose StatementBegin
CREATE TRIGGER trg_audit_no_truncate BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION audit_block_mutation();
-- +goose StatementEnd

-- Verifies the whole chain; returns the ids of rows whose stored hash does not match
-- a recomputation (or whose prev link is wrong). Empty result = chain intact.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_verify_chain() RETURNS TABLE (bad_id bigint)
LANGUAGE plpgsql STABLE AS $$
DECLARE
    r    record;
    prev text := NULL;
BEGIN
    FOR r IN SELECT * FROM audit_logs ORDER BY id LOOP
        IF r.prev_hash IS DISTINCT FROM prev
           OR r.row_hash IS DISTINCT FROM audit_row_hash(prev, r.org_id, r.actor_type,
                r.actor_id, r.action, r.target_type, r.target_id, r.metadata, r.ip, r.ts)
        THEN
            bad_id := r.id;
            RETURN NEXT;
        END IF;
        prev := r.row_hash;
    END LOOP;
END $$;
-- +goose StatementEnd

-- Search indexes: action filtering within an org (the common audit-trail query) and
-- target lookups ("everything that happened to this workload").
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_audit_org_action_ts ON audit_logs (org_id, action, ts DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_audit_org_target ON audit_logs (org_id, target_type, target_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_audit_no_truncate ON audit_logs;
DROP TRIGGER IF EXISTS trg_audit_append_only ON audit_logs;
DROP TRIGGER IF EXISTS trg_audit_chain ON audit_logs;
DROP FUNCTION IF EXISTS audit_verify_chain();
DROP FUNCTION IF EXISTS audit_block_mutation();
DROP FUNCTION IF EXISTS audit_chain_link();
DROP FUNCTION IF EXISTS audit_row_hash(text, uuid, text, text, text, text, text, jsonb, inet, timestamptz);
ALTER TABLE audit_logs DROP COLUMN IF EXISTS prev_hash, DROP COLUMN IF EXISTS row_hash;
DROP INDEX IF EXISTS idx_audit_org_action_ts;
DROP INDEX IF EXISTS idx_audit_org_target;
-- +goose StatementEnd
