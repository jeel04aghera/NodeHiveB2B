# GPU Platform — V1 Engineering Specification

**Mode:** Execution. **Author seat:** Staff Engineer + Founding CTO.
**Optimizing for:** first deployment · first customer · fastest learning · lowest engineering risk. **Not** elegance.
**Accepted constraints:** owned-GPU only · visibility + utilization first · reclaim + self-service · **no** marketplace · **no** custom scheduler · **no** custom overlay · **no** future cloud platform. 2 founders · 6 engineers · 18 months.
**Date:** 2026-06-01

> **The product in one line:** install an agent on the GPU boxes you already own and, within a day, see real utilization, reclaim idle capacity, give engineers self-service launch, and hand finance a chargeback report. Everything below is the minimum to make that true.

---

# TASK 1 — Define MVP

## Problem Statement
Organizations that own GPUs run them at ~5% utilization with no visibility into who is using what, no way to reclaim idle capacity, no self-service access (engineers SSH directly into boxes and fight over cards), and no cost accountability. V1 makes an owned GPU fleet **visible, accountable, and self-serviceable within a day of install** — turning idle, contested hardware into a usable internal cloud and a number finance can defend.

## Target Customer
A single organization that **owns or colocates** its GPUs: a mid-market AI-product company, ~10–50 engineers, fleet running under ~20% utilization, **no dedicated platform team**, with a founder/CFO now asking what the GPU spend bought. Cloud-native enough to install in an afternoon. Explicitly **not** a university (free Open OnDemand), **not** a hyperscaler, **not** already standardized on Run:ai/Kubernetes. V1 is **single-org** — one deployment serves one customer.

## User Personas
- **Priya — Platform Admin / Infra Lead.** Installs the agent, sets idle policy and rates, owns the fleet, exports reports. Primary daily admin user. Technical.
- **Sam — ML Engineer / Researcher.** Wants a GPU with the right environment *now*, via SSH/Jupyter, without filing a ticket or pinging Priya. Primary self-service user. Cares about speed, not infra.
- **Dana — Finance / Eng Leadership.** Does not log in daily. Consumes the chargeback and utilization reports; the buyer of the "what did we spend" answer.

## Jobs To Be Done
- *(Priya)* "Show me every GPU we own and how utilized it actually is." · "Reclaim the idle ones." · "Let my team self-serve without me babysitting." · "Give finance a chargeback report they'll accept."
- *(Sam)* "Get me a GPU with my environment in minutes, with SSH/Jupyter, and don't make me learn Slurm or K8s."
- *(Dana)* "Tell me what each team's GPU usage cost last month."

## Success Metrics (V1)
- **Install-to-first-inventory < 1 hour**; **install-to-value < 1 day**.
- **Time-to-first-instance for an engineer < 5 minutes.**
- Utilization baseline established within 24h of install; **idle GPU-hours and their $ cost quantified**.
- ≥1 reclaim action taken (auto-stop of a managed idle workload) or idle alert acted on.
- ≥1 **chargeback report exported and accepted by finance**.
- **Pilot outcome:** one design-partner organization using V1 *weekly* on real owned hardware, with a measured before/after utilization number.

## Non-Goals (ruthlessly excluded from V1)
Marketplace · multi-org / multi-tenant isolation · custom scheduler (gang-scheduling, DRF, preemption, reservations) · custom overlay / relay network · multi-region · HA / automatic failover · billing & payment rails (invoicing, Stripe) · double-entry ledger · Kata/KVM/Firecracker isolation · GPU partitioning UI (MIG/MPS management) · cross-tenant GPU sharing · autoscaling · spot/preemptible markets · SSO/SAML/SCIM (WorkOS later) · fine-grained RBAC (admin/user only) · hosted image registry · public-cloud bursting · Windows nodes · non-NVIDIA GPUs · mobile app. **If a feature is not required for the first paying customer's value, it is not in V1.**

---

# TASK 2 — User Workflows

Seven workflows, each as Trigger → User actions → Backend actions → Data generated → Failure scenarios.

