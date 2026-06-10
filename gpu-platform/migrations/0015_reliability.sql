-- +goose Up
-- Phase 4 (Reliability): placement locking, command outbox, node removal, metrics retention.

-- +goose StatementBegin
-- R1: a GPU can have at most ONE active (undetached) workload attachment. The
-- application claims GPUs under row locks (FOR UPDATE SKIP LOCKED), and this partial
-- unique index is the database-level backstop: even a regressed code path cannot
-- double-assign a GPU. Pre-existing duplicates (from the historical race) are
-- resolved first by detaching all but the newest attachment per GPU.
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY gpu_id ORDER BY attached_at DESC, id DESC) AS rn
      FROM workload_gpus
     WHERE detached_at IS NULL
)
UPDATE workload_gpus wg SET detached_at = now()
  FROM ranked r
 WHERE wg.id = r.id AND r.rn > 1;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_workload_gpus_one_active
    ON workload_gpus (gpu_id) WHERE detached_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- R2: transactional command outbox. A launch/stop command is INSERTed in the same
-- transaction as the workload state change, so a command can never be lost between
-- "DB says pending/stopping" and "agent was told". The delivery engine sends rows
-- to connected agents (at-least-once; the agent dedupes by command id), the agent's
-- CommandResult acks them, and rows that miss deliver_by become an explicit failure.
CREATE TABLE agent_commands (
    id              uuid PRIMARY KEY,                 -- doubles as the wire command_id
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    node_id         uuid NOT NULL REFERENCES gpu_nodes(id) ON DELETE CASCADE,
    workload_id     uuid REFERENCES workloads(id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('launch','stop','get_inventory')),
    payload         bytea NOT NULL,                   -- marshaled agentv1.ServerMessage
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','sent','acked','failed','expired','superseded')),
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    deliver_by      timestamptz,                      -- explicit-failure deadline
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    acked_at        timestamptz
);
CREATE INDEX idx_agent_commands_due
    ON agent_commands (node_id, next_attempt_at) WHERE status IN ('pending','sent');
CREATE INDEX idx_agent_commands_workload ON agent_commands (workload_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- R5: node removal. Historical references must survive (or release) a node delete:
-- workloads keep their history with node_id nulled; per-GPU attachment history dies
-- with the GPU; usage records (billing truth) keep their numbers with gpu_id nulled.
ALTER TABLE workloads     DROP CONSTRAINT IF EXISTS workloads_node_id_fkey;
ALTER TABLE workloads     ADD CONSTRAINT workloads_node_id_fkey
    FOREIGN KEY (node_id) REFERENCES gpu_nodes(id) ON DELETE SET NULL;
ALTER TABLE workload_gpus DROP CONSTRAINT IF EXISTS workload_gpus_gpu_id_fkey;
ALTER TABLE workload_gpus ADD CONSTRAINT workload_gpus_gpu_id_fkey
    FOREIGN KEY (gpu_id) REFERENCES gpus(id) ON DELETE CASCADE;
ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_gpu_id_fkey;
ALTER TABLE usage_records ADD CONSTRAINT usage_records_gpu_id_fkey
    FOREIGN KEY (gpu_id) REFERENCES gpus(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- R6: hourly rollups so raw gpu_metrics can be aged out without losing the
-- long-range utilization history (capacity planning, chargeback context).
CREATE TABLE gpu_metrics_hourly (
    org_id          uuid NOT NULL,
    gpu_id          uuid NOT NULL,
    node_id         uuid NOT NULL,
    hour_ts         timestamptz NOT NULL,             -- date_trunc('hour', ts)
    sample_count    int  NOT NULL,
    avg_util_pct    real,
    max_util_pct    real,
    avg_mem_used_mb real,
    max_mem_used_mb int,
    avg_power_w     real,
    max_temp_c      real,
    ecc_errors      bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (gpu_id, hour_ts)
);
CREATE INDEX idx_gpu_metrics_hourly_org ON gpu_metrics_hourly (org_id, hour_ts);
-- +goose StatementEnd

-- +goose StatementBegin
-- R4: fast queue scans for the periodic promotion sweep.
CREATE INDEX idx_workloads_queued
    ON workloads (org_id, queued_at) WHERE status = 'queued';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_workloads_queued;
DROP TABLE IF EXISTS gpu_metrics_hourly;
ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_gpu_id_fkey;
ALTER TABLE usage_records ADD CONSTRAINT usage_records_gpu_id_fkey
    FOREIGN KEY (gpu_id) REFERENCES gpus(id);
ALTER TABLE workload_gpus DROP CONSTRAINT IF EXISTS workload_gpus_gpu_id_fkey;
ALTER TABLE workload_gpus ADD CONSTRAINT workload_gpus_gpu_id_fkey
    FOREIGN KEY (gpu_id) REFERENCES gpus(id);
ALTER TABLE workloads     DROP CONSTRAINT IF EXISTS workloads_node_id_fkey;
ALTER TABLE workloads     ADD CONSTRAINT workloads_node_id_fkey
    FOREIGN KEY (node_id) REFERENCES gpu_nodes(id);
DROP TABLE IF EXISTS agent_commands;
DROP INDEX IF EXISTS idx_workload_gpus_one_active;
-- +goose StatementEnd
