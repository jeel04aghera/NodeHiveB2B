# Architecture — NodeHive (V1)

> This describes the system **as it is being built** (V1). For the 10-year north-star
> architecture see `GPU_Cloud_Platform_Technical_Design.md`; for what we deliberately
> chose *not* to build, see `Adversarial_Architecture_Review.md`; for implementation
> detail see `gpu-platform/docs/ENGINEERING_FOUNDATION.md`.

## Shape

```
  ┌─────────────┐   outbound gRPC stream    ┌──────────────────────────┐
  │   Agent     │ ─────────(TLS, :9090)────▶ │   Control Plane          │
  │ (GPU node)  │   Enroll + Connect         │   (Go modular monolith)  │
  └─────────────┘                            │                          │
        ▲  runs containers (later)           │  agentgw │ httpapi        │
        │                                    │  identity│ nodes          │
        │                                    │  telemetry│ policy        │      ┌────────────┐
        │                                    │  workloads│ billing │audit │ ───▶ │ PostgreSQL │
        │                                    └──────────────────────────┘      └────────────┘
                                                       ▲  REST (:8080)
                                                       │
                                                ┌──────────────┐
                                                │  Frontend     │
                                                │  (Next.js)    │
                                                └──────────────┘
```

The agent **always dials out**; the control plane never connects to the agent. The frontend
talks only to the REST API. One Postgres is the system of record.

## Components

**Agent** (`gpu-platform/agent/*`, `cmd/agent`). A single static Go binary on each GPU node.
Packages: `identity` (stable fingerprint + persisted credential), `transport` (the outbound
gRPC client), `discovery`/`metrics`/`executor` (interfaces; hardware enumeration, DCGM/NVML
sampling, containerd+CDI runtime — filled in by later tickets). It enrolls once, stores its
credential, then holds one persistent `Connect` stream sending heartbeats (and, later,
inventory + workload status) and receiving launch/stop commands.

**Control plane** (`gpu-platform/cmd/controlplane`, `internal/*`). One process, one database.
Two listeners: **`agentgw`** (gRPC, terminates agent streams) and **`httpapi`** (REST for the
frontend). Business logic is split into modules — `identity`, `nodes` (the V1 enroll/query
service), `inventory`, `telemetry`, `policy`, `workloads`, `billing`, `audit` — each exposing
a `Service` interface and owning its own tables. Modules collaborate through interfaces and an
in-process event bus (`internal/platform/events`), never by reaching into each other's tables.

**PostgreSQL** (`gpu-platform/migrations/0001_init.sql`). System of record. Every table carries
`org_id` from day one (multi-org becomes config, not a rewrite). Telemetry tables
(`gpu_metrics`, `agent_heartbeats`) are range-partitioned by time so retention is a
partition-drop. `usage_records` is immutable/append-only — the metering source of truth.

**Frontend** (`gpu-platform/web`). Next.js + TanStack Query. Calls the REST API only; never the
database or gRPC.

## Key decisions (and why)

- **Outbound-only agent** — solves NAT/firewall traversal and is far easier to get through an
  enterprise security review than an inbound daemon. This is the architecture's load-bearing idea.
- **Bearer credential now, mTLS + pinned-CA later** — the slice issues a random credential
  (stored hashed); the proto already carries `public_key` for the real signed-credential model.
- **Single Postgres** — no ClickHouse/Citus/sharding until measured load demands it.
- **First-fit placement, not a scheduler** — V1 is single-org/low-contention; for Kubernetes
  customers we adopt NVIDIA **KAI/DRA** rather than building a scheduler.
- **Buy connectivity** (Tailscale/Cloudflare Tunnel) — we do not build an overlay or relay.
- **Containers only** — containerd + NVIDIA Container Toolkit (CDI); no Kata/KVM/Firecracker yet.

## Enroll data flow (the working slice)
1. Agent computes a fingerprint (machine-id + hostname) and generates a local key.
2. Agent calls `AgentService.Enroll(token, fingerprint, node info, public_key)`.
3. Control plane validates+consumes the enrollment token, upserts the node by fingerprint, and
   stores a credential — **all in one transaction**. Returns `node_id` + credential.
4. Agent persists the credential, opens `Connect` with `authorization: Bearer <credential>`.
5. A stream interceptor authenticates the credential and injects the node identity.
6. Each heartbeat updates `last_seen_at`/status and appends an `agent_heartbeats` row.
7. `GET /api/v1/nodes` returns the node.

## Deferred (with the trigger that revisits it)

| Deferred | Revisit when |
|---|---|
| Overlay / relay (use Tailscale) | A pilot's nodes are unreachable even with Tailscale |
| Custom scheduler (gang/DRF/preempt) | Real multi-tenant contention; else KAI/DRA + first-fit |
| Kata/KVM isolation, MIG/MPS UI | A regulated/multi-tenant customer needs hard isolation |
| ClickHouse / Kafka / sharding | Postgres metrics/usage tables actually hurt (measure) |
| Multi-region / HA | A signed SLA requires it |
| SSO/SAML/SCIM | First enterprise deal requires it → adopt WorkOS |
| Marketplace / multi-org | After the owned-GPU control plane is proven sticky |