### 1. Platform Admin installs agent
- **Trigger:** Priya wants to onboard a GPU node.
- **User actions:** In the dashboard, clicks *Add node* → copies a one-line install command containing a short-lived **enrollment token** → runs it on the node (`curl … | sh` or download binary + `gpu-agent enroll --token …`).
- **Backend actions:** Validate the token (hashed, single-/limited-use, expiring); create the `gpu_nodes` row; agent generates a keypair and the control plane issues a per-agent credential bound to `node_id`; the agent opens its persistent outbound connection.
- **Data generated:** `gpu_nodes` row, `agent_credentials` row, enrollment `audit_logs` entry.
- **Failure scenarios:** invalid/expired token → clear rejection; **node cannot reach control plane (egress firewall)** → agent prints a connectivity diagnostic (this is the #1 real-world failure, surface it loudly); missing NVIDIA driver/DCGM → agent enrolls but reports `degraded` with the reason; re-running install on an already-enrolled node → idempotent (updates, not duplicates).

### 2. GPUs appear in dashboard
- **Trigger:** agent connects and runs hardware discovery.
- **User actions:** Priya watches the node and its GPUs populate (no action required).
- **Backend actions:** agent reports node specs (CPU/RAM/OS/driver/CUDA) and per-GPU inventory (model, memory, UUID) via NVML/`nvidia-smi`; control plane upserts `gpu_nodes` and `gpus`.
- **Data generated:** `gpus` rows, updated `gpu_nodes`, discovery `audit_logs`.
- **Failure scenarios:** partial discovery (MIG-mode card, driver mismatch) → show what's visible, flag the rest; hardware changes later → re-discovery on reconnect and on interval reconciles inventory.

### 3. Usage becomes visible
- **Trigger:** agent streams metrics on a fixed interval.
- **User actions:** Priya opens the utilization dashboard (per-GPU, per-node, fleet-wide time series).
- **Backend actions:** agent samples DCGM/NVML (util %, mem, power, temp, process count) every ~15s, batches, pushes over the existing connection; control plane writes to time-partitioned `gpu_metrics`; a rollup worker produces hourly aggregates.
- **Data generated:** `gpu_metrics` samples, hourly rollups.
- **Failure scenarios:** agent offline → gaps shown honestly (no fabricated data); clock skew → server-side timestamps authoritative; metric volume growth → fixed sample interval + retention/partition drop.

### 4. Idle resources identified
- **Trigger:** the policy worker evaluates the idle rule on each rollup.
- **User actions:** Priya views the *Idle* panel (which GPUs, for how long, $ wasted) and tunes the threshold/duration in org settings.
- **Backend actions:** compute idle = utilization below threshold for ≥ continuous duration; flag the GPU and any managed workload on it; compute idle cost from the rate card.
- **Data generated:** idle findings, idle `cost_records`.
- **Failure scenarios:** false-positive idle (low-util-but-important job) → require a minimum duration and allow label-based exclusions; **idle usage from a process not launched via the platform → alert only, never auto-kill** (safety rule for V1).

### 5. Chargeback reports generated
- **Trigger:** Priya/Dana requests a report (or a scheduled run fires).
- **User actions:** pick period + grouping (project or user) → view → export CSV.
- **Backend actions:** aggregate `usage_records` × `rate_cards` → `cost_records` → grouped totals.
- **Data generated:** `cost_records`, a report export, `audit_logs` of the export.
- **Failure scenarios:** untagged usage → bucket as *Unallocated* and prompt to assign a project; no rate configured → apply a default rate with a visible warning; period contains agent gaps → annotate coverage %.

### 6. User launches a workload
- **Trigger:** Sam needs a GPU.
- **User actions:** pick a **template** (image) + GPU type/count + optional idle-timeout → *Launch* → receive SSH and/or Jupyter endpoint + live status.
- **Backend actions:** admission check (is matching capacity free?); **first-fit placement** picks a node+GPU(s); dispatch a `LaunchWorkload` command to the agent; agent runs the container with GPU access via NVIDIA Container Toolkit (CDI); on ready, return endpoint; begin metering.
- **Data generated:** `workloads` row, `workload_gpus`, lifecycle `audit_logs`, ongoing `usage_records`.
- **Failure scenarios:** no free capacity → clear "no GPUs available" (V1 does **not** queue — it tells the truth); image pull failure → surface agent logs; container crash → status + logs; GPU fault mid-run → mark unhealthy, stop scheduling onto it; endpoint unreachable → connectivity guidance (LAN IP:port for reachable nodes, Tailscale for NAT'd nodes — we do not proxy traffic in V1).

### 7. Admin receives utilization report
- **Trigger:** scheduled (e.g., weekly) or on-demand.
- **User actions:** Priya/Dana receives an emailed summary (fleet utilization, idle $, top users/projects, reclaimed GPU-hours) and can open the full report.
- **Backend actions:** scheduled job aggregates rollups + usage → renders summary → emails recipients; same data available in-app.
- **Data generated:** report artifact, email-send `audit_logs`.
- **Failure scenarios:** email delivery failure → retry then fall back to in-app notification; empty period → send a clearly-labeled "no activity" summary rather than nothing.

---

# TASK 3 — MVP Architecture

Five moving parts, plus Postgres. Each justified on *why it exists / why now / why it isn't over-engineered.*

**Frontend — Next.js + React + TypeScript dashboard (single app).**
- *Why it exists:* the admin and engineer UI is the product surface — the utilization screen is literally the demo that closes the sale.
- *Why now:* visibility and self-service launch are unusable without it.
- *Why not over-engineered:* one app, one API client, component library off the shelf. No micro-frontends, no SSR complexity beyond what Next gives free.

**Backend — single Go modular monolith (one deployable).**
- *Why it exists:* the control plane — auth, enrollment, inventory, metrics ingest, idle evaluation, launch orchestration, reporting. It also terminates agent connections.
- *Why now:* it is the brain; nothing works without it.
- *Why not over-engineered:* one process with clean internal modules and one database. No microservices, no cells, no external message bus, no service mesh. In-process workers handle rollups and scheduled reports.

**Database — single PostgreSQL primary.**
- *Why it exists:* system of record for orgs/users/nodes/gpus/workloads and the immutable usage/cost truth.
- *Why now:* every feature needs durable, queryable state.
- *Why not over-engineered:* one Postgres. Time-series lives in **time-partitioned tables**, not a separate TSDB. No ClickHouse, no Citus, no sharding, no read replicas yet.

**Agent — single static Go binary on each GPU node.**
- *Why it exists:* the only thing that can see and act on customer hardware; it dials out (outbound-only), so no inbound firewall changes.
- *Why now:* there is no data and no control without it.
- *Why not over-engineered:* one binary, no plugins, no embedded scheduler, no peer networking. It discovers, samples, heartbeats, and runs/stops containers on command.

**Metrics pipeline — agent sampling → existing agent connection → Postgres (partitioned) → rollup worker.**
- *Why it exists:* utilization visibility, idle detection, and metering all derive from GPU telemetry.
- *Why now:* it is the core value (visibility) and the basis of reclaim and chargeback.
- *Why not over-engineered:* it **reuses the agent's connection** (no Kafka/NATS), writes to Postgres (no streaming cluster), and keeps raw samples on a short retention with hourly rollups for the long tail.

**Authentication — local email/password (bcrypt) + session token for humans; enrollment-token → per-agent credential for agents.**
- *Why it exists:* gate human access; establish trust with each agent.
- *Why now:* required on day one.
- *Why not over-engineered:* a library, not a platform. No SAML/SCIM/OIDC-provider, no Vault, no policy engine. Optional Google OIDC for the SaaS variant is a small add, not a dependency.

**Connectivity (named explicitly so nobody "fixes" it later).** The **agent control channel** is one outbound gRPC stream to the backend — that is the entire networking story for control. **Workload access** (SSH/Jupyter to a launched container) is the node's `IP:port` for LAN-reachable owned hardware, with **Tailscale** documented for NAT'd nodes. We **buy** connectivity; we do **not** build a relay, gateway, or overlay in V1.

**Storage.** Postgres for structured data; **local node disk** for ephemeral workload storage; the customer's **existing container registry / Docker Hub** for images (we do not host a registry). Optional S3-compatible bucket only if report/log export needs it — not required for V1.

### Intentionally deferred (and the trigger that would revisit each)
| Deferred | Revisit when |
|---|---|
| Relay / gateway / overlay network | A paying customer's nodes can't be reached even with Tailscale |
| Custom scheduler (gang/DRF/preemption/reservations) | Multi-tenant contention; until then, first-fit + KAI/DRA for K8s customers |
| Marketplace / multi-org | After the owned-GPU control plane is proven and sticky |
| HA / multi-region / failover | Control-plane downtime threatens a signed SLA |
| ClickHouse / Kafka / Citus | Postgres metrics/usage tables actually hurt (measure first) |
| Double-entry ledger / invoicing | Real money flows out (payouts/billing), not just chargeback |
| Kata/KVM/Firecracker, MIG/MPS UI | A regulated or multi-tenant customer needs hard isolation/partitioning |
| SSO/SAML/SCIM | First enterprise deal requires it → adopt WorkOS, don't hand-roll |
| Autoscaling, spot, queueing | Demand patterns prove the need; V1 tells the truth instead of queuing |

---

# TASK 4 — Service Boundaries

**Default decision: one modular monolith.** With 6 engineers and zero scale pressure, separate services would add deploy, network, and transactional cost for no benefit. The only things that are *separate by necessity* are the **Agent** (runs on customer hardware), the **Frontend** (runs in the browser), and **Postgres**. Everything else is a Go *module* (package) inside one binary, with boundaries clean enough to extract later at near-zero cost.

For each module: responsibility · APIs · data owned · dependencies — followed by the honest "should this be its own service?" answer (uniformly **no** for V1).

| Module | Responsibility | APIs (exposed) | Data owned | Depends on | Separate service? |
|---|---|---|---|---|---|
| **Identity & Auth** | Login, sessions, users, roles, enrollment tokens, agent credentials | `/auth/*`, `/users/*`, `/enrollment-tokens` | organizations, users, enrollment_tokens, agent_credentials | — | No — shared by all; splitting adds auth latency everywhere |
| **Fleet / Inventory** | Node enrollment, hardware inventory, node/GPU status | `/nodes/*`, `/gpus/*` | gpu_nodes, gpus, agent_heartbeats | Identity, Agent Gateway | No — tightly coupled to agent lifecycle |
| **Telemetry** | Ingest metrics batches, store, roll up | `/metrics/*` (read) | gpu_metrics, rollups | Fleet, Agent Gateway | No — would just be a Postgres-writer with extra hops |
| **Policy / Idle** | Idle detection, reclaim actions, alerts | `/metrics/idle`, settings | idle findings (derived), org settings | Telemetry, Workloads, Billing | No — small worker over shared data |
| **Workloads** | Launch/stop lifecycle, first-fit placement, templates, command dispatch | `/workloads/*`, `/templates` | workloads, workload_gpus | Fleet, Agent Gateway, Billing | No — transactional with inventory; a split here would need distributed txns |
| **Billing / Chargeback** | Usage records, rate cards, cost records, reports | `/reports/*`, `/rate-cards` | usage_records, cost_records, rate_cards, projects | Telemetry, Workloads | No — derives from local data; keep in-process |
| **Audit** | Append-only record of who did what | (internal) | audit_logs | — | No — a shared in-process writer |
| **Agent Gateway** | Terminate agent gRPC streams, authenticate agents, route commands/telemetry | gRPC `AgentService` (mTLS) | (writes via Fleet/Telemetry/Workloads) | Identity (agent creds) | **Borderline** — could become its own process at high agent counts; in V1 it is a module in the same binary, behind its own listener |

The Agent Gateway is the only candidate for future extraction (it has a different scaling axis — long-lived connections). It is built as a self-contained module *now* so that extraction later is a deployment change, not a rewrite. Everything else stays in the monolith indefinitely until data says otherwise.

---

# TASK 5 — Database Design

PostgreSQL, single primary. Every table carries `org_id` from day one (even though V1 is single-org) because adding a tenant key later is the one schema change that is *brutally* expensive to retrofit — so we pay the trivial cost now and stay multi-org-ready without building multi-org. UUID primary keys for entities; `BIGINT` identity for high-volume append-only logs. Two distinct telemetry concepts are kept separate on purpose: **`gpu_metrics`** (raw operational samples, short retention) vs. **`usage_records`** (billing-grade, immutable, long retention).

The nine required tables are below, plus four support tables that V1 genuinely cannot function without (`projects`, `rate_cards`, `gpu_metrics`, `enrollment_tokens` + `agent_credentials`), each flagged.

### organizations
*Why:* tenant root and the home for fleet-wide settings (idle threshold, currency). One row in self-hosted; many later in SaaS.
- `id uuid PK`, `name text`, `slug text unique`, `settings jsonb` (idle_threshold_pct, idle_duration_min, currency, default_rate), `created_at timestamptz`
- **Indexes:** `unique(slug)`

### users
*Why:* human identity and authorization.
- `id uuid PK`, `org_id uuid FK→organizations`, `email citext`, `password_hash text`, `name text`, `role text` (`admin`|`user`), `status text` (`active`|`disabled`), `last_login_at timestamptz`, `created_at`
- **Relationships:** belongs to org. **Indexes:** `unique(org_id, email)`, `index(org_id)`

### projects *(support — required for chargeback grouping)*
*Why:* chargeback must group by cost center; without it, chargeback is per-user only and finance won't accept it.
- `id uuid PK`, `org_id uuid FK`, `name text`, `created_at`
- **Indexes:** `unique(org_id, name)`

### gpu_nodes
*Why:* the physical/virtual machine; maps 1:1 to an installed agent.
- `id uuid PK`, `org_id uuid FK`, `hostname text`, `status text` (`online`|`offline`|`degraded`), `os text`, `kernel text`, `cpu_model text`, `cpu_cores int`, `ram_mb bigint`, `nvidia_driver text`, `cuda_version text`, `agent_version text`, `labels jsonb`, `enrolled_at`, `last_seen_at`
- **Relationships:** has many gpus, heartbeats. **Indexes:** `index(org_id, status)`, `index(last_seen_at)`

### gpus
*Why:* the unit we utilize, reclaim, and bill.
- `id uuid PK`, `node_id uuid FK→gpu_nodes`, `org_id uuid` (denormalized for fast fleet queries), `gpu_index int` (index on node), `uuid text` (NVIDIA GPU UUID), `model text`, `memory_mb bigint`, `mig_enabled bool`, `status text` (`healthy`|`unhealthy`|`in_use`|`idle`), `created_at`, `updated_at`
- **Relationships:** belongs to node. **Indexes:** `unique(uuid)`, `index(node_id)`, `index(org_id, status)`

### workloads
*Why:* the unit of self-service, metering, and safe reclaim — the only thing the platform will ever auto-stop.
- `id uuid PK`, `org_id uuid FK`, `project_id uuid FK→projects NULL`, `user_id uuid FK→users` (launcher), `node_id uuid FK→gpu_nodes`, `name text`, `image text`, `requested_gpu_count int`, `status text` (`pending`|`running`|`stopping`|`stopped`|`failed`), `idle_timeout_sec int NULL`, `ssh_endpoint text NULL`, `jupyter_endpoint text NULL`, `started_at`, `stopped_at`, `stop_reason text` (`user`|`idle_reclaim`|`admin`|`failure`), `created_at`
- **Relationships:** belongs to org/user/node/project; has many workload_gpus, usage_records. **Indexes:** `index(org_id, status)`, `index(node_id)`, `index(user_id)`, `index(project_id)`

### workload_gpus *(join — required)*
*Why:* a workload may hold multiple GPUs; a clean join beats an array for integrity and metering.
- `id uuid PK`, `workload_id uuid FK`, `gpu_id uuid FK`, `attached_at`, `detached_at NULL`
- **Indexes:** `unique(workload_id, gpu_id)`, `index(gpu_id)`

### gpu_metrics *(support — required; the visibility/idle source)*
*Why:* raw telemetry powering the utilization dashboard and idle detection. Operational, high-volume, short retention.
- `id bigint GENERATED ALWAYS AS IDENTITY`, `org_id uuid`, `gpu_id uuid`, `node_id uuid`, `ts timestamptz`, `util_pct real`, `mem_used_mb int`, `power_w real`, `temp_c real`, `sm_clock_mhz int`, `ecc_errors int`, `proc_count int`
- **Partitioning:** range-partitioned by `ts` (weekly); drop old partitions per retention (e.g., 30–90 days). **Indexes:** `index(gpu_id, ts DESC)` per partition.

### usage_records
*Why:* the **immutable, append-only** billing truth. If we don't capture clean usage from instance #1, it can never be reconstructed — this is the most expensive thing to get wrong. Distinct from `gpu_metrics` (this is aggregated and permanent).
- `id bigint GENERATED ALWAYS AS IDENTITY`, `org_id uuid`, `workload_id uuid FK`, `gpu_id uuid FK`, `project_id uuid NULL`, `user_id uuid`, `period_start timestamptz`, `period_end timestamptz`, `gpu_seconds bigint`, `avg_util_pct real`, `max_mem_mb int`, `source text` (`workload`|`idle`), `created_at`
- **Immutability:** insert-only; no UPDATE/DELETE in app code. **Indexes:** `index(org_id, period_start)`, `index(workload_id)`, `index(project_id, period_start)`

### rate_cards *(support — required for cost)*
*Why:* turning GPU-seconds into money needs a price per GPU model; must be configurable and time-bounded.
- `id uuid PK`, `org_id uuid FK`, `gpu_model text`, `rate_per_gpu_hour numeric(12,4)`, `currency text`, `effective_from timestamptz`, `effective_to timestamptz NULL`, `created_at`
- **Indexes:** `index(org_id, gpu_model, effective_from)`

### cost_records
*Why:* materialized money — usage × rate — so reports are stable and fast and don't silently change when a rate is edited.
- `id bigint GENERATED ALWAYS AS IDENTITY`, `org_id uuid`, `usage_record_id bigint FK`, `project_id uuid NULL`, `user_id uuid`, `period_start`, `period_end`, `gpu_seconds bigint`, `rate_per_gpu_hour numeric(12,4)`, `rate_card_id uuid FK`, `currency text`, `amount numeric(14,4)`, `created_at`
- **Indexes:** `index(org_id, period_start)`, `index(project_id, period_start)`

### agent_heartbeats
*Why:* liveness and debugging ("when did this node last check in?"). High-volume but disposable.
- `id bigint GENERATED ALWAYS AS IDENTITY`, `org_id uuid`, `node_id uuid FK`, `ts timestamptz`, `status text` (`healthy`|`degraded`), `agent_version text`, `summary jsonb` (gpu count, mean util)
- **Partitioning:** range by `ts`, short retention (e.g., 7 days). `gpu_nodes.last_seen_at` is the denormalized fast path; this table is the history. **Indexes:** `index(node_id, ts DESC)`

### enrollment_tokens + agent_credentials *(support — required for agent trust)*
*Why:* secure two-step agent onboarding (short-lived token → durable per-agent credential).
- **enrollment_tokens:** `id uuid PK`, `org_id uuid`, `token_hash text`, `created_by uuid`, `expires_at`, `max_uses int`, `uses int`, `created_at`. Index `unique(token_hash)`.
- **agent_credentials:** `id uuid PK`, `org_id uuid`, `node_id uuid FK`, `public_key text` (or cert fingerprint), `issued_at`, `revoked_at NULL`. Index `index(node_id)`, `unique(public_key)`.

### audit_logs
*Why:* security/compliance and the answer to "who stopped my workload?" Append-only.
- `id bigint GENERATED ALWAYS AS IDENTITY`, `org_id uuid`, `actor_type text` (`user`|`agent`|`system`), `actor_id text`, `action text`, `target_type text`, `target_id text`, `metadata jsonb`, `ip inet NULL`, `ts timestamptz`
- **Indexes:** `index(org_id, ts DESC)`, `index(actor_type, actor_id)`

**Schema notes:** foreign keys enforce integrity within the single Postgres; `org_id` is everywhere for future tenancy; the only partitioned tables are the three high-volume append-only ones (`gpu_metrics`, `agent_heartbeats`, optionally `usage_records` by month). No triggers, no stored procedures, no exotic extensions beyond `citext`/`uuid-ossp`. Migrations are plain SQL, forward-only.

---

# TASK 6 — Agent Specification

A single static Go binary, one per GPU node. It is the highest-trust, hardest-to-reverse component, so it is small, auditable, and conservative.

### Responsibilities
1. **Registration** — `gpu-agent enroll --token <T> --server <url>`: generate a keypair, send public key + node info, receive `node_id` + a durable credential, persist them locally (root-only file).
2. **Hardware discovery** — enumerate CPU/RAM/OS/kernel and NVIDIA GPUs via NVML/`nvidia-smi` (model, memory, UUID, MIG state, driver/CUDA). Re-run on reconnect and on interval.
3. **GPU metrics collection** — sample DCGM (preferred) or NVML every ~15s: util %, memory, power, temp, SM clock, ECC errors, process count. Batch and push.
4. **Heartbeats** — lightweight liveness + summary every ~30s on the same connection.
5. **Workload execution** — on command, run a container with GPU access via **containerd + NVIDIA Container Toolkit (CDI)**; stop it on command or on idle-timeout; report status and logs.

### Communication protocol
- **One persistent, outbound-only connection:** a **gRPC bidirectional stream over TLS (HTTP/2, port 443)**. The agent dials the control plane; the control plane never dials the agent. No inbound ports are opened on the node.
- On the stream: **agent→server** = `Heartbeat`, `Inventory`, `MetricsBatch`, `WorkloadStatus`, `CommandResult`; **server→agent** = `LaunchWorkload`, `StopWorkload`, `GetInventory`, `UpdateAgent`.
- **Versioned from v1** (a `protocol_version` in the handshake); the server must support N-1 agents forever, because you cannot force-upgrade fleets you don't own.
- Reconnect with backoff; on reconnect the agent **re-sends inventory and reconciles** running-workload state (self-healing).

### Authentication model
- **Enrollment:** short-lived, hashed, use-limited `enrollment_token` → agent submits a generated public key → server issues an `agent_credential` bound to `node_id`.
- **Steady state:** every connection authenticates with that credential — **mTLS client cert (preferred)** or a signed, rotating token. Server identity is verified against a **pinned CA**. Credentials are individually **revocable**.

### Update strategy
- The server advertises a desired agent version + a **signed** binary URL. The agent downloads, **verifies the signature**, swaps, restarts, and runs a health check; **on failure it rolls back** to the previous binary.
- Rollout is **opt-in per customer** with simple rings (canary node → rest). V1 may start with admin-triggered updates; automatic self-update with rollback is the target within M6.

### Security model
- **Outbound-only** (no inbound attack surface) · **TLS + pinned CA** · **signed binaries** · **scoped, revocable credentials** · **strict server-command allowlist** (the agent executes only the five known commands; anything else is rejected and audited) · **every action audited**.
- Workloads run as **containers, not on the host**. The agent runs with the **minimum privilege** required and the install **documents exactly why** (access to the containerd socket, GPU devices, and DCGM) — this transparency is the wedge through the enterprise security review.
- **Open-sourcing the agent is recommended** to make it auditable and defuse the "third-party root daemon" objection (the #1 GTM risk from the strategy review).

### What the agent will NOT do in V1 (explicit)
No inbound connections · no peer-to-peer / overlay / relaying of user traffic · **no scheduling decisions** (the control plane decides placement; the agent only executes) · **never kills processes it didn't launch** (idle on unmanaged usage = alert only) · no VM/Kata/Firecracker · no MIG/MPS partitioning management · no multi-tenant isolation beyond containers (single org) · no autoscaling · no secret storage beyond its own credential · no Windows · no non-NVIDIA GPUs · no host-level configuration changes.

---

# TASK 7 — API Design

Two surfaces: a **human/REST API** (JSON over HTTPS, session-bearer auth) consumed by the frontend, and an **agent gRPC service** (mTLS) consumed only by agents. All REST paths are under `/api/v1`. Requests/responses abbreviated to essential fields.

### Authentication
| Method | Path | Request | Response |
|---|---|---|---|
| POST | `/auth/login` | `{email, password}` | `{token, user{id,name,role}}` |
| POST | `/auth/logout` | — | `204` |
| GET | `/auth/me` | — | `{user, org}` |
| POST | `/enrollment-tokens` *(admin)* | `{expires_in, max_uses}` | `{token, install_command, expires_at}` |

### Organizations & Users
| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/org` | — | `{org, settings}` |
| PATCH | `/org` | `{settings:{idle_threshold_pct, idle_duration_min, currency, default_rate}}` | `{org}` |
| GET | `/users` | — | `[{id,email,role,status}]` |
| POST | `/users` *(admin)* | `{email, name, role}` | `{user, invite}` |
| PATCH | `/users/:id` *(admin)* | `{role?, status?}` | `{user}` |
| GET/POST | `/projects` | `{name}` | `{project}` / `[{project}]` |

### Nodes
| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/nodes` | `?status=` | `[{id,hostname,status,gpu_count,last_seen_at}]` |
| GET | `/nodes/:id` | — | `{node, gpus[]}` |
| DELETE | `/nodes/:id` *(admin)* | — | `204` (deregister + revoke credential) |

### GPUs
| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/gpus` | `?status=&node_id=` | `[{id,model,node_id,status,current_util_pct,mem_used_mb}]` |
| GET | `/gpus/:id` | `?from=&to=` | `{gpu, recent_metrics[]}` |

### Metrics
| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/metrics/summary` | — | `{gpu_total, gpus_idle, avg_util_pct, idle_cost_24h, workloads_running}` |
| GET | `/metrics/utilization` | `?scope=fleet|node|gpu&id=&from=&to=&interval=` | `{series:[{ts,util_pct,mem_pct}]}` |
| GET | `/metrics/idle` | `?from=&to=` | `{idle_gpus:[{gpu_id,idle_seconds,idle_cost}], total_idle_cost}` |

### Workloads
| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/templates` | — | `[{id,name,image,description}]` |
| GET | `/workloads` | `?status=&user_id=&project_id=` | `[{id,name,status,gpu_count,user,started_at}]` |
| POST | `/workloads` | `{name, template_id, gpu_type, gpu_count, project_id?, idle_timeout_sec?}` | `{workload, status:"pending"}` |
| GET | `/workloads/:id` | — | `{workload, endpoints{ssh,jupyter}, status}` |
| POST | `/workloads/:id/stop` | — | `{workload, status:"stopping"}` |
| GET | `/workloads/:id/logs` | `?tail=` | `{logs}` |

### Reporting
| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/reports/chargeback` | `?from=&to=&group_by=project|user&format=json|csv` | grouped `{rows:[{group, gpu_hours, amount}], total}` or CSV |
| GET | `/reports/utilization` | `?from=&to=` | `{avg_util, idle_hours, reclaimed_hours, top_users[]}` |
| GET/PUT | `/rate-cards` | `{gpu_model, rate_per_gpu_hour, currency}` | `[{rate_card}]` / `{rate_card}` |
| POST | `/reports/schedule` *(optional)* | `{type, cadence, recipients[]}` | `{schedule}` |

### Agent gRPC service (mTLS, agents only)
```
service AgentService {
  rpc Enroll(EnrollRequest) returns (EnrollResponse);      // {token, node_info, pubkey} -> {node_id, credential}
  rpc Connect(stream AgentMessage) returns (stream ServerMessage);  // persistent bidi stream
}
// AgentMessage  = Heartbeat | Inventory | MetricsBatch | WorkloadStatus | CommandResult
// ServerMessage = LaunchWorkload | StopWorkload | GetInventory | UpdateAgent
```

---

# TASK 8 — Repository Structure

### Choices and justification
- **Language:** **Go** for the control plane *and* the agent; **TypeScript** for the frontend. One backend language means shared protobuf types between agent and server, one hiring pool, and the agent ships as a single static binary with no runtime — decisive for "install in an afternoon." Go's goroutines fit the agent-gateway's many long-lived streams.
- **Backend framework:** Go standard library + **chi** (HTTP routing), **grpc-go** (agent gateway), **pgx** with **sqlc**-generated queries (typed SQL, no heavy ORM), **goose** (forward-only migrations).
- **Frontend framework:** **Next.js + React + TypeScript**, **TanStack Query** (server state), **Tailwind + shadcn/ui** (fast, consistent UI). One app, no SSR heroics.
- **Agent libraries:** **go-nvml**/DCGM bindings (metrics), **containerd** client + **NVIDIA Container Toolkit (CDI)** (workload runtime).
- **Repository strategy:** **Monorepo.** With 6 engineers and shared agent↔server contracts, a monorepo gives atomic cross-cutting changes, one CI, and trivial code sharing. Polyrepo's isolation buys nothing at this size and taxes every protocol change. Revisit only if the agent's release cadence must fully diverge.

### Directory structure
```
gpu-platform/                      # monorepo root
├── cmd/
│   ├── controlplane/main.go       # the monolith entrypoint (HTTP + gRPC listeners)
│   └── agent/main.go              # the agent binary entrypoint
├── internal/                      # control-plane modules (not importable outside)
│   ├── identity/                  # auth, users, sessions, enrollment tokens, agent creds
│   ├── fleet/                     # nodes, gpus, inventory, heartbeats
│   ├── telemetry/                 # metrics ingest, storage, rollups
│   ├── policy/                    # idle detection, reclaim, alerts
│   ├── workloads/                 # launch/stop lifecycle, first-fit placement, templates
│   ├── billing/                   # usage records, rate cards, cost records, reports
│   ├── audit/                     # append-only audit log
│   ├── agentgw/                   # gRPC AgentService: stream handling, command routing
│   └── platform/                  # shared: db (pgx), config, logging, errors, auth middleware
├── agent/                         # agent-only packages
│   ├── discovery/                 # NVML/nvidia-smi hardware enumeration
│   ├── metrics/                   # DCGM/NVML sampling + batching
│   ├── runtime/                   # containerd + CDI: run/stop containers
│   ├── transport/                 # outbound gRPC stream client, reconnect, reconcile
│   └── updater/                   # signed self-update + rollback
├── proto/                         # protobuf contracts (agent <-> control plane)
│   └── agent/v1/agent.proto
├── pkg/                           # genuinely shared libs (e.g., generated proto Go, api client)
├── web/                           # Next.js frontend
│   ├── app/                       # routes: /login, /nodes, /gpus, /workloads, /reports
│   ├── components/                # dashboard, charts, tables (shadcn/ui)
│   └── lib/                       # api client, TanStack Query hooks
├── migrations/                    # forward-only SQL migrations (goose)
├── deploy/
│   ├── docker-compose.yml         # self-host: controlplane + postgres + web
│   ├── install-agent.sh           # one-line agent installer
│   └── helm/                      # (deferred placeholder, not built in V1)
├── scripts/                       # dev tooling, codegen (sqlc, protoc)
├── docs/                          # onboarding, install, security packet
├── Makefile                       # build/test/codegen/migrate targets
├── go.mod
└── README.md
```

This layout makes the monolith's module boundaries physical (`internal/<module>`), keeps the agent's hardware-specific code isolated (`agent/`), and puts the one shared contract (`proto/`) where both sides import it — so the future extraction of `agentgw` is a `cmd/` split, not a refactor.

---

# TASK 9 — Implementation Plan

Six milestones on a critical path of ~18 weeks; with parallel tracks the team reaches a pilot in ~5 months, inside the "first customer in 6 months" gate. Effort is in engineer-weeks (the team has ~6 engineers + 2 founders who also build early).

### Milestone 1 — Agent registers node
- **Deliverables:** monorepo + CI scaffold; `proto` v1; control-plane skeleton with HTTP + gRPC listeners; human login; `POST /enrollment-tokens`; agent `enroll` + persistent outbound stream; `gpu_nodes` + credentials; dashboard *Nodes* list with online/offline.
- **Dependencies:** Postgres, repo scaffolding.
- **Risks:** **outbound connectivity through customer firewalls** (test against a deliberately locked-down node early); credential/mTLS plumbing.
- **Effort:** ~6 eng-weeks.

### Milestone 2 — GPU inventory visible
- **Deliverables:** agent hardware discovery (NVML/`nvidia-smi`); inventory push; `gpus` table; *Node detail* + *GPU list* UI.
- **Dependencies:** M1.
- **Risks:** driver/NVML variance across customer setups; MIG-mode nodes; stale inventory after hardware swaps.
- **Effort:** ~4 eng-weeks.

### Milestone 3 — Metrics pipeline
- **Deliverables:** DCGM/NVML sampling; `MetricsBatch` protocol; partitioned `gpu_metrics` ingest; hourly rollup worker; utilization dashboard (fleet/node/GPU time series); `/metrics/summary`.
- **Dependencies:** M2.
- **Risks:** metric volume/retention; DCGM not present on some nodes (NVML fallback); sampling overhead on the node.
- **Effort:** ~6 eng-weeks.

### Milestone 4 — Usage + reporting (idle & chargeback)
- **Deliverables:** `usage_records` generation; idle detection + idle-cost view; org idle settings; `rate_cards`; `cost_records`; chargeback report + CSV; utilization report + scheduled email.
- **Dependencies:** M3; `projects`.
- **Risks:** defining "idle" without false positives (duration + exclusions); untagged usage handling; rate configuration UX.
- **Effort:** ~6 eng-weeks.

### Milestone 5 — Workload launching + reclaim
- **Deliverables:** agent containerd/CDI runtime (run/stop a GPU container); `LaunchWorkload`/`StopWorkload`; first-fit placement; templates; SSH/Jupyter endpoint exposure; **idle auto-stop of managed workloads**; workload metering feeding `usage_records`.
- **Dependencies:** M2 (runtime), M4 (metering/idle).
- **Risks:** container GPU access correctness (CDI); endpoint reachability (LAN vs Tailscale); making auto-stop provably safe (only platform-launched workloads).
- **Effort:** ~8 eng-weeks.

### Milestone 6 — Pilot deployment
- **Deliverables:** self-host packaging (`docker-compose` + one-line agent install); hardening (reconnect/reconcile, error states, audit coverage, security pass); onboarding docs + security packet; deploy to the first design partner; feedback loop.
- **Dependencies:** M1–M5.
- **Risks:** real-world firewall/driver/hardware variance; security review; early support load.
- **Effort:** ~6 eng-weeks + ongoing.

---

# TASK 10 — Build Order (empty repo → first pilot)

Five parallel tracks: **A** Agent/runtime (E1, E2) · **B** Control plane + agent-gateway (E3, E4) · **C** Frontend (E5) · **D** Data/metrics + SRE/deploy (E6) · founders **F1** (product, design partners) and **F2** (security/architecture, codes early). Milestone completions marked ✅.

| Week | A — Agent | B — Control plane | C — Frontend | D — Data/SRE |
|---|---|---|---|---|
| **1** | Agent skeleton, config, outbound stream stub | Monorepo + CI, `proto` v1, HTTP/gRPC listeners | Next.js shell, auth pages, API client | Postgres + migrations harness, dev compose |
| **2** | `enroll` flow + keypair + credential store | Enrollment tokens, agent auth, `gpu_nodes` | Nodes list (online/offline) | CI gates, secrets/TLS, pinned-CA setup |
| **3** | Heartbeats + reconnect/backoff | Node API, audit-log baseline | Node detail page | **Locked-down-firewall connectivity test** → ✅ **M1** |
| **4** | Hardware discovery (NVML) | Inventory ingest, `gpus` table | GPU list view | Driver-variance test matrix |
| **5** | Inventory reconcile on reconnect | `/gpus` API, status logic | GPU detail + fleet overview | → ✅ **M2** |
| **6** | DCGM/NVML sampling + batching | `MetricsBatch` ingest endpoint | Utilization charts (scaffold) | `gpu_metrics` partitioning |
| **7** | Sampling tuning, NVML fallback | Rollup worker, `/metrics/summary` | Fleet/node/GPU time-series UI | Retention/partition-drop job |
| **8** | Metrics hardening | `/metrics/utilization` | Dashboard polish | → ✅ **M3** |
| **9** | — (supports B/D) | `usage_records` generation, `rate_cards` | Rate-card settings UI | Usage aggregation correctness tests |
| **10** | — | Idle detection + `/metrics/idle` | Idle panel + idle-cost UI | Idle false-positive tuning |
| **11** | — | `cost_records`, chargeback + CSV, report email | Chargeback + utilization report UI | Scheduled-report worker → ✅ **M4** |
| **12** | containerd + CDI: run/stop a GPU container | `LaunchWorkload`/`StopWorkload` dispatch | Launch form (templates) | Container GPU-access verification |
| **13** | Workload status + logs streaming | First-fit placement, templates API, lifecycle | Workload list + detail + logs | Endpoint exposure (LAN) + Tailscale doc |
| **14** | Idle-timeout auto-stop (managed only) | Workload metering → `usage_records` | Launch→endpoint happy path UX | Safe-auto-stop tests |
| **15** | Self-update + rollback (opt-in) | Reconcile running workloads on reconnect | Stop/relaunch flows | → ✅ **M5** |
| **16** | Install script hardening | Self-host packaging (compose) | Empty/error/loading states everywhere | One-line agent installer, deploy runbook |
| **17** | Security pass (least-priv, allowlist) | Audit coverage, rate limits, reconnect storms | Admin settings, onboarding flow | Backup/restore, log/metric retention |
| **18** | Internal dogfood on real GPUs | Bug bash | Bug bash | End-to-end on real hardware |
| **19** | Pilot support | Pilot fixes | Pilot fixes | **Deploy to design partner**, onboarding |
| **20** | Iterate on feedback | Iterate | Iterate | Stabilize → ✅ **M6 / first pilot** |

**Reading the plan:** visibility (M1–M3, weeks 1–8) ships before anything risky, so the team is demoing real utilization to design partners by ~week 8 — the fastest possible learning. Reclaim + chargeback (M4) lands the ROI story by ~week 11. Launching (M5) — the highest-risk track — comes only after the data foundation exists. Pilot hardening (M6) is the last three weeks. Founders run design-partner conversations from week 1 so a pilot site is ready when the software is.

---

## Success criteria check
| Criterion | Result |
|---|---|
| One coherent MVP | ✅ V1 = visibility → reclaim → self-service → chargeback, single org, one install |
| No future marketplace features | ✅ Explicitly excluded (Task 1 non-goals, Task 3 deferred) |
| No future cloud features | ✅ No multi-region/HA/multi-tenant/autoscaling in V1 |
| No speculative architecture | ✅ One monolith, one Postgres, bought connectivity; every deferral has a trigger |
| Every feature tied to customer value | ✅ Each module maps to a JTBD/persona (visibility, reclaim, launch, chargeback) |
| Clear path empty repo → first deployment | ✅ Week-by-week build order ending at a pilot (Task 10) |

**The discipline in one sentence:** build the agent and the utilization view first because they create *learning* and *trust* fastest; add reclaim and chargeback for the *ROI* story; add launch last because it is the riskiest; and buy or defer everything that does not move the first customer.





