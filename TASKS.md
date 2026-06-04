# Tasks — NodeHive

> **Legend:** ✅ done · 🟡 partial · ◻ todo  
> **Last updated:** 2026-06-02 (end of full implementation session)

---

## Immediate next (ordered by priority)

1. **Offline workload sweep** — When `SweepOffline` marks a node offline, also set workloads on that node to `failed` (P0, ~2h)
2. **Enforce mTLS by default** — Flip `GRPC_INSECURE` default to `false`, document `make certs` (P0, ~1h)
3. **Enrollment token revoke** — `DELETE /enrollment-tokens/{id}` endpoint + UI button in Settings (P0, ~3h)
4. **Synthetic metric seed data** — Add 24h of fake `gpu_metrics` to `seed_dev.sql` so charts populate immediately (P1, ~30min)
5. **SSE real-time events** — `GET /events` endpoint + frontend `EventSource` (P1, ~1 day)

---

## Backlog

| ID | Title | Milestone | Status | Notes |
|---|---|---|---|---|
| T-001 | Repo bootstrap | M1 | ✅ | scaffold, buf/sqlc config, Makefile, CI |
| T-002 | Control-plane HTTP skeleton | M1 | ✅ | chi, pgx pool, `/healthz`, error envelope |
| T-003 | Agent gateway skeleton | M1 | ✅ | gRPC server, `Enroll` |
| T-004 | Agent transport | M1 | ✅ | enroll + `Connect` stream + reconnect + heartbeat |
| T-005 | DB layer | M1 | ✅ | pgx repo (sqlc adoption still deferred → T-005b) |
| T-005b | sqlc adoption | — | ◻ | hand-written SQL works; sqlc.yaml is staged |
| T-006 | Auth + users | M1 | ✅ | JWT, bcrypt, DEV_BOOTSTRAP_ADMIN, login/me/create |
| T-007 | Agent GPU discovery | M2 | ✅ | nvidia-smi enumeration → Inventory proto → DB upsert |
| T-008 | Inventory ingest | M2 | ✅ | `gpus` table populated; `/gpus` API works |
| T-009 | Agent metrics sampling | M3 | ✅ | nvidia-smi 30s → MetricsBatch → telemetry.Ingest |
| T-010 | Telemetry ingest | M3 | ✅ | partitioned `gpu_metrics` writes |
| T-011 | Rollups + retention | M3 | ◻ | partition create/drop job not scheduled |
| T-012 | Metrics API | M3 | ✅ | `/metrics/summary`, `/metrics/utilization`, `/metrics/idle` |
| T-013 | Dashboard + overview UI | M3 | ✅ | fleet cards + utilization chart (needs real data to show) |
| T-014 | Agent executor | M5 | ✅ | Docker executor runs real SSH/Jupyter containers; GPU passthrough configurable (off in dev). containerd/CDI is V2 |
| T-015 | Workloads lifecycle | M5 | ✅ | launch/stop, first-fit placement, billing on stop |
| T-016 | Workload reconcile + endpoints | M5 | ✅ | SSH/Jupyter endpoints real + reachable; stuck-workload sweep wired (T-033) |
| T-017 | Launch UI | M5 | ✅ | template picker, launch form, SSH command display, logs tab |
| T-018 | Rate cards | M4 | ✅ | CRUD + settings UI |
| T-019 | Usage metering | M4 | ✅ | `usage_records` from workload stop + `RecordWorkloadUsage` |
| T-020 | Idle detection | M4 | ✅ | `policy.EvaluateIdle` + `SweepIdle` every 5min |
| T-021 | Idle auto-stop | M5 | ✅ | `SweepIdle` calls `workloads.Stop` for managed workloads |
| T-022 | Chargeback report | M4 | ✅ | `cost_records`, `/billing/chargeback` + CSV (client-side) |
| T-023 | Scheduled report email | M4 | ◻ | not started |
| T-024 | Audit trail | — | ✅ | writes on: enroll, launch, stop, user.create; query endpoint |
| T-024b | Audit UI page | — | ◻ | backend done, frontend page not built |
| T-025 | Agent self-update | — | ◻ | `UpdateAgent` proto defined, handler not implemented |
| T-026 | Security hardening | — | 🟡 | TLS code complete, not default; no credential rotation |
| T-026a | mTLS enforce + certs | — | ◻ | `make certs` works; need to flip default |
| T-026b | Enrollment token rotation | — | ◻ | revoke endpoint missing |
| T-026c | Agent credential rotation | — | ◻ | expires_at not implemented |
| T-027 | Self-host packaging | M6 | 🟡 | Dockerfile + compose exist; install script + images pending |
| T-028 | Observability | — | 🟡 | slog structured logs; Prometheus metrics/tracing pending |
| T-029 | E2E dogfood + bug bash | M6 | ◻ | needs real GPU box test |
| T-030 | Pilot onboarding | M6 | ◻ | docs + security packet pending |
| T-031 | SSE real-time events | — | ◻ | currently polling; SSE not implemented |
| T-032 | Live workload logs | — | ◻ | logs on stop only; 30s push during running not done |
| T-033 | Offline workload sweep | — | ✅ | `SweepStuck` (30s ticker) fails workloads on offline nodes + stuck `stopping`, frees GPUs |
| T-034 | Metric seed data | — | ✅(wontfix) | dev agent emits live synthetic metrics every 5s; no fake seed rows needed |
| T-036 | Workload readiness probe | — | ◻ | reports `running` before sshd/jupyter accept connections |
| T-037 | Jupyter token hardening | — | ◻ | empty token when no SSH pass; generate+surface per-workload token |
| T-038 | Pre-baked sshd/jupyter images | — | ◻ | avoid per-launch apt/pip install latency |
| T-035 | Project picker in launch form | — | ◻ | project_id in schema, not in UI |

---

## Known gaps / accepted tech debt

- **gRPC is plaintext** (`GRPC_INSECURE=true`) — TLS code complete, not the default → **T-026a**
- **Credentials don't expire or rotate** → **T-026b, T-026c**
- **Workloads stuck on agent crash** — no timeout/sweep → **T-033**
- **Hand-written pgx SQL** — sqlc.yaml staged → **T-005b**
- **Chart shows no data from seed** — no synthetic metric rows → **T-034**
- **Live logs during running** — only captured on stop → **T-032**
- **No real GPU box test** → **T-029**
