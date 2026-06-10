# NodeHive Operational Runbook (Phase 5)

How to monitor, diagnose and operate a NodeHive control-plane deployment. The
companion deploy guide is `deploy/RAILWAY.md`.

## 1. Endpoints at a glance

| Endpoint | Auth | Purpose |
|---|---|---|
| `GET /healthz` | none | Liveness: process up. Never touches dependencies. |
| `GET /readyz` | none | Readiness: 200 when the database answers a ping within 2s, else 503. Point the platform health check here. |
| `GET /metrics` | Bearer `METRICS_TOKEN` | Prometheus exposition. **Production: 404 until METRICS_TOKEN is set** (fail closed). Dev: open. |
| `GET /api/v1/health` | org admin/owner | Full diagnostics document (JSON): database, command queue, agents, background jobs, deployment info. |
| `GET /api/v1/audit-logs` | org member | Searchable org-scoped audit history (see §3). |

`/api/v1/healthz` remains as a legacy alias of `/healthz`.

## 2. Logging

- Production emits **one JSON object per line** on stdout (`LOG_FORMAT=json`,
  the default when `ENV` ≠ development). Dev uses human-readable text.
- Every HTTP request produces one `http_request` line with: `request_id`,
  `method`, `path`, `status`, `bytes`, `duration_ms`, `ip`, and `user_id`/`org_id`
  once authenticated. Probe endpoints log at debug to keep steady-state logs clean.
- **Correlation:** every response carries `X-Request-ID` (a sane inbound value is
  honored, otherwise generated). To trace one request end to end:
  1. take the `X-Request-ID` from the failing response (the SPA can surface it),
  2. grep the log drain for that id,
  3. search the audit log with `q=<request id>` — HTTP-originated audit events
     embed it in `metadata.request_id`.
- `LOG_LEVEL=debug` turns on per-probe access logs and module debug output.

## 3. Audit log

### Coverage
Authentication (`auth.login`, `auth.login_failed`, `auth.logout`, `auth.register`),
sessions (`session.revoke`, `session.revoke_all`), organization lifecycle
(`org.create`, `org.leave`, `org.transfer_ownership`), members and roles
(`member.role_change`, `member.remove`), invitations (`invitation.create/resend/
revoke/accept`), join codes (`join_code.create/revoke/join`), nodes (`node.enroll`,
`node.remove`, `node.revoke_credentials`), enrollment tokens (`enrollment_token.
issue/revoke`), workloads (`workload.launch`, `workload.stop`, `workload.stopped`,
`workload.failed` — user actions carry the user actor; sweep/idle/agent transitions
use `system`/`agent` actors), billing (`billing.topup`, `billing.rate_set`), plus
`user.create`, `user.assign_department`, `template.create`, `department.create`,
`project.create`.

Every event: actor (type + id), action, target (type + id), timestamp, client IP,
metadata (incl. `request_id`). Visibility is **org-scoped** — an org admin sees only
their org's trail. Failed logins for a known account are attributed to that
account's org so its admins can spot brute-force attempts.

### Search API
`GET /api/v1/audit-logs?` accepts `q` (free text across action/actor/target/
metadata), `action` (prefix match, e.g. `action=workload`), `actor_id`,
`target_type`, `target_id`, `from`/`to` (RFC3339, default last 30 days), `limit`
(≤500, default 100), `offset`. Response: `{items, total, limit, offset}`.

### Tamper resistance
- `audit_logs` is **append-only**: triggers reject UPDATE, DELETE and TRUNCATE for
  every role, including the application user.
- Each row is **hash-chained**: `row_hash = sha256(prev_row_hash ‖ canonical
  fields)`. Rewriting or removing any historical row breaks every later hash.
- Verify the chain (returns the ids of broken rows; empty = intact):
  ```sql
  SELECT * FROM audit_verify_chain();
  ```
  Run this on a schedule (or after any incident). A non-empty result means the
  audit table was modified outside the INSERT path — treat as a security incident.
- **Residual risk:** a database superuser can drop the triggers and rebuild the
  chain. For stronger guarantees, ship the log lines (or periodic chain heads —
  `SELECT max(id), row_hash FROM audit_logs ORDER BY id DESC LIMIT 1`) to external
  WORM storage.

## 4. Metrics (`/metrics`)

Set `METRICS_TOKEN` and scrape with
`Authorization: Bearer <token>` (Prometheus `authorization:` block, or Grafana
Cloud/Agent equivalent). Cadence 15–60s; each scrape costs a couple of short DB
queries.

Key series (all prefixed `nodehive_`):

| Metric | Meaning / alert hint |
|---|---|
| `http_requests_total{method,route,status}` | Alert on 5xx ratio > 1% over 5m. |
| `http_request_duration_seconds` | p95 latency per route. |
| `agents_connected` | Live agent streams. Alert when it drops below the expected fleet size for >5m. |
| `nodes{status}` | Enrolled nodes by status (online/offline). |
| `command_outbox{status}` / `command_outbox_overdue` | **overdue > 0 for >5m = page**: commands past their delivery deadline (agents not picking up work). |
| `workloads{status}` | Fleet workload states; a growing `pending` count means placement/delivery problems. |
| `job_last_success_timestamp_seconds{job}` / `job_failures_total{job}` | Alert when `time() - last_success > 3×` the job cadence (see §5 table). |
| `build_info{version,env}` | Deployed version. |
| `state_scrape_ok` | 0 = the DB sample failed during scrape. |

Standard Go/process collectors are included (`go_goroutines`,
`process_resident_memory_bytes`, …).

## 5. Background jobs

Visible in `/api/v1/health` (`jobs[]`) and `/metrics`. A job is *healthy* while its
last success is within 3× its cadence.

| Job | Cadence | Does |
|---|---|---|
| `offline_sweep` | 30s | Marks silent nodes offline. |
| `stuck_sweep` | 30s | Reclaims GPUs from dead workloads; fails undeliverable launches. |
| `queue_sweep` | 20s | Promotes queued workloads when capacity frees. |
| `meter_sweep` | 60s | Bills running workloads (restart-safe watermark). |
| `alert_eval` | 60s | Evaluates cost alert rules. |
| `idle_sweep` | 5m | Auto-stops idle workloads. |
| `retention_sweep` | 1h | Rolls up + ages out time-series data. |

**An unhealthy job with the process still up** usually means recurring DB errors —
check Sentry / Error-level logs for the job's name.

## 6. Error monitoring (Sentry)

Set `SENTRY_DSN` (project DSN) — that is the whole rollout; without it nothing
changes. What is captured:
- every HTTP panic (with `request_id`, method, path), already converted to a 500
  for the client;
- every `Error`-level log line anywhere in the control plane, with its attributes.

`VERSION` (or Railway's commit sha) becomes the Sentry release; `ENV` the
environment. Performance tracing is off by design (errors-only keeps the free tier
and adds no latency).

## 7. Diagnosing common situations

**`/readyz` 503** — DB unreachable. Check `DATABASE_URL`, the Postgres service,
and pool saturation in `/api/v1/health → database.detail`.

**Launches stuck `pending`** — check `/api/v1/health → queue`. `overdue > 0`
means commands aren't reaching agents: agent process down, gRPC endpoint
unreachable (Railway TCP proxy!), or credentials revoked. The stuck sweep will
fail the workload after the 10-minute delivery deadline; the audit trail shows
`workload.failed` with `reason: sweep_reclaim`.

**Node shows offline but the box is up** — agent stream dropped; with keepalive it
reconnects within ~1 minute. If not: agent logs, then `node.revoke_credentials` +
re-enroll as the big hammer.

**Billing anomalies** — `meter_sweep` health first (metering is watermark-based and
self-heals), then the org's `billing.*` audit events.

**Suspected tampering / insider activity** — `SELECT * FROM audit_verify_chain();`
then search the audit log by `actor_id` over the window.

## 8. Recommended minimal monitoring stack

1. **Structured JSON logs → platform log drain** (built-in; Railway captures
   stdout). Free, zero infra.
2. **Sentry (free tier) for errors** — set `SENTRY_DSN`. Alerts on new
   panic/error signatures with full context.
3. **Prometheus-compatible scrape of `/metrics`** — Grafana Cloud free tier (or
   any Prometheus) with `METRICS_TOKEN`. Three alerts cover most failure modes:
   5xx ratio, `command_outbox_overdue > 0`, stale `job_last_success`.
4. **Uptime probe on `/readyz`** (UptimeRobot/Better Stack/Railway health check).

Deliberately not adopted: full OpenTelemetry (SDK + collector + tracing backend) —
designed for multi-service request flows; the control plane is one process and
the OTel collector would be more infrastructure than the product itself today.
Revisit when the monolith splits or when cross-service traces (CP ↔ agents)
become a real debugging need. The `/metrics` endpoint and JSON logs are both
OTel-collector-ingestible later without code changes.
