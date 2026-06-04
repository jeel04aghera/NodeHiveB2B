# GPU Cloud Platform — Technical Design Document

**Codename:** GPU Cloud OS ("Private AWS for GPU Infrastructure")
**Document type:** Engineering build blueprint
**Audience:** Founding engineering team
**Status:** v1 — first-principles architecture
**Date:** 2026-06-01

---

## How to read this document

This is written as a build blueprint, not a pitch. It takes positions and defends them, names the things you must get right early, and is explicit about what to deliberately *not* build yet. Where I disagree with the stated vision — most sharply on Phase 5 (decentralized consumer compute) — I say so and explain why, because that is the job of a technical co-founder.

Three ideas recur and are worth stating up front, because they shape every later decision:

1. **This is a distributed control plane over hardware you do not own and cannot fully trust.** That single framing dictates the agent model, the networking design, and the security model. It is the thing that makes this *not* "just another Kubernetes wrapper."
2. **The hard part is not launching a container with a GPU. The hard part is reaching that container** when it lives on a workstation under someone's desk, behind a corporate NAT, with a dynamic IP. Most of the engineering moat is in networking, the agent, and the scheduler — not the dashboard.
3. **Build Phase 1 in a way that *is* the marketplace substrate, but do not bet the company on Phase 5.** The good news, defended in §16, is that a well-built Phase 1 already contains 80% of what a marketplace needs. The bad news is that "idle consumer GPUs, location-agnostic" is a much narrower and harder business than it sounds.

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Product Architecture](#2-product-architecture)
3. [High-Level System Design](#3-high-level-system-design)
4. [Detailed Component Design](#4-detailed-component-design)
5. [Technology Selection](#5-technology-selection)
6. [Control Plane Design](#6-control-plane-design)
7. [Data Plane Design](#7-data-plane-design)
8. [Scheduler Design](#8-scheduler-design)
9. [Agent Design](#9-agent-design)
10. [Database Design](#10-database-design)
11. [Networking Design](#11-networking-design)
12. [Security Design](#12-security-design)
13. [Observability Design](#13-observability-design)
14. [Billing Design](#14-billing-design)
15. [Scalability Strategy](#15-scalability-strategy)
16. [Future Marketplace Evolution](#16-future-marketplace-evolution)
17. [Risk Analysis](#17-risk-analysis)
18. [MVP Roadmap](#18-mvp-roadmap)
19. [Team Structure](#19-team-structure)
20. [Final Recommended Architecture](#20-final-recommended-architecture)

---

## 1. Executive Summary

### 1.1 What we are building

A control plane that turns GPU hardware an organization already owns — workstations, on-prem servers, RTX 4090 / RTX 6000 Ada / L40S / A100 / H100 clusters — into a self-service cloud that feels like AWS EC2. An employee logs in, picks a GPU/CPU/RAM/storage/image, clicks **Launch**, and within seconds gets an SSH endpoint, a Jupyter URL, and a VSCode URL. The platform meters usage and produces chargeback reports per team and project.

The wedge is not "cheaper GPUs." It is **"you already paid for the GPUs; we give you the cloud experience on top of them."** That reframing matters because it changes the buyer (the platform/IT team that owns idle hardware) and removes the need to win on price against hyperscalers on day one.

### 1.2 The core technical insight

Every incumbent GPU cloud (RunPod, Vast.ai, Lambda, CoreWeave) owns or leases the hardware and the network it sits in. We are choosing the harder problem on purpose: **orchestrating compute on nodes that live inside other people's networks**. Those nodes are behind NAT, have no inbound public IP, run heterogeneous drivers and kernels, and may be powered off at any moment.

This has one dominant architectural consequence that echoes through the entire document:

> **The agent must dial out to us. We must never need to dial in.**

Everything — provisioning, SSH, Jupyter, metrics, logs, upgrades — must work over a connection the node initiated. This is the same model as Tailscale, Cloudflare Tunnel, and GitHub Actions runners, and it is the single most important design decision in the platform.

### 1.3 Recommended stack (defended in §5)

- **Control plane & agent:** Go. Not by default — because the cloud-native ecosystem (gRPC, containerd, NVML bindings, Prometheus, NATS, etcd) is Go, the single-static-binary deployment model is perfect for an agent that must run on arbitrary customer Linux boxes, and goroutine concurrency fits a control plane that holds 100k+ long-lived agent connections.
- **Performance-critical edges:** Rust, carved out deliberately for the networking data plane (the relay/proxy that carries SSH and HTTP traffic) and any agent hot path. Not for the whole backend — the velocity cost is not worth it where Go is adequate.
- **Frontend:** Next.js + React + TypeScript, with a documented escape hatch to a plain Vite SPA for the authenticated console (see §5.1 — the console is not a content site and you can end up fighting Server Components).
- **Data plane:** Tiered isolation — containers (containerd + NVIDIA Container Toolkit) for same-org trust, Kata Containers or full KVM/QEMU VMs with VFIO GPU passthrough for cross-tenant isolation. **Firecracker is explicitly rejected for GPU instances** because it still has no GPU passthrough as of 2026.
- **Orchestration:** A custom control plane + lightweight agent (Nomad-inspired), **not** Kubernetes-on-customer-hardware. We run *our own* services on Kubernetes; we do not force K8s onto a customer's random workstations. We borrow Kubernetes' DRA scheduling concepts and offer a K8s *integration* mode for customers who already have clusters. Defended in §7.
- **System of record:** PostgreSQL. **Metering:** append-only event stream (NATS JetStream → ClickHouse, Kafka/Redpanda at scale). **Ledger:** double-entry from day one.
- **Networking:** WireGuard overlay + regional relay/gateway fleet; SSH and HTTP reach instances through relays over the agent's outbound tunnel.
- **Observability:** OpenTelemetry as the unifying standard; Prometheus/VictoriaMetrics + Loki + Tempo + Grafana; DCGM for GPU health. Agents *push* (remote_write/OTLP) because you cannot pull across NAT.

### 1.4 The honest strategic read

Phase 1 → Phase 3 (org cloud → multi-org SaaS → curated provider marketplace) is a real, large, fundable business and the architecture below is designed for it. **Phase 5 (location-agnostic decentralized consumer compute) is the part I would push back on hardest.** It is a graveyard for good reasons — consumer GPUs lack ECC and fast interconnect, serious training needs co-located NVLink/InfiniBand, and no enterprise will run sensitive data on a stranger's PC. Build the architecture so Phase 5 *remains possible*, but do not let the business *depend* on it. Full argument in §16.

### 1.5 What you must get right early (and what you can defer)

| Get right from day one (expensive to retrofit) | Safe to defer (cheap to add later) |
|---|---|
| Outbound-only agent + overlay networking | Fractional GPU (MIG/MPS/time-slicing) |
| Org → Team → Project data model + row-level tenant scoping | Full VM / Kata isolation |
| Append-only usage events + double-entry ledger | Marketplace, payouts, pricing engine |
| mTLS service/agent identity (SPIFFE-style) | Multi-region, cell-based sharding |
| Instance lifecycle as an explicit state machine | Advanced bin-packing & preemption |
| Audit log (immutable, hash-chained) | SAML/SCIM (OIDC is enough for design partners) |

---

## 2. Product Architecture

### 2.1 First-principles decomposition

Start from the user's job-to-be-done — *"give me a GPU box I can work on, and tell my finance team what it cost"* — and derive the capabilities the system must have. Nothing here assumes an implementation yet.

1. **Know what hardware exists and its live state.** You cannot place a workload without an accurate, real-time inventory of GPUs, their VRAM, CPU, RAM, disk, driver/kernel versions, topology (NVLink/PCIe), and current free capacity. → *Inventory service + an agent on every node.*
2. **Decide where a request runs.** Given a request ("1× H100, 32 vCPU, 128 GB RAM, 500 GB disk, PyTorch image") and the inventory, choose a node and reserve resources, honoring quotas, priorities, and fairness. → *Scheduler.*
3. **Make the runtime real.** Pull the image/template, attach the GPU, set up storage and networking, boot the container/VM, expose sshd/Jupyter/VSCode. → *Data plane, driven by the agent.*
4. **Connect the human to it.** Across NAT, with no inbound public IP, securely. → *Networking / relay / ingress plane.* (The hard one.)
5. **Measure what was used.** GPU-seconds, CPU, RAM, storage-GB-hours, egress, per instance, attributed to org/team/project. → *Metering pipeline.*
6. **Charge or show-back.** Apply a price book, produce reports, and (later) move real money. → *Billing + ledger.*
7. **Control who can do what.** Org/team/project hierarchy, RBAC, SSO, isolation between tenants. → *Identity + authz + multi-tenancy.*
8. **See and debug the whole thing.** Metrics, logs, traces, alerts — including on nodes you don't physically control. → *Observability.*

These eight capabilities are the spine. Every service in §6–§14 maps back to one of them.

### 2.2 The three personas and their surfaces

The product is three dashboards over one platform, separated by role, not by codebase:

- **Developer / researcher (the end user):** launches and manages instances, opens Jupyter/VSCode/SSH, sees their own usage. This is the EC2-like console. Optimize for *time-to-first-prompt* — login to a running GPU in under 60 seconds.
- **Organization admin (the buyer):** manages teams, projects, quotas, images/templates, sees fleet utilization and chargeback by cost center, onboards nodes (the "curl | sh" agent install), sets policy. This persona is who you actually sell to; their dashboard is where retention lives.
- **Platform operator (you, then your customers' platform teams):** fleet health, node enrollment, scheduler behavior, incidents, audit. In Phase 1 this is largely internal; by Phase 3 it becomes the provider/operator console.

### 2.3 Core user workflow (the EC2 parallel, made precise)

```
Login (OIDC/SSO)
  → Select project (sets quota + billing attribution + network scope)
  → Configure instance:
        GPU type & count, CPU, RAM, disk size, OS/image/template, region/zone
  → Launch
        → Scheduler admits & places → Agent provisions → Networking wires endpoints
  → Receive: ssh string, Jupyter URL, VSCode URL, web terminal, public/private addr
  → Work (train / infer / render / engineer)
  → Stop / Terminate (or auto-stop on idle — a key cost-control feature)
  → Usage flushed to metering → appears in chargeback report
```

Two product decisions worth making explicit because they have outsized technical impact:

- **Idle auto-stop is a first-class feature, not an afterthought.** The #1 complaint about internal GPU clouds is "someone left a $40k/mo H100 box idle over the weekend." A reliable idle detector (GPU utilization + session activity, with a grace period and warning) is one of the highest-ROI features you can ship and it directly drives the chargeback story.
- **Templates are the product, not the OS.** Users don't want "Ubuntu." They want "PyTorch 2.x + CUDA 12.x + JupyterLab, ready." Treat templates (curated, versioned, scannable images) as a managed catalog with org-private additions. This is also a security control point (§12).

### 2.4 Positioning and the boundary of the product

"Private AWS for GPU" is the right framing for the buyer. Internally, hold a sharper definition: **we are the control plane and access layer for GPU compute the customer already operates.** We are explicitly *not*, in Phase 1:

- a model-training framework (we run theirs),
- an MLOps/experiment-tracking product (we integrate, we don't compete with W&B),
- a hardware vendor or colo provider.

Saying no to these keeps the surface area survivable for a small team and keeps the wedge sharp.

## 3. High-Level System Design

### 3.1 The two-plane model

The system splits cleanly into a **control plane** (decides, records, coordinates — lives in *our* cloud) and a **data plane** (executes — lives on *customer* hardware). This is the same split AWS, Kubernetes, and Nomad use, and it is what lets one small control plane manage a huge, untrusted, geographically scattered fleet.

```
                        ┌───────────────────────────────────────────────────────┐
                        │                    OUR CLOUD (control plane)            │
   ┌─────────┐          │  ┌──────────┐  ┌───────────┐  ┌───────────┐            │
   │ Browser │──HTTPS──▶│  │   API     │  │   AuthN/   │  │  Inventory │            │
   │ console │  (BFF)   │  │  Gateway  │─▶│   AuthZ    │  │  Service   │            │
   └─────────┘          │  └────┬─────┘  └───────────┘  └─────┬─────┘            │
                        │       │                              │                  │
   ┌─────────┐          │  ┌────▼─────┐  ┌───────────┐  ┌─────▼─────┐  ┌────────┐│
   │   CLI    │──gRPC───▶│  │Scheduler │  │  Billing  │  │  Metering  │  │ Audit  ││
   └─────────┘          │  │ (leader) │  │ + Ledger  │  │ Aggregator │  │  Log   ││
                        │  └────┬─────┘  └───────────┘  └─────▲─────┘  └────────┘│
                        │       │            ┌──────────┐     │                   │
                        │   ┌───▼────────────▼──────┐   │     │  PostgreSQL /      │
                        │   │  Command Bus (NATS)    │   │     │  ClickHouse /      │
                        │   │  + Relay Coordination  │   │     │  Redis / Object    │
                        │   └───┬────────────────────┘   │     │  store             │
                        └───────┼────────────────────────┼─────┼───────────────────┘
                                │  (agent dials OUT)      │     │ push metrics/logs
              ╔═════════════════▼═════════════════════════▼═════▼═══════════════════╗
              ║              REGIONAL RELAY / GATEWAY FLEET (edge)                   ║
              ║   WireGuard termination · SSH/TCP proxy · HTTPS reverse proxy        ║
              ╚═════════════════▲════════════════════════════════════════════════════╝
                                │  outbound mTLS tunnel (QUIC/gRPC + WireGuard)
   ┌────────────────────────────┼───────────────────────────────────────────────┐
   │            CUSTOMER NETWORK (data plane — behind NAT/firewall)               │
   │   ┌────────────┐   ┌────────────┐   ┌────────────┐   ┌────────────┐         │
   │   │  Agent +   │   │  Agent +   │   │  Agent +   │   │  Agent +   │         │
   │   │ containerd │   │ containerd │   │   KVM/Kata │   │ containerd │  ...    │
   │   │  [RTX4090] │   │ [A100 x8]  │   │  [H100 x8] │   │[RTX6000Ada]│         │
   │   └────────────┘   └────────────┘   └────────────┘   └────────────┘         │
   └──────────────────────────────────────────────────────────────────────────────┘
```

### 3.2 Why these specific boundaries

- **The control plane never initiates a connection to a node.** All node communication rides the agent's outbound tunnel, multiplexed over the relay fleet. This is what makes the platform work across NAT/firewalls without asking the customer to open inbound ports (a non-starter for enterprise security teams).
- **The relay fleet is a distinct tier from the control-plane services.** Relays are on the *data path* (they carry SSH bytes and HTTP requests); control-plane services are on the *decision path*. They scale independently and fail independently. Conflating them is a classic mistake that couples a bandwidth problem to a database problem.
- **Metering is a one-way push, separate from the command bus.** Usage events flow node → relay → metering aggregator as an append-only stream. They must survive control-plane restarts and never block provisioning. Keeping metering off the critical command path means a billing outage never stops a researcher from launching a job, and a provisioning outage never loses a billing event.

### 3.3 Request lifecycle (launch an instance), end to end

1. Console calls `POST /v1/instances` through the API gateway; authn (OIDC) and authz (RBAC + quota) pass.
2. API writes an `Instance` row in state `PENDING` (the durable source of truth) and emits a `schedule.requested` event.
3. Scheduler filters the live inventory for feasible nodes, scores them, picks one, and writes an `Assignment` (optimistic concurrency on the node's resource version — §8).
4. Command bus delivers the assignment to the target node's agent over its outbound stream.
5. Agent performs node-level admission (still has the capacity?), pulls the image, attaches the GPU (NVIDIA toolkit / VFIO), creates storage and the network namespace, boots the workload, starts sshd/Jupyter/VSCode.
6. Agent reports `RUNNING` with endpoint details; networking plane allocates the overlay address and registers the per-instance hostnames at the relay/proxy.
7. API transitions the `Instance` to `RUNNING`; console shows the SSH string and URLs; metering starts counting from the authoritative `RUNNING` timestamp.

Failure at any step is a state transition, not an exception — see the instance state machine in §4.3 and scheduler failure handling in §8.6.

---

## 4. Detailed Component Design

### 4.1 Service inventory (control plane)

| Service | Responsibility | Consistency need | Notes |
|---|---|---|---|
| **API Gateway / BFF** | AuthN, request validation, rate limiting, fan-out to services; WebSocket/SSE for realtime | Stateless | The only public ingress for the console/CLI. gRPC internally, REST+WS externally. |
| **Identity & AuthZ** | Users, orgs, teams, projects, roles, sessions, OIDC/SAML/SCIM, policy decisions | Strong | Backed by Postgres; policy via Cedar/OPA (§12). |
| **Inventory** | Node registry, live capacity, GPU/topology facts, health | Strong-ish (recent) | Hot state in memory + Redis; durable in Postgres. Source of truth for the scheduler. |
| **Scheduler** | Admission, placement, quotas, fairness, preemption, reservations | Strong (single writer) | Leader-elected; one active per region/cell. §8. |
| **Provisioning orchestrator** | Drives agents through provision/teardown; reconciles desired vs actual | Eventual + reconcile | Owns the instance state machine; idempotent, retry-safe. |
| **Metering aggregator** | Ingests usage events, dedupes, aggregates to billable records | Append-only, exactly-once-effective | Off the critical path; §14. |
| **Billing + Ledger** | Rating, chargeback/invoices, double-entry ledger, (later) payouts | Strong, auditable | The money. Double-entry from day one. §14. |
| **Audit** | Immutable, hash-chained log of every privileged action | Append-only, tamper-evident | Separate store; WORM-capable for compliance. |
| **Relay coordinator** | Assigns agents/instances to relays, hands out keys/routes | Strong-ish | Control logic for the data-path relays. |
| **Notification/events** | Webhooks, emails, in-app events | Eventual | Driven off the event bus. |

Phase 1 reality check: these are *logical* services, not eight separate deployments on day one. Start as a **modular monolith** — one Go binary, clean package boundaries, one Postgres — and split out the scheduler, metering, and relay coordinator first when load demands it (§15). Microservices on day one for a 5-person team is premature and will halve your velocity.

### 4.2 Components on the node (data plane)

- **Agent** (Go, single static binary): the only thing we install. Owns enrollment, inventory reporting, heartbeats, command execution, provisioning, log/metric shipping, self-upgrade, network tunnel. §9.
- **Container runtime:** containerd + NVIDIA Container Toolkit (CDI mode). The agent talks to containerd directly via its API — we do not require Docker Engine.
- **VM runtime (when isolation requires it):** QEMU/KVM or Kata Containers with VFIO GPU passthrough. §7.
- **Network datapath:** WireGuard interface + a lightweight userspace proxy for per-instance routing; eBPF/nftables for tenant isolation. §11.
- **Local exporters:** DCGM exporter (GPU), node_exporter (host), scraped locally by the agent and pushed out (§13).

### 4.3 The instance state machine (get this right early)

Treat an instance as an explicit, durable state machine. This single design choice prevents an entire class of "ghost instance / double-bill / leaked GPU" bugs.

```
            ┌─────────┐  admit+place   ┌───────────┐  agent ack   ┌──────────────┐
  create──▶ │ PENDING │ ─────────────▶ │ SCHEDULED │ ───────────▶ │ PROVISIONING │
            └────┬────┘                └─────┬─────┘              └──────┬───────┘
                 │ reject/timeout            │ node reject               │ ready
                 ▼                           ▼ (reschedule)              ▼
            ┌─────────┐                                          ┌──────────────┐
            │ FAILED  │◀───────────────────────────────────────│   RUNNING     │
            └─────────┘    error / health-fail                  └──┬────────┬──┘
                 ▲                                                  │ stop   │ idle-timeout
                 │                                                  ▼        ▼
                 │                                            ┌──────────┐ (warn→stop)
                 └────────────────────────────────────────── │ STOPPING │
                              terminate                       └────┬─────┘
                                                                   ▼
                                                            ┌──────────┐  reclaim  ┌──────────┐
                                                            │ STOPPED  │ ────────▶ │TERMINATED│
                                                            └──────────┘           └──────────┘
```

Rules that matter:

- **Billing is driven by transitions into and out of `RUNNING`,** cross-checked against agent heartbeats — never by a single source (§14.4).
- **Every transition is persisted before the side effect is attempted,** so a control-plane crash mid-provision is recoverable by reconciliation (desired state in DB vs actual state reported by agent).
- **`STOPPED` retains storage and identity; `TERMINATED` reclaims everything.** This is the EC2 stop-vs-terminate distinction and users will expect it.

---

## 5. Technology Selection

This section takes positions. The brief asked me not to default to popular choices without justification, so each call below leads with the reason, not the name.

### 5.1 Frontend: Next.js + React + TypeScript — yes, with one caveat

**TypeScript: non-negotiable.** A platform with this much domain modeling (instances, quotas, roles, billing) needs static types end-to-end. You will also share types between the API and the frontend (generate TS clients from the gRPC/OpenAPI schema), which removes a whole class of drift bugs.

**React: yes.** Hiring pool, component ecosystem, and the realtime/interactive nature of a cloud console all point to React. No serious competitor (Vue, Svelte, Solid) is *wrong*, but React is the lowest-risk choice for a team you need to grow.

**Next.js: yes, but know what you're using it for.** The caveat is real and worth internalizing: a GPU cloud console is a **heavily authenticated, realtime, client-state-rich application** — closer to a trading terminal than a content site. Next.js's marquee feature, React Server Components with server-side data fetching, fights you here: almost everything is behind auth, depends on live WebSocket state, and re-renders on events. Teams routinely adopt Next.js and then end up running the console as a client-side app *inside* Next.js, getting the framework's complexity without its benefit.

Recommendation: **Use Next.js for the public surface (marketing, docs, login/onboarding, billing pages) and the app shell, and build the live console as a client-rendered experience** (App Router with client components, or honestly a route group that's effectively an SPA). Keep a documented escape hatch: if the team finds itself fighting Server Components for the console, a plain **Vite + React SPA served as static assets behind the Go API** is a legitimate, simpler choice and you lose very little. Do not be dogmatic about SSR for screens only ten authenticated people will ever see.

**Frontend architecture specifics:**

- **Server state:** TanStack Query (React Query). It is the correct tool for cache, revalidation, and optimistic updates against the API. Do *not* hand-roll fetch + useEffect.
- **Client state:** keep it small. Zustand for the little global UI state that exists (selected project, theme, panel layout). Reach for Redux Toolkit only if client state genuinely grows complex — most of this app's state is *server* state, which belongs in TanStack Query, not Redux.
- **Realtime:** instance status, logs, GPU metrics, and the web terminal need live updates. Use **WebSocket** for bidirectional streams (terminal, logs) and **SSE** for simple server-push (status changes) where you don't need a client→server channel. Multiplex through the API gateway; do not open a socket per widget. Reconcile realtime events into the TanStack Query cache so the UI has one source of truth.
- **Permissions in the UI:** the frontend renders affordances based on the user's effective permissions (fetched once, cached), but **the server is the only authority.** Never trust client-side role checks for anything but hiding buttons. Every API call is authorized server-side against the same policy engine (§12).
- **Web terminal / Jupyter / VSCode:** these are iframes/embeds pointed at per-instance hostnames that resolve to the relay/reverse-proxy (§11). The terminal is xterm.js over a WebSocket proxied to the instance's PTY.

### 5.2 Backend language: the real comparison

This is the decision the brief most wanted interrogated, so here is the full matrix, scored for *this* workload (a concurrency-heavy control plane + a single-binary agent on untrusted hardware), not in the abstract.

| Criterion | **Go** | **Rust** | **Node/TS** | **Java/JVM** | **Python** |
|---|---|---|---|---|---|
| Raw perf / latency | High, GC pauses sub-ms with modern Go | Highest, no GC | Medium | High (after warmup) | Low |
| Concurrency model | Goroutines — ideal for 100k+ long-lived conns | async/await, excellent but harder | Event loop, single-threaded CPU | Threads + virtual threads (Loom) | GIL — poor for CPU concurrency |
| Memory footprint | Low | Lowest | Medium | High (JVM heap) | High |
| Single-binary deploy (agent!) | **Excellent** (static cross-compile) | **Excellent** (static) | Poor (needs runtime) | Poor (needs JVM) | Poor (needs interpreter) |
| Cloud-native ecosystem | **Best in class** (k8s, containerd, gRPC, NATS, NVML, Prometheus all Go) | Growing, good | Good (web), weak (infra) | Good | ML-only |
| Dev velocity / iteration | High | Lower (borrow checker, compile times) | Highest | Medium | High |
| Hiring pool (infra eng) | Large & growing | Smaller, expensive | Largest overall | Large | Large (but ML, not infra) |
| Memory safety | GC-safe | **Compile-time guaranteed** | GC-safe | GC-safe | GC-safe |
| Maintainability at team scale | High (boring on purpose) | Medium (steep curve) | Medium (footguns) | High | Low at scale |

**Recommendation — and the reasoning:**

- **Go for the control plane and the agent.** The decisive factors are not generic "Go is fast." They are: (1) the agent must be a single static binary that drops onto any customer Linux box with zero runtime dependencies, and Go cross-compiles to exactly that; (2) the entire ecosystem you will integrate — containerd, NVML/DCGM bindings, gRPC, NATS, Prometheus, etcd — is Go-native, so you are not the first person to call these APIs from your language; (3) a control plane's defining workload is *fan-out concurrency over many slow connections* (100k agents each holding a stream), which is precisely what goroutines are built for; (4) GC pause is a non-issue because the control plane is on the *decision* path, not the *GPU compute* path — nanoseconds don't matter when you're deciding where a 6-hour training job runs.

- **Rust, deliberately and narrowly, for the networking data plane and agent hot paths.** The relay/proxy that carries SSH and HTTP bytes *is* on the data path and benefits from no-GC determinism and memory safety on an internet-facing, untrusted-input surface. Vector (the log shipper) is already Rust and a good fit. Carve Rust out where the performance and safety genuinely pay for the velocity hit — not everywhere.

- **Node/TypeScript only for the frontend and its BFF.** Sharing types with the React app is the one real win, and you'll have TS expertise on the team anyway. Do not build the scheduler or agent in Node — the single-threaded event loop and the deployment story are both wrong for this.

- **Python stays in its lane:** internal data science, the analytics/reporting batch, ML-template tooling, and customer-facing SDKs. **Never** on the control-plane hot path. (You *will* ship a Python SDK because your users are ML engineers — that's a client library, not a service.)

- **Java/JVM: no.** Nothing here justifies the operational weight and memory footprint, and the agent deployment story is disqualifying. The one place it would be defensible — a JVM-based stream processor for metering at massive scale (Flink) — is a Stage-C decision you can make later, in isolation, without it touching the rest of the stack.

**The honest counter-argument** (because a co-founder should state it): an all-Rust shop gets memory safety and determinism *everywhere*, which on an untrusted-input, security-sensitive platform is genuinely attractive, and some elite infra teams (e.g., parts of Cloudflare, Fly.io) lean Rust hard. The reason I still recommend Go-primary is **velocity at the founding stage**: you will rewrite the scheduler three times before product-market fit, and Rust's compile-iterate loop and hiring scarcity tax every one of those rewrites. Go lets a small team move fast and stay correct enough; Rust is the right *second* language for the parts where "correct enough" isn't. If your founding team is already Rust-fluent and Go-averse, that changes the calculus — pick the language your team will be fastest in, and apply the data-path/control-path split regardless.

### 5.3 Supporting technology choices (summary; details in later sections)

| Concern | Choice | Why (one line) |
|---|---|---|
| RPC | gRPC (+ grpc-gateway for REST) | Streaming for the agent channel; codegen for typed clients |
| System of record | PostgreSQL | Relational, strong consistency, mature; the boring correct choice |
| Event/command bus | NATS JetStream (Phase 1) → Kafka/Redpanda (scale) | Go-native, light, great for agent command bus; Kafka for metering firehose later |
| Metering store | ClickHouse | Columnar, cheap high-cardinality time-series for billing-grade events |
| Cache/sessions | Redis | Obvious |
| Object storage | MinIO (self-host) / S3 (cloud) | S3-compatible everywhere; §storage |
| Secrets | HashiCorp Vault / cloud KMS | Dynamic secrets, envelope encryption |
| Policy | Cedar or OPA | Externalized authz; auditable |
| Overlay network | WireGuard | Fast, in-kernel, simple, audited |
| Observability | OpenTelemetry + Prometheus/VictoriaMetrics + Loki + Tempo + Grafana + DCGM | One standard, push-based across NAT |
| Our-cloud orchestration | Kubernetes | For *our* services — not customer nodes |

## 6. Control Plane Design

### 6.1 Service boundaries and why they are drawn here

The boundary test is: *what fails together should live together; what scales differently should split.* Applying that:

- **Identity/AuthZ is its own bounded context** because it has a different consistency and security posture (strong consistency, the highest blast radius if compromised) and a different change cadence (slow, careful) than everything else. It is a dependency of every other service but depends on none of them.
- **Inventory and Scheduler are tightly coupled but distinct.** Inventory is read-heavy, write-frequent (every heartbeat updates capacity); the scheduler is the single writer of *placement decisions*. They share data but have opposite access patterns, so the scheduler keeps an in-memory projection of inventory and the inventory service owns durability. Splitting them lets inventory scale reads (replicas, cache) without contending with the scheduler's write path.
- **Provisioning orchestrator is separate from the scheduler** because scheduling is a fast, in-memory decision and provisioning is a slow, failure-prone, long-running saga (pull a 15 GB image, boot a VM). Coupling them would make the scheduler's latency hostage to image pulls. The scheduler decides *where*; the orchestrator makes it *real* and reconciles.
- **Metering and Billing are downstream and isolated** so that money-path bugs and load never touch the provisioning-path. Metering ingests a firehose; billing is transactional and audited. Different stores, different SLAs.
- **Audit is write-only from everyone, read-only for humans,** and physically separated so that a compromise of any service cannot rewrite history.

### 6.2 Inter-service communication

- **Synchronous, request/response:** gRPC between services (typed, streaming-capable, fast). The API gateway is the only thing translating external REST/WS to internal gRPC.
- **Asynchronous, event-driven:** NATS JetStream as the backbone. Provisioning, metering, notifications, and audit are all event consumers. This decouples services and gives you replay, which is invaluable for reconciliation and debugging.
- **The agent command channel** is a special case: a long-lived bidirectional gRPC stream from agent → relay → command bus, described in §9. Commands to a node are published to a per-node subject; the agent's stream subscribes.

### 6.3 Consistency model

Be deliberate about where you spend strong consistency, because it is expensive at scale:

- **Strong (single-writer / transactional):** identity, RBAC, quotas, the instance state machine, the ledger. These are correctness-critical and relatively low-throughput. Postgres transactions.
- **Read-recent (bounded staleness):** inventory/capacity. A scheduler acting on 2-second-stale capacity is fine because it confirms with the node at admission time (optimistic concurrency, §8.3). Demanding strong consistency on capacity would serialize the whole fleet through one lock.
- **Eventual:** metrics, logs, notifications, derived analytics. These can lag and recover.

This tiering is the difference between a platform that scales and one that melts under a global lock. It is also why the scheduler can be fast: it does not ask the database "is this node free?" on the hot path; it asks its in-memory projection and reconciles on commit.

---

## 7. Data Plane Design

The data plane is where a workload actually runs on a node. The central question is **isolation vs. density vs. speed**, and the right answer is *tiered* — different tenancy and trust levels get different runtimes. Let me compare the options against our actual threat model (multi-tenant, eventually untrusted, GPU-attached) rather than in the abstract.

### 7.1 Runtime options compared

| Runtime | Isolation | GPU support | Startup | Density | Right for us when |
|---|---|---|---|---|---|
| **Docker / containerd + NVIDIA toolkit** | Namespaces + cgroups, **shared kernel** | Excellent (CDI, mature) | ~1 s | Highest | Same-org / trusted tenants; the Phase-1 default |
| **Kubernetes (as node substrate)** | Same as containers (it *is* containers) | Excellent (DRA GA in 1.34, NVIDIA driver now CNCF) | ~seconds | High | *Our* cloud; customer clusters that already run K8s |
| **KVM/QEMU VM + VFIO passthrough** | **Full VM (separate kernel)** | Excellent (whole-GPU passthrough; vGPU for sharing) | ~10–30 s | Lower | Cross-tenant isolation; "real VM / pick any OS" UX |
| **Kata Containers + VFIO** | **VM-isolated, container UX** | Yes (VFIO GPU passthrough) | ~2–5 s | Medium | Container ergonomics *with* VM isolation — the sweet spot for multi-tenant |
| **Firecracker** | VM-isolated, minimal | **None for GPU (2026)** — PCIe/passthrough unofficial & paused | <1 s | Highest (microVM) | **Rejected for GPU.** Reserve for CPU-only serverless later |

**The Firecracker decision, stated plainly:** Firecracker is a beautiful piece of engineering for serverless CPU workloads (it powers AWS Lambda), but as of 2026 it has **no supported GPU passthrough**. PCIe support — the prerequisite — is paused for lack of maintainer resources, and the VFIO path that exists is unofficial and requires bespoke host configuration. Designing GPU instances around Firecracker would be building on a feature that does not exist. If you later add a CPU-only serverless tier (for pre/post-processing, web hooks, lightweight inference orchestration), Firecracker becomes the obvious choice *there*. Not here.

### 7.2 Recommended architecture: tiered isolation, policy-selected

The scheduler/orchestrator picks the runtime per workload based on a **tenancy policy**, not per whim:

```
Trust level                        Runtime               GPU mechanism
─────────────────────────────────────────────────────────────────────────
Same org, trusted (Phase 1)     → containerd + CDI     → whole GPU, or MIG/MPS/time-slice
Same org, sensitive             → Kata Containers      → VFIO passthrough or MIG
Cross-org / marketplace (P3+)   → KVM/QEMU VM or Kata  → VFIO passthrough or MIG (HW-isolated)
"Give me any OS" (EC2 parity)   → KVM/QEMU full VM     → VFIO passthrough
```

Why tiered instead of "VMs for everyone" (max isolation) or "containers for everyone" (max density)?

- **Containers everywhere** is fast and dense but shares the host kernel — a container escape is a node compromise, unacceptable once you have two distrusting tenants on a box.
- **VMs everywhere** is safe but slow to boot and wastes capacity on small jobs; forcing a 30-second VM boot on a researcher iterating on a notebook is a bad experience.
- **Tiered** lets you give the common Phase-1 case (an org's own employees) the fast container path, and reserve the heavier VM/Kata path for when isolation actually matters. The cost is complexity in the orchestrator, which is worth it.

In Phase 1, you will overwhelmingly use the **container path**, because the tenant boundary is the org and employees already share infrastructure trust. Kata/VM is built *next*, ahead of multi-org (Phase 2).

### 7.3 GPU sharing primitives (and their honest limits)

Three mechanisms, with very different isolation guarantees — this matters enormously for security (§12):

- **Time-slicing:** the GPU round-robins between workloads. **No memory or fault isolation** — one workload can OOM or crash-affect another, and there's no security boundary. Acceptable only *within* a trust boundary (same team), great for dev/notebooks on consumer cards. Never across tenants.
- **MPS (Multi-Process Service):** a control daemon partitions compute and memory by fraction. Better than time-slicing, still **not a hard security boundary**. Same-trust only.
- **MIG (Multi-Instance GPU):** hardware partitioning on A100/H100/etc. into smaller "mini-GPUs" with **memory and fault isolation at the hardware layer**. This is the *only* sharing mechanism safe to cross a tenant boundary. Predefined profiles (e.g., 1g.10gb, 3g.40gb).

Design rule, enforced by the scheduler: **cross-tenant GPU sharing is MIG-only or whole-GPU-passthrough-only. Time-slicing and MPS are restricted to a single trust domain.** Encode this as a hard scheduling constraint, not a guideline.

### 7.4 On Kubernetes — the decision you must not get wrong

It is tempting to "just use Kubernetes" for the data plane. I recommend **against** running Kubernetes on customer hardware in Phases 1–2, and **for** running our own services on Kubernetes. The reasoning:

**Why not K8s on customer nodes (yet):**

- Customer hardware is *heterogeneous and lonely*: a single RTX 4090 workstation under a desk, a 4-node A100 rack in a closet, an H100 pod in a colo — often not even in the same building, frequently behind NAT, sometimes powered off nightly. Kubernetes assumes a managed cluster with a network you control and nodes that are roughly peers. Federating K8s across untrusted, NAT'd, heterogeneous, intermittently-online nodes is fighting the tool.
- The killer onboarding motion is **`curl … | sh` and the node joins the fleet in 60 seconds.** A single static Go agent delivers that. Asking a customer to stand up or join a Kubernetes cluster does not.
- We need *marketplace and multi-org* semantics (cross-org quotas, provider payouts, fairness across tenants) that Kubernetes does not have natively. We'd be bending K8s primitives into shapes they resist.

**Why we still lean on Kubernetes heavily:**

- **Our own control-plane services run on Kubernetes** in our cloud — that's exactly what it's for.
- DRA going **GA in 1.34** (with device health reporting and fractional device sharing), and NVIDIA donating its DRA driver to the CNCF, means Kubernetes is now genuinely good at GPU scheduling. We **borrow its concepts** (claims, device classes, structured parameters) for our own scheduler, and we will integrate with it.
- **K8s integration mode** (Phase 2): for customers who already operate GPU Kubernetes clusters, we represent their cluster in our platform via a **virtual-kubelet-style adapter** — our scheduler places a workload, the adapter renders it as a DRA ResourceClaim + Pod in their cluster. They get our console, billing, and access layer; their cluster stays their cluster.

**Migration path:** custom agent + scheduler (Phase 1–2) → optional per-region Kubernetes substrate for *platform-owned* hardware (Phase 4) where we *do* control the network → a unified scheduler that treats "our K8s region," "customer raw nodes," and "customer K8s clusters" as three placement backends behind one interface. The scheduler interface is designed for this from day one (a `PlacementBackend` abstraction), so adopting K8s later is an additive backend, not a rewrite.

### 7.5 Storage architecture

GPU workloads have a distinctive storage profile: enormous read-heavy datasets, large model checkpoints written periodically, and a strong need for fast local scratch. The temptation is to build a distributed block-storage layer on day one. Resist it — that is a multi-quarter project that most successful GPU clouds defer.

**Three storage tiers, by purpose:**

| Tier | What | Implementation | When |
|---|---|---|---|
| **Local scratch** | Fast ephemeral working space next to the GPU | Node-local **NVMe** (the box already has it) | Day one |
| **Object storage** | Datasets, model artifacts, snapshots, checkpoints | **MinIO** (self-host, S3-API) or **S3** (cloud) | Day one |
| **Network block / persistent volumes** | "My disk survives stop/start and follows me" | Local-first + replication; **Ceph RBD** only if/when truly needed | Defer |

**MinIO vs S3 vs Ceph — the honest evaluation:**

- **S3** (or any cloud object store) where you run in cloud: managed, durable, cheap, zero ops. Use it for our control-plane backups, template image registry, and any platform-owned region.
- **MinIO** for on-prem / customer-owned environments: S3-API-compatible, runs on the customer's own disks, so datasets and checkpoints stay inside their network (a real requirement for enterprises with data-residency constraints). This is the default object store on customer hardware.
- **Ceph** is powerful (unified block/object/file, strong consistency, self-healing) but **operationally heavy** — it wants dedicated nodes, careful tuning, and an operator who knows it well. Recommending Ceph to a small team in year one is how you lose a quarter to storage incidents. Defer it until network block storage is a proven, demanded need (Phase 3+), and even then evaluate managed alternatives first.

**Recommended posture:** start **local NVMe for scratch + MinIO/S3 for persistence**, and be explicit with users that instance-local disk is ephemeral while persistence is via volumes/object store (the EC2 instance-store-vs-EBS mental model). A **read-through dataset cache** on node NVMe (pull from object store once, reuse across runs) is high-leverage because datasets are pulled repeatedly — build that before you build distributed block storage.

- **Snapshots:** instance disk snapshots via LVM/ZFS/qcow2, pushed to object storage; restorable as new instances ("save my environment").
- **Checkpoints:** ML checkpoints are application-level, written to a mounted persistent volume or directly to object storage; the preemption hook (§8.5) triggers a checkpoint before eviction.
- **Backups:** object-store versioning + cross-region replication for platform data; **Postgres PITR** (WAL archiving) for the system of record; tested restores (an untested backup is not a backup).

---

## 8. Scheduler Design

This is the technical heart of the platform and a prioritized-deep section. The scheduler decides *which node and which GPUs* a request runs on, subject to quotas, priorities, fairness, and topology. It is inspired by the Kubernetes scheduler (filter→score), Nomad (optimistic concurrency, plan/apply), and Google Omega (shared-state, lock-free placement).

### 8.1 Design goals and the central tension

- **Correctness over optimality:** never double-allocate a GPU; a slightly worse placement is fine, a conflicting one is not.
- **Throughput:** decisions in milliseconds; thousands of pending requests; tens of thousands of nodes (per region/cell at scale).
- **GPU- and topology-aware:** a multi-GPU job needs GPUs on the same node and ideally the same NVLink domain; placing 8 GPUs across a slow PCIe path silently halves training throughput.
- **Anti-fragmentation:** keep whole GPUs (and whole nodes) free for big jobs by packing small/fractional jobs together.
- **Fair and bounded:** hierarchical quotas (org→team→project), priorities, preemption, and reservations, with fairness across tenants competing for scarce GPUs.

The central tension: **bin-packing (maximize utilization) vs. spreading (maximize resilience/perf) vs. fairness (don't starve anyone).** These conflict. We resolve it with a scoring function whose weights are policy, defaulting to *pack GPUs, spread within reason, never starve*.

### 8.2 Two-level architecture

```
        pending requests (priority queue, per-cell)
                     │
            ┌────────▼─────────┐
            │  Global admission│  quota check, fairness (DRF), reservation honoring
            │  + queueing      │
            └────────┬─────────┘
                     │ feasible request
            ┌────────▼─────────┐     in-memory projection of inventory
            │  Placement engine│◀──── (snapshot, versioned per node)
            │  filter → score  │
            └────────┬─────────┘
                     │ chosen node + GPU set (an "assignment plan")
            ┌────────▼─────────┐
            │  Optimistic commit│  CAS on node.resourceVersion in store
            └────────┬─────────┘
            success  │  conflict → retry with fresh snapshot
                     ▼
            dispatch to agent (node-level admission) ── reject → requeue
```

- **Level 1 (global, in the control plane):** admission, quota/fairness, reservation honoring, and placement. Single active scheduler **per region/cell**, leader-elected (lease in Postgres or etcd). One writer per cell keeps placement decisions serializable without a global lock; cells give you horizontal scale and blast-radius isolation (§15).
- **Level 2 (node, in the agent):** final admission. The node confirms it still has the resources (the scheduler acted on a slightly stale snapshot) and either accepts or rejects → requeue. This is the optimistic-concurrency safety net.

### 8.3 Why optimistic concurrency (and not a global lock or pure K8s-style)

At 10k+ nodes with per-second heartbeats, you cannot take a lock per scheduling decision, and you cannot re-read the whole inventory from the DB per decision. So:

- The scheduler keeps an **in-memory projection** of node capacity, updated by the heartbeat/event stream.
- It places against that projection and **commits with a compare-and-set** on the node's `resourceVersion`. If another decision (or a heartbeat reporting external change) bumped the version, the commit fails and the scheduler retries with a fresh snapshot for that node only.
- The **node-level admission** in the agent is the final backstop against the rare case where the projection was wrong (e.g., a GPU threw an Xid error between snapshot and dispatch).

This is the Nomad/Omega model and it scales far better than the naive "lock the DB" or "ask the node synchronously" approaches.

### 8.4 Placement algorithm — filter then score

```python
def place(request, inventory_snapshot, policy):
    # ---- FILTER (hard predicates; eliminate infeasible nodes) ----
    candidates = []
    for node in inventory_snapshot.healthy_nodes():
        if not node.has_gpu_type(request.gpu_type):           continue
        if node.free_gpus(request.gpu_type) < request.gpu_count: continue
        if request.fractional and not node.supports(request.frac_mode):  # MIG/MPS/TS
            continue
        if node.free_vcpu  < request.vcpu:                    continue
        if node.free_mem   < request.mem:                     continue
        if node.free_disk  < request.disk:                    continue
        if not node.supports_image(request.image):            continue
        if not node.matches_affinity(request.affinity):       continue
        if node.tainted() and not request.tolerates(node.taints): continue
        if request.cross_tenant and not node.supports_hw_isolation():  # MIG/VM only
            continue
        if request.gpu_count > 1 and not node.has_interconnect_group(request.gpu_count):
            continue   # multi-GPU needs NVLink/topology group on one node
        candidates.append(node)

    if not candidates:
        return NoFit(reason=diagnose(request, inventory_snapshot))  # feed back to user/queue

    # ---- SCORE (soft preferences; pick the best feasible node) ----
    best, best_score = None, -inf
    for node in candidates:
        s = 0
        s += policy.w_binpack   * gpu_binpack_score(node, request)   # prefer fuller nodes
        s += policy.w_frag      * antifragmentation_score(node, request) # keep whole GPUs free
        s += policy.w_topology  * topology_score(node, request)      # tightest NVLink group
        s += policy.w_locality  * data_locality_score(node, request) # near the dataset/volume
        s += policy.w_spread    * spread_score(node, request)        # avoid hot nodes
        s += policy.w_price     * price_score(node, request)         # marketplace phase only
        if s > best_score:
            best, best_score = node, s

    gpu_set = select_gpus(best, request)   # choose specific GPUs: tightest interconnect set
    return AssignmentPlan(node=best, gpus=gpu_set, version=best.resourceVersion)
```

Key subtlety in `select_gpus`: for a multi-GPU job, choose the specific physical GPUs that share the **tightest interconnect** (same NVLink domain), not just any N free GPUs. For fractional jobs, prefer a GPU that is *already partially used* (so you don't fragment a fresh GPU) — that's `antifragmentation_score` pulling the opposite direction for whole vs. fractional jobs, which is correct.

### 8.5 Fairness, quotas, priorities, reservations

- **Hierarchical quotas:** org has a GPU-hour/concurrent-GPU budget, suballocated to teams, then projects. Admission rejects (or queues) a request that would exceed the project's quota. Quotas are checked *before* placement (cheap) — no point finding a node for a request that violates quota.
- **Fairness via Dominant Resource Fairness (DRF):** with multiple resource types (GPU, vCPU, RAM, VRAM), simple "equal GPU share" is wrong. DRF equalizes each tenant's *dominant* resource share, which is the correct generalization for heterogeneous demands and is what Mesos/YARN use.

```python
# DRF: pick the next request from the tenant whose dominant share is smallest
def pick_next(pending_by_tenant, cluster_capacity, allocated_by_tenant):
    best_tenant, best_dom = None, +inf
    for tenant, queue in pending_by_tenant.items():
        if not queue: continue
        dom = max(allocated_by_tenant[tenant][r] / cluster_capacity[r]
                  for r in RESOURCES)          # GPU, vCPU, RAM, VRAM
        if dom < best_dom:
            best_tenant, best_dom = tenant, dom
    return pending_by_tenant[best_tenant].peek()
```

- **Priorities & preemption:** requests carry a priority class (e.g., `interactive` > `batch` > `preemptible`). If no node fits a high-priority request, the scheduler may **preempt** lower-priority *preemptible* workloads to make room. Preemption is graceful: signal the victim (checkpoint hook), grace period, then reclaim. Only preempt jobs explicitly marked preemptible — never silently kill someone's 20-hour training run. (Preemptible capacity is also the basis of a future "spot" tier — a nice margin lever.)

```python
def try_preempt(request, node_candidates, policy):
    for node in node_candidates_sorted_by_least_disruption():
        victims = select_preemptible_jobs(node, needed=request.resources,
                                           max_priority=request.priority - 1)
        if frees_enough(victims, request.resources):
            checkpoint_and_evict(victims, grace=policy.preempt_grace)
            return AssignmentPlan(node=node, ...)
    return None
```

- **Reservations / capacity blocks:** an org can reserve N GPUs for a window (e.g., "8× H100, next Tuesday 9am–5pm, for the big training run"). Reservations are honored at admission: reserved capacity is invisible to non-reserving requests during the window. This is also a future revenue product (committed-use discounts).

### 8.6 Failure handling

The scheduler operates on stale, partial, and sometimes wrong information by design. Failure handling is most of the real engineering:

- **Stale snapshot → wrong placement:** caught by node-level admission (§8.2) → requeue. Bounded retries with backoff; after K failures, mark the request `NoFit` with a diagnostic and surface it to the user.
- **Node disappears mid-provision:** the provisioning orchestrator (not the scheduler) owns the saga; on agent timeout it transitions the instance to a retry/reschedule, releasing the (now-orphaned) reservation after a lease expiry so the GPUs aren't leaked.
- **Scheduler crash / failover:** state is in the durable store, not the scheduler's RAM. A new leader rebuilds its in-memory projection from inventory + open assignments on startup. Because commits are CAS'd and idempotent, an in-flight decision at crash time is either committed (and the new leader sees it) or not (and it's retried). No double-allocation.
- **Split-brain (two schedulers think they're leader):** prevented by a fenced lease — the CAS commit includes the leader's fencing token; a stale leader's writes are rejected by the store. This is the single most important correctness mechanism; get the lease + fencing right and most distributed-scheduler horror stories disappear.
- **GPU hardware fault (Xid/ECC) on a running instance:** agent reports degraded health; orchestrator cordons the GPU/node (no new placements), optionally live-migrates or checkpoints the workload, opens an incident. Health-driven cordoning is why DCGM integration (§13) is not optional.
- **Thundering herd after an outage:** when a region reconnects and thousands of agents re-register at once, admission must rate-limit and the projection rebuild must be incremental. Jittered backoff on the agent side (§9) and bounded reconcile concurrency on the control side.

---

## 9. Agent Design

The agent is the second technical heart of the platform (the scheduler being the first). It is the only software we install on customer hardware, so it must be **trivial to install, impossible to lose contact with, safe by construction, and self-updating.** Prioritized-deep section.

### 9.1 Form factor and non-negotiables

- **Single static Go binary**, cross-compiled, no runtime dependencies. Install is `curl https://… | sh` (or a signed `.deb`/`.rpm`, or a container for K8s nodes). It drops a systemd unit and starts.
- **Outbound-only.** The agent *dials out* to a relay; the control plane never needs an inbound route to the node. This is the whole ballgame for working behind NAT/firewalls (restated from §1 because it constrains every part of the agent).
- **Crash-only design.** The agent can be killed at any instant and restart cleanly by reconciling actual node state (running containers/VMs, attached GPUs) against the control plane's desired state. No critical state lives only in the agent's memory.

### 9.2 Lifecycle

```
INSTALL → BOOTSTRAP → ENROLL → REGISTER → STEADY-STATE ⇄ UPGRADE
                                              │
                                              ├─→ DRAIN (cordon, finish/evict workloads)
                                              └─→ DECOMMISSION (deregister, wipe identity)
```

1. **Bootstrap:** generate a keypair locally (private key never leaves the node), read a short-lived **join token** (issued by the org admin from the console, TTL-bound, single- or limited-use).
2. **Enroll:** present the join token over TLS to the enrollment endpoint; submit a CSR; receive a **node identity certificate** signed by the platform CA (SPIFFE-style SVID, e.g. `spiffe://gpu.io/org/<org>/node/<node-id>`). The join token is now spent. From here on, the agent authenticates with its cert (mTLS) — the token was only to bootstrap trust.
3. **Register:** report the full node inventory — GPUs (via NVML: model, VRAM, MIG capability, UUID), CPU topology, RAM, disks, NICs, kernel/driver versions, NVLink/PCIe topology, virtualization capability (does it support VFIO/KVM?). The control plane records this and the node enters the schedulable pool.
4. **Steady-state:** maintain the outbound stream; send heartbeats; receive and execute commands; ship metrics/logs; run health checks. Details below.
5. **Upgrade:** self-update on control-plane instruction (§9.5).
6. **Drain / decommission:** cordon (no new work), finish or evict running workloads, deregister, and on decommission wipe the identity cert and local secrets.

### 9.3 Communication protocol

- **Transport:** a long-lived **bidirectional gRPC stream** over mTLS, tunneled through the relay. (QUIC is the natural evolution for better behavior on flaky residential/mobile links — design the relay to support both.)
- **Multiplexed logical channels** over the one connection:
  - *Control:* command request/response (provision, stop, snapshot, upgrade, drain).
  - *Heartbeat:* lightweight liveness + capacity delta, every few seconds, with jitter.
  - *Telemetry:* metrics and logs, batched and compressed, pushed out (pull is impossible across NAT — §13).
  - *Data:* per-instance SSH/HTTP byte streams ride the same tunnel via the relay (§11), logically separate from control.
- **Commands are declarative and idempotent.** "Ensure instance X exists in state RUNNING with spec S," not "run docker run …". The agent reconciles toward the desired state and can safely re-receive the same command (network retries are normal). Every command carries an idempotency key.
- **Liveness:** missed heartbeats beyond a threshold → control plane marks the node `SUSPECT` then `LOST`; running instances enter a grace period before being declared failed (a brief network blip must not nuke a 10-hour job). The agent uses **jittered exponential backoff** on reconnect to avoid thundering herds after a relay or control-plane blip.

### 9.4 Security model

The agent typically runs as root (it manages devices, namespaces, VMs) — so it is the highest-value target on the node and must be hardened accordingly:

- **Identity:** per-node mTLS cert (SPIFFE SVID), **short-lived and auto-rotated** (hours, not months). Rotation rides the existing stream. A stolen cert expires fast and can be revoked centrally.
- **No long-lived secrets at rest** beyond the rotating identity cert. Workload secrets (e.g., a user's registry creds) are delivered just-in-time, scoped to the instance, and never persisted.
- **Least privilege despite root:** drop capabilities not needed; seccomp-confine the agent's own syscalls; the workload-facing surface (image pull, exec) runs through the runtime, not ad-hoc shell. Sensitive operations are gated by signed commands from the control plane.
- **Mutual distrust with the workload:** the user's container/VM is treated as hostile. Isolation is the runtime's job (§7); the agent never executes user-provided code in its own context.
- **Signed everything:** agent binaries and upgrade artifacts are signed; the agent verifies signatures before applying (§9.5). Templates/images are scanned and admission-controlled (§12).
- **Auditability:** every command the agent executes is logged centrally (who issued it, when, idempotency key) to the immutable audit store.

### 9.5 Upgrade strategy

Fleet upgrades across hardware you don't control is an operational minefield; design for it explicitly:

- **Control-plane-orchestrated, ring-based (canary) rollout:** ring 0 (internal/test nodes) → ring 1 (design partners who opt in) → ring N (general fleet), with bake time and automatic halt on elevated error/health-regression signals.
- **Signed artifacts + verify-before-apply:** the agent downloads the new binary (through the relay/CDN), verifies the signature against a pinned public key, swaps the binary, and restarts via the supervisor. **Automatic rollback** if the new version fails its post-upgrade health check within a window.
- **Version skew policy:** the control plane stays **backward compatible** with agents N versions behind; agents must be within a supported window or they are quarantined (allowed to run existing workloads, refused new ones, and nudged to upgrade). This decouples your control-plane release cadence from the slowest customer to upgrade — essential, because some customers *will* pin versions.
- **Zero workload disruption:** an agent upgrade must not kill running instances. The agent restarts and *reconciles* to the already-running containers/VMs (crash-only design, §9.1). Test this relentlessly — it is the scariest operation you run.

## 10. Database Design

The brief asked for complete schemas, so this section is concrete. The guiding principle: **PostgreSQL is the system of record for everything transactional; time-series and high-cardinality usage data live elsewhere (ClickHouse); operational metrics live in a TSDB (Prometheus/VictoriaMetrics).** Do not try to make Postgres do all three — billing-grade usage events at 100k-GPU scale will crush a relational primary.

### 10.1 Polyglot persistence map

| Data | Store | Why |
|---|---|---|
| Orgs, users, teams, projects, RBAC, instances, inventory, quotas, reservations | **PostgreSQL** | Relational, transactional, the source of truth |
| Usage events (raw, append-only) + aggregates | **ClickHouse** | Columnar, cheap, fast over billions of rows |
| Ledger (double-entry), invoices | **PostgreSQL** (separate logical DB/schema) | Money needs ACID + audit; isolate from hot tables |
| Audit log | **PostgreSQL partitioned / append-only** → object store (WORM) | Immutable, tamper-evident |
| Operational metrics | **Prometheus → VictoriaMetrics/Mimir** | Purpose-built TSDB |
| Logs | **Loki** | Cheap, label-indexed |
| Sessions, cache, rate limits, scheduler hints | **Redis** | Ephemeral, fast |
| Event/command bus | **NATS JetStream** (→ Kafka at scale) | Durable streaming |

### 10.2 Core relational schema (PostgreSQL)

Abbreviated but representative DDL for the control-plane system of record. Every tenant-scoped table carries `org_id` for **row-level security** (§12) and is a future sharding key (§15).

```sql
-- ============ IDENTITY & TENANCY ============
CREATE TABLE orgs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    slug          TEXT NOT NULL UNIQUE,
    plan          TEXT NOT NULL DEFAULT 'standard',
    status        TEXT NOT NULL DEFAULT 'active',     -- active|suspended
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         CITEXT NOT NULL UNIQUE,
    name          TEXT,
    idp_subject   TEXT,                                -- OIDC sub / SAML nameID
    status        TEXT NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A user belongs to an org with a role (a user may be in several orgs).
CREATE TABLE org_memberships (
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role          TEXT NOT NULL,                       -- owner|admin|member|billing|viewer
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

CREATE TABLE teams (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    UNIQUE (org_id, name)
);

CREATE TABLE team_members (
    team_id       UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role          TEXT NOT NULL DEFAULT 'member',
    PRIMARY KEY (team_id, user_id)
);

-- Projects are the unit of quota + billing attribution + network scope.
CREATE TABLE projects (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    team_id       UUID REFERENCES teams(id) ON DELETE SET NULL,
    name          TEXT NOT NULL,
    cost_center   TEXT,                                -- for chargeback
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

-- ============ INVENTORY (data-plane hardware) ============
CREATE TABLE nodes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    hostname      TEXT,
    region        TEXT NOT NULL,                       -- logical region/cell
    status        TEXT NOT NULL DEFAULT 'enrolling',   -- enrolling|ready|suspect|lost|draining|decommissioned
    spiffe_id     TEXT UNIQUE,                         -- node identity
    kernel        TEXT, nvidia_driver TEXT,
    cpu_cores     INT, mem_bytes BIGINT, disk_bytes BIGINT,
    supports_vfio BOOLEAN DEFAULT FALSE,               -- can it run passthrough VMs?
    topology      JSONB,                               -- NVLink/PCIe graph
    resource_ver  BIGINT NOT NULL DEFAULT 0,           -- optimistic-concurrency token
    last_heartbeat TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_nodes_org      ON nodes(org_id);
CREATE INDEX idx_nodes_sched    ON nodes(region, status) WHERE status = 'ready';
CREATE INDEX idx_nodes_hb       ON nodes(last_heartbeat);

CREATE TABLE gpus (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id       UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    uuid          TEXT NOT NULL UNIQUE,                -- NVML GPU UUID
    model         TEXT NOT NULL,                       -- 'H100-SXM5-80GB', 'RTX4090', ...
    vram_bytes    BIGINT NOT NULL,
    mig_capable   BOOLEAN NOT NULL DEFAULT FALSE,
    mig_profile   TEXT,                                -- current partition profile if any
    nvlink_domain INT,                                 -- topology grouping on the node
    health        TEXT NOT NULL DEFAULT 'healthy',     -- healthy|degraded|failed
    allocation    TEXT NOT NULL DEFAULT 'free'         -- free|whole|shared
);
CREATE INDEX idx_gpus_node   ON gpus(node_id);
CREATE INDEX idx_gpus_sched  ON gpus(model, health, allocation);

-- ============ INSTANCES (the state machine of §4.3) ============
CREATE TABLE instances (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    project_id    UUID NOT NULL REFERENCES projects(id),
    owner_id      UUID NOT NULL REFERENCES users(id),
    node_id       UUID REFERENCES nodes(id),           -- null until SCHEDULED
    state         TEXT NOT NULL DEFAULT 'pending',     -- pending|scheduled|provisioning|running|stopping|stopped|terminated|failed
    runtime       TEXT NOT NULL,                       -- container|kata|vm
    spec          JSONB NOT NULL,                      -- gpu_type/count, vcpu, mem, disk, image, frac_mode
    gpu_ids       UUID[],                              -- specific GPUs assigned
    endpoints     JSONB,                               -- ssh, jupyter, vscode, addr
    priority      INT NOT NULL DEFAULT 100,
    preemptible   BOOLEAN NOT NULL DEFAULT FALSE,
    started_at    TIMESTAMPTZ, stopped_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_inst_org_proj ON instances(org_id, project_id);
CREATE INDEX idx_inst_owner    ON instances(owner_id);
CREATE INDEX idx_inst_active   ON instances(state) WHERE state IN ('pending','scheduled','provisioning','running','stopping');
CREATE INDEX idx_inst_node     ON instances(node_id) WHERE node_id IS NOT NULL;

-- ============ QUOTAS & RESERVATIONS ============
CREATE TABLE quotas (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    scope_type    TEXT NOT NULL,                       -- org|team|project
    scope_id      UUID NOT NULL,
    gpu_type      TEXT,                                -- null = all types
    max_concurrent_gpus INT,
    max_gpu_hours_month  NUMERIC,
    UNIQUE (scope_type, scope_id, gpu_type)
);

CREATE TABLE reservations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id),
    project_id    UUID REFERENCES projects(id),
    gpu_type      TEXT NOT NULL, gpu_count INT NOT NULL,
    starts_at     TIMESTAMPTZ NOT NULL, ends_at TIMESTAMPTZ NOT NULL,
    status        TEXT NOT NULL DEFAULT 'confirmed'
);
CREATE INDEX idx_resv_window ON reservations(gpu_type, starts_at, ends_at);

-- ============ AUDIT (append-only, hash-chained) ============
CREATE TABLE audit_log (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id        UUID,
    actor_id      UUID,
    action        TEXT NOT NULL,                       -- 'instance.launch', 'quota.update', ...
    target        TEXT,
    metadata      JSONB,
    prev_hash     BYTEA,                               -- hash chain for tamper-evidence
    row_hash      BYTEA NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
) PARTITION BY RANGE (created_at);
-- monthly partitions; old partitions exported to object storage (WORM)
```

(Usage events and the ledger schemas are in §14; they live in ClickHouse and a separate Postgres schema respectively.)

### 10.3 Indexing and access-pattern notes

- The hot scheduler reads (`idx_nodes_sched`, `idx_gpus_sched`) are **partial indexes** on the schedulable subset — you never scan lost/decommissioned nodes.
- `idx_inst_active` is partial on non-terminal states; the vast majority of rows over time are `terminated` and should not bloat the hot index.
- Every list endpoint is org-scoped, so `org_id` leads composite indexes — this also sets up sharding by `org_id` later.
- Avoid `SELECT *` on `instances`; `spec`/`endpoints` are JSONB and can be large.

### 10.4 Scaling strategy (previewing §15)

- **Stage A–B:** single primary + read replicas; PgBouncer for connection pooling (a fleet of agents and API pods will exhaust raw Postgres connections fast).
- **Stage C:** shard by `org_id` — **Citus** (Postgres-native distributed) or app-level sharding. Org is the natural shard key because tenancy boundaries already align to it and cross-org queries are rare (and belong in the analytics warehouse anyway).
- **Usage/metering never goes in Postgres at scale** — it is in ClickHouse from the start of any real volume. This is the single most important "don't paint yourself into a corner" database decision.
- **Audit partitions** roll monthly and export to immutable object storage; the live DB keeps a rolling window.

---

## 11. Networking Design

This is the section that separates a real product from a demo, and the brief explicitly asks: **how does `ssh user@instance-ip` actually reach a container on a workstation behind a corporate NAT?** The honest answer is that there is *no routable instance IP in the traditional sense* — and pretending otherwise is how naive designs fail in the field.

### 11.1 The core problem

Customer GPU nodes are:

- behind NAT / corporate firewalls, with **no inbound public IP**,
- on dynamic, sometimes-changing addresses,
- frequently behind egress proxies that only allow outbound 443.

So classic cloud networking (allocate a public IP, route packets to the VM) is **unavailable**. The platform must create connectivity over connections the *node* initiated outbound. Three building blocks make this work: an **overlay network**, a **relay fleet**, and an **ingress/proxy tier**.

### 11.2 The overlay + relay model

- Each agent brings up a **WireGuard** interface and establishes connectivity *outbound* to the nearest **regional relay** (and, where possible, direct peer-to-peer via NAT traversal, falling back to relayed). This is the Tailscale/Cloudflare-Tunnel model, and for good reason — it is the proven way to mesh machines across hostile networks.
- Every instance receives a **stable overlay address** (e.g., in a CGNAT 100.64.0.0/10 range) and a **stable DNS name**: `inst-<id>.<project>.<org>.gpu.io`. That name is what the user and the UI use; the underlying physical IP is irrelevant and may change.
- The **relay fleet** is a horizontally scaled, regional, stateless-ish tier that (a) terminates agent tunnels, (b) proxies user connections to instances, and (c) is assigned to agents/instances by the relay coordinator (closest/least-loaded). Relays are on the data path and scale with bandwidth, independently of the control plane.

### 11.3 Walkthrough: `ssh user@inst-abc123.proj.acme.gpu.io`

Step by step, because this is the question that matters:

```
1. User runs:  ssh user@inst-abc123.proj.acme.gpu.io
2. DNS resolves the name to the user's NEAREST regional SSH gateway (anycast / GeoDNS),
   NOT to the instance (the instance has no public IP).
3. TCP/SSH connection lands on the SSH gateway (a TCP proxy in our edge).
4. AuthN at the gateway: the platform issues SHORT-LIVED SSH CERTIFICATES.
   The user's key was signed by the platform CA at login (via the CLI/console),
   carrying their identity + which instances they may reach. The gateway verifies the
   cert, checks authorization (does this user's project own inst-abc123?), and logs it.
5. The gateway looks up inst-abc123 in the control plane → finds (region, relay, node).
6. The gateway forwards the TCP stream over the OVERLAY to the relay that terminates
   that node's outbound tunnel.
7. The relay pushes the bytes down the agent's EXISTING OUTBOUND TUNNEL to the node.
8. The agent forwards the stream into the instance's network namespace, to sshd:22.
9. sshd authenticates the now-inner SSH session (the user's key, or a platform-injected
   authorized principal). The user is in.

   User → [anycast] → SSH Gateway → [overlay] → Relay → [agent's outbound tunnel] → Agent → instance sshd
```

The crucial points:

- **At no stage did anyone connect *inbound* to the node.** Bytes travel down a tunnel the agent opened outbound. This is why it works behind any NAT/firewall that allows outbound 443.
- **Authn happens at the edge gateway** (short-lived SSH certs, platform-CA-signed), so access is centrally controlled, time-bound, and audited — vastly better than distributing static SSH keys to instances. Revoking access is instant (don't re-sign), not "go delete keys on N boxes."
- **The "IP" the user sees** (`inst-abc123…`) is a stable name over the overlay, decoupled from the node's real, possibly-changing physical address.

### 11.4 Two connectivity modes (offer both)

1. **Relayed gateway (zero-install, the default):** user → public gateway → relay → agent → instance, exactly as above. Works from any machine with no client software. Downside: bytes traverse our relay (bandwidth cost; we're on the data path). Best for SSH, web terminal, Jupyter/VSCode.
2. **Mesh client (power users / high-bandwidth):** the user installs the platform client (a WireGuard mesh endpoint). Their machine joins the overlay and gets **direct P2P** to the instance via NAT traversal (STUN-style hole punching), falling back to relay only when P2P fails. Best for large dataset transfers and lowest latency. Optional, not required.

### 11.5 Jupyter, VSCode, web terminal (HTTP path)

These are HTTP(S)/WebSocket, routed through a **regional reverse proxy** (Envoy/Traefik) rather than the raw TCP SSH gateway:

- Per-instance hostname `inst-abc123.proj.acme.gpu.io` with **wildcard TLS** (`*.acme.gpu.io`) terminated at the proxy.
- The proxy **auth-gates** every request against the platform session (SSO cookie / short-lived token) *before* proxying — so an unauthenticated request never reaches the instance. This is also where you enforce that the requester is allowed to access *this* instance.
- The request is then proxied over the overlay → relay → agent → instance's Jupyter/VSCode/PTY port. The web terminal is xterm.js over a WebSocket the proxy forwards to the instance's shell.

### 11.6 Tenant isolation, service discovery, load balancing

- **Per-instance network namespace**; east-west traffic between instances (especially across tenants) is **denied by default**. The agent enforces L3/L4 policy with eBPF/nftables; the overlay segments tenants so one tenant cannot even address another's instances.
- **Security groups** (familiar EC2 concept): per-instance L3/L4 allow rules, enforced at the agent and the edge.
- **Service discovery:** the control plane is the source of truth (instance → region/relay/node); our own services in-cloud discover each other via Kubernetes DNS / a service mesh. Customer-side discovery is the overlay + control-plane registry, never multicast/mDNS (won't cross networks).
- **Load balancing:** control-plane APIs sit behind a standard L7 LB (Envoy) with anycast/GeoDNS. The relay tier is balanced by the relay coordinator (assign closest/least-loaded, rebalance on failure). For user-facing inference endpoints (a future product), per-instance or per-service LBs ride the same ingress.

### 11.7 Why not just give every node a VPN into our VPC?

A tempting simplification — put all nodes on one big flat VPN — but it is wrong at scale and for security: a flat L2/L3 network across thousands of untrusted customer nodes is a lateral-movement nightmare (one compromised node sees everyone), it doesn't segment tenants, and it scales poorly. The overlay-with-policy model gives connectivity *and* isolation, and segments per tenant by construction. Connectivity and trust are different problems; don't solve the first in a way that destroys the second.

---

## 12. Security Design

Assume enterprise customers from day one — security is a gating sales requirement, not a later concern. The model spans identity, authorization, isolation (the multi-layered crux), secrets, encryption, and audit.

### 12.1 Authentication

- **End users:** OIDC as the primary protocol (Google, Microsoft, Okta, Auth0). **SAML 2.0** for enterprises that require it, plus **SCIM** for automated user provisioning/deprovisioning (enterprises will demand "deprovision in our IdP → access gone here"). Build OIDC first (enough for design partners); SAML/SCIM when the first enterprise asks — they are well-understood but non-trivial, so don't pre-build.
- **Service-to-service & agents:** **mTLS with SPIFFE/SPIRE identities** — every service and node has a short-lived, auto-rotated SVID. No shared API keys between internal services.
- **MFA, session management, device trust:** enforced at the IdP and the gateway; short-lived sessions; refresh with re-validation.

### 12.2 Authorization

- **Hierarchical RBAC** over org → team → project, with roles (owner/admin/member/billing/viewer) and fine-grained permissions (`instance:launch`, `quota:edit`, `node:enroll`, `billing:view`, …).
- **Externalized policy engine — Cedar (AWS's authz language) or OPA.** Keep authorization *out* of business logic and in a declarative, testable, auditable policy. Every API call resolves the actor's effective permissions against the policy; the answer is logged. ABAC for resource-level rules ("can edit instances in projects they own").
- **The server is the sole authority.** UI permission checks only hide affordances; the API re-authorizes everything (restated from §5.1 because it is the most common security mistake in dashboards).

### 12.3 Multi-tenancy isolation (the crux) — defense in depth

Isolation is enforced at **every layer**, because any single layer can fail:

| Layer | Mechanism |
|---|---|
| **Data** | `org_id` on every row + Postgres **row-level security**; per-tenant object-store buckets/prefixes with separate keys |
| **Compute** | Cross-tenant workloads get **VM/Kata isolation** (separate kernel) — never shared-kernel containers across tenants (§7) |
| **GPU** | Cross-tenant GPU sharing is **MIG (hardware-isolated) or whole-GPU only** — time-slicing/MPS are same-trust only (§7.3) |
| **Network** | Per-tenant overlay segmentation; east-west deny-by-default; eBPF/nftables enforcement (§11.6) |
| **Secrets** | Per-tenant keys; envelope encryption; no cross-tenant key access |
| **Identity** | Per-node, per-service SVIDs; blast radius of a compromised credential is one node/service |

The single most important rule: **a shared-kernel container is not a security boundary between distrusting tenants.** Phase 1 (one org, its own employees) can use containers because the trust boundary *is* the org. The moment two orgs can land on the same physical GPU (Phase 2+), cross-tenant placement must use VM/Kata + MIG/passthrough. Encode this as a hard scheduler constraint and a tested invariant, not a policy doc.

### 12.4 Secrets, encryption, supply chain

- **Secrets:** HashiCorp Vault or cloud KMS; dynamic, short-lived secrets; envelope encryption; agents hold only their rotating identity cert.
- **Encryption in transit:** TLS 1.3 externally, mTLS internally, WireGuard on the overlay.
- **Encryption at rest:** full-disk (LUKS) on nodes where we control provisioning, encrypted volumes, SSE on object storage, KMS-managed keys, optional per-tenant keys (BYOK) for enterprise.
- **Supply chain:** signed agent/runtime binaries (§9.5), signed and **scanned** template images, SBOMs, admission control rejecting unsigned/unscanned images. The template catalog is a controlled attack surface — treat it like one.

### 12.5 Audit & compliance

- **Immutable, hash-chained audit log** (§10.2) of every privileged action — who, what, when, from where — exportable for the customer's own SIEM and tamper-evident by construction.
- **Compliance trajectory:** **SOC 2 Type II** first (table stakes for enterprise B2B; start the controls early because Type II needs an observation window), then **ISO 27001**, then **HIPAA/FedRAMP** only if the market pulls you there. Don't chase certifications ahead of demand, but *do* build the controls (audit, encryption, access reviews, RLS) from the start so certification is a documentation exercise, not a re-architecture.

---

## 13. Observability Design

You are operating software on machines you cannot physically touch, often cannot SSH into directly, and that disappear from the network without warning. Observability is therefore not a "nice to have" — it is how you run the business. The brief's tools (Prometheus, Grafana, Loki, OpenTelemetry) are the right core; the non-obvious part is **how telemetry crosses NAT.**

### 13.1 The push constraint

Prometheus's default model is **pull** (the server scrapes targets). **You cannot pull a node behind NAT.** Therefore:

- The **agent scrapes local exporters** (DCGM exporter for GPU, node_exporter for host) on `localhost` and **pushes** the data out over its existing outbound tunnel via **OTLP** or Prometheus **remote_write** to a regional collector. Pull becomes push at the node boundary. This is the defining design point of observability here, and getting it wrong (assuming you can scrape customer nodes) breaks the whole pipeline.

### 13.2 The three pillars

- **Metrics:** OpenTelemetry SDKs in all control-plane services; agents remote_write host + GPU metrics. **DCGM** is mandatory for GPU telemetry — utilization, memory, temperature, power, and crucially **Xid/ECC error counters** that drive health-based cordoning (§8.6). Storage: Prometheus per region → long-term in **VictoriaMetrics** or **Mimir** (Thanos is fine too) for global query and retention.
- **Logs:** **Loki** (label-indexed, cost-effective at scale). Agents ship instance and system logs via an OTel collector / Vector (Rust, efficient) over the tunnel. Per-instance log streaming to the console rides the same path (the user watching `stdout` in the UI).
- **Traces:** **OpenTelemetry → Tempo/Jaeger.** Trace a launch request end to end (API → scheduler → orchestrator → agent → provision) — when "launch is slow," distributed tracing tells you whether it's scheduling, image pull, or network setup. Invaluable given how many hops a request makes.
- **Visualization:** Grafana over all three, plus customer-facing fleet/utilization dashboards (which feed the chargeback story — utilization data *is* a product feature, not just ops).

### 13.3 Alerting and SLOs

- **Alertmanager → PagerDuty/Opsgenie**, with defined **SLOs and error budgets** (launch success rate, time-to-running P95, agent connectivity %, scheduler decision latency).
- **GPU-health alerts** (Xid storms, ECC errors, thermal throttling, fan/PSU faults) are first-class — a degrading H100 is both a reliability and a financial event.
- **Synthetic probes:** continuously launch-and-teardown a canary instance per region to detect provisioning regressions before customers do.

### 13.4 One standard: OpenTelemetry

Standardize on **OTel** as the single instrumentation API for metrics, logs, and traces. It decouples instrumentation from backends (swap Tempo for Jaeger, Loki for OpenSearch, without touching app code), which at a 10-year horizon is worth far more than any single tool choice. Instrument once, route anywhere.

---

## 14. Billing Design

Billing is where correctness compounds: a small metering error, multiplied by GPU-hours and months, becomes a trust-destroying invoice dispute. The architecture must be **exact, idempotent, auditable, and evolvable** from internal chargeback (Phase 1) to marketplace billing (Phase 3) to provider payouts (Phase 4). The single most important decision is to build on a **double-entry ledger from day one**, even when no real money moves.

### 14.1 Pipeline: events → aggregation → rating → ledger → invoice

```
agent usage heartbeats        ┌───────────┐    ┌───────────┐   ┌──────────────┐   ┌──────────┐
(resource × time, per inst) → │  Metering │ →  │  Rating   │ → │  Double-entry │ → │ Invoice/  │
   + instance state events    │ aggregate │    │  (price   │   │   Ledger      │   │ chargeback│
                              │  + dedupe │    │   book)   │   │  (immutable)  │   │  report   │
                              └───────────┘    └───────────┘   └──────────────┘   └──────────┘
   ClickHouse (raw, append-only) ──────────────┘  reconciliation jobs cross-check all stages
```

### 14.2 Metering (the foundation)

- The agent emits **usage events** — `(instance_id, resource_type, quantity, interval_start, interval_end, event_id)` — for GPU-seconds (by GPU type/MIG profile), vCPU, RAM-GB-hours, storage-GB-hours, and network egress.
- Events are **append-only** in ClickHouse and **idempotent** (deduped by `event_id`). Idempotency is non-negotiable: agents reconnect and replay after network blips, and double-counting GPU-hours is a direct billing error.
- **Authoritative duration** comes from the instance state machine's `RUNNING` enter/exit timestamps (§4.3), **cross-checked** against agent heartbeats. Never bill from a single source: if the agent says "running" but the control plane stopped it, reconciliation flags the discrepancy. Handle clock skew (use server-stamped boundaries), disconnects (grace policy — who pays for a node that vanished mid-job?), and crashes explicitly.

```sql
-- ClickHouse: raw usage (append-only, deduped on event_id)
CREATE TABLE usage_events (
    event_id     UUID,
    org_id       UUID,  project_id UUID,  instance_id UUID,
    resource     LowCardinality(String),   -- 'gpu_h100','gpu_rtx4090','vcpu','mem_gb','disk_gb','egress_gb'
    quantity     Float64,
    interval_start DateTime64(3),
    interval_end   DateTime64(3),
    ingested_at    DateTime64(3) DEFAULT now64(3)
) ENGINE = ReplacingMergeTree(ingested_at)
ORDER BY (org_id, project_id, instance_id, resource, interval_start, event_id);
```

### 14.3 The ledger (build this early, even for chargeback)

Model **every** chargeable unit as a double-entry transaction from the start:

```sql
-- PostgreSQL (isolated billing schema)
CREATE TABLE ledger_accounts (
    id        UUID PRIMARY KEY,
    org_id    UUID NOT NULL,
    kind      TEXT NOT NULL,        -- project_cost_center | platform_revenue | provider_payable | tax
    name      TEXT NOT NULL
);
CREATE TABLE ledger_entries (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    txn_id    UUID NOT NULL,                       -- groups the balanced legs
    account_id UUID NOT NULL REFERENCES ledger_accounts(id),
    direction TEXT NOT NULL,                       -- debit | credit
    amount    NUMERIC(20,6) NOT NULL,              -- minor units; positive
    currency  TEXT NOT NULL DEFAULT 'USD',
    usage_ref UUID,                                -- link back to aggregated usage
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Invariant, enforced by the writing service and a periodic check:
--   for every txn_id, SUM(debits) = SUM(credits).
```

Why double-entry now, with no real money? Because **chargeback is accounting**, and retrofitting accounting correctness onto a system that was "just summing usage" is one of the most painful migrations there is. With a ledger from day one, Phase 1 chargeback is "debit project cost-center, credit an internal clearing account," and Phase 3/4 (real revenue, provider payouts, commission, tax) are *new account types and new transaction shapes on the same ledger* — not a rewrite.

### 14.4 Phase evolution

- **Phase 1 — internal chargeback/showback:** rate usage against the org's internal price book; produce per-team/project/cost-center reports. No external money; the ledger records internal allocations.
- **Phase 3 — marketplace billing:** add real consumer payments (**Stripe**), wallets/credits, holds/escrow, price discovery (provider-set or platform dynamic pricing), and **commission** (a ledger split: consumer debit → provider payable + platform revenue).
- **Phase 4 — provider payouts:** payout rails (**Stripe Connect**, ACH/wire), tax handling (1099/global VAT), fraud and chargeback handling, reconciliation against the ledger. Every payout is a ledger transaction; the ledger is the single source of financial truth, and external systems (Stripe) reconcile *to* it.

### 14.5 Correctness guarantees

- **Idempotent ingestion** (dedupe by `event_id`) → no double-billing on retry.
- **Reconciliation jobs** cross-check raw events ↔ aggregates ↔ ledger ↔ external payment processor, alerting on any drift.
- **Immutable raw events + immutable ledger** → every invoice is fully auditable back to the GPU-seconds that produced it. When a customer disputes a charge, you can show the exact intervals. That auditability is itself a sales asset for enterprise finance teams.

## 15. Scalability Strategy

The architecture must survive four orders of magnitude of growth. The discipline here is to **not build Stage D on day one** (it would never ship) while **not painting yourself into corners** that force a rewrite. Below, each stage names *what breaks first* and *how it evolves* — because "scalability" is meaningless without naming the specific failure.

### Stage A — ~100 users, hundreds of GPUs, 1–few orgs

- **Shape:** Modular monolith (one Go control-plane binary, clean package boundaries), single Postgres (with a read replica), NATS JetStream, one regional relay, one region. Everything fits in memory; the scheduler is a single goroutine over an in-memory projection.
- **What you do NOT build:** microservices, sharding, multi-region, cells, Kafka, a pricing engine. All premature.
- **First thing that breaks (and when):** nothing infrastructural — your bottleneck is *product*, not scale. The risk at Stage A is over-engineering. Ship the monolith.

### Stage B — ~10,000 users, thousands of GPUs, many orgs

- **What breaks:** (1) Postgres write contention on hot tables (`instances`, usage); (2) connection exhaustion from many agents + API pods; (3) a single relay's bandwidth; (4) metering volume in Postgres.
- **How it evolves:** split out the **scheduler**, **metering**, and **relay coordinator** as their own services (the rest stays monolithic). Add **PgBouncer** and read replicas. Move usage events to **ClickHouse** (out of Postgres entirely). Deploy **multiple regional relays** with the coordinator assigning closest/least-loaded. Redis for caching/sessions. The scheduler is still a single leader-elected instance — fine to several thousand nodes.
- **Still fine:** single Postgres primary (with replicas), single-region control plane, single global scheduler.

### Stage C — ~100,000 users, 100,000 GPUs, global

- **What breaks (the big ones):** (1) a single global scheduler over 100k nodes with per-second churn — decision throughput and projection size; (2) the single Postgres primary; (3) single-region latency for globally distributed agents; (4) blast radius — one bad deploy takes down *everyone*.
- **How it evolves — three structural shifts:**
  1. **Regionalize the scheduler and inventory.** Each region/cell runs its own leader-elected scheduler owning its regional inventory. A thin global admission layer routes a request to the right region (by data locality, capacity, policy). No single scheduler ever sees all 100k nodes.
  2. **Shard Postgres by `org_id`** (Citus or app-level). Org is the natural shard key (tenancy already aligns to it). Global/identity data stays in a small strongly-consistent core; tenant data shards out.
  3. **Cell-based architecture.** Partition the fleet into **cells** — each an independent, full control-plane stack serving a slice of orgs/regions. A cell failure affects only its tenants. This is how AWS, Slack, and others contain blast radius; it is the single most important Stage-C pattern and the reason to keep services stateless-per-cell and avoid global mutable singletons.
- **Metering:** Kafka/Redpanda firehose → ClickHouse. **Consistency:** embrace eventual consistency for inventory/metrics; reserve strong consistency for identity, quotas, and the ledger.

### Stage D — global platform / marketplace

- **What breaks:** assumptions of a single trust domain (now untrusted providers), a single pricing model (now a market), and intra-region placement (now cross-region supply/demand matching).
- **How it evolves:** full multi-region active-active; cells as the unit of deployment and isolation; a **supply/demand matching + pricing engine** (Phase 3+); the relay fleet becomes a **global anycast mesh**; the data plane spans org-owned + provider + platform-owned hardware behind one `PlacementBackend` interface. Control-plane edge (auth, API) goes to a CDN/edge tier; only the truly-global state (identity, ledger) remains centrally coordinated, everything else is regional/cellular.

### The through-line

The pieces you must get right *early* so Stage C/D is an evolution and not a rewrite (restating §1.5 with scale framing): **org-scoped data model** (enables sharding), **outbound agent + overlay** (enables global fleet), **`PlacementBackend` abstraction** (enables regional/cell schedulers and K8s integration), **append-only events + ledger** (enables marketplace billing), and **stateless-per-cell services** (enables cell partitioning). Everything else can be bolted on when the specific bottleneck actually bites.

---

## 16. Future Marketplace Evolution

This is where a co-founder earns their equity by disagreeing. The phased vision is mostly sound — and one phase is a trap. Let me go phase by phase and be direct.

### 16.1 Phase 1 → 2 (single-org cloud → multi-org SaaS): **green light**

This is ordinary multi-tenant SaaS evolution. The architecture already assumes org as the tenant boundary, so Phase 2 is mostly: harden cross-tenant isolation (VM/Kata + MIG becomes mandatory, §7.3/§12.3), add org self-service signup/billing, and operate at higher scale. Low strategic risk. Do it.

### 16.2 Phase 2 → 3 (multi-org → provider marketplace): **green light, but it's a different company**

A marketplace where providers (GPU farms, data centers, universities) register hardware and consumers rent it, with the platform taking commission, is a *proven* business — RunPod (managed, multi-source supply, ~$120M ARR, 400k+ developers) and Vast.ai (peer-to-peer, cheapest-in-market) both demonstrate it works. The architecture transfers almost perfectly: the outbound agent, overlay networking, scheduler, metering, and ledger are *exactly* what a marketplace needs. **Building Phase 1 well is building the marketplace substrate** — this is the strongest argument for the whole plan.

**But flag this honestly to yourself:** the marketplace buyer is *not* the Phase 1 buyer. Phase 1 you sell to a platform/IT team that owns idle GPUs and wants control. The marketplace serves (a) developers/AI startups who want cheap GPUs and (b) providers who want yield. Different go-to-market, different trust model, different ops. Phase 1 is a great *beachhead*, but it does not automatically funnel into a marketplace — you will be standing up a second motion. That's fine; just don't assume the customer list converts for free.

### 16.3 Phase 4 (hybrid: org-owned + provider + platform-owned): **green light**

This is the natural unification, and the `PlacementBackend` abstraction (§7.4) is designed for exactly it: org nodes, provider nodes, and platform-owned regions are three backends behind one scheduler. The hard parts are commercial (pricing across heterogeneous supply) and operational (SLA differences between a hyperscaler-grade colo and someone's closet), not architectural.

### 16.4 Phase 5 (decentralized network, idle consumer GPUs, "location-agnostic"): **this is the part I'd push back on hard**

The vision — "individuals' idle GPUs, users don't care where compute comes from" — is seductive and mostly a **graveyard**. Here is the unsentimental engineering and business reality:

1. **Consumer GPUs are the wrong hardware for serious work.** Gaming RTX cards lack ECC memory, lack fast interconnect (no NVLink/InfiniBand), and lack datacenter reliability features. Serious AI **training** needs many GPUs with *co-located high-bandwidth interconnect* — you fundamentally cannot assemble that from GPUs scattered across bedrooms on residential internet. The physics of gradient synchronization don't care about your overlay network.
2. **"Location-agnostic" is true for a narrow workload class and false for most.** It holds for *fungible, stateless, latency-tolerant, embarrassingly-parallel* jobs — batch inference, some fine-tuning, rendering. It is false for distributed training (needs tight interconnect), latency-sensitive serving (needs locality to users), and **anything regulated or sensitive** (no enterprise runs proprietary data on a stranger's PC — the data-governance objection alone kills most enterprise demand).
3. **The economics are brutal.** Consumer nodes are unreliable (powered off, residential power/network, churn), so orchestration overhead (replication, checkpointing, fault tolerance, fraud detection, verifying the provider didn't lie about the hardware) eats the margin. The crypto-era decentralized-GPU projects mostly learned this the expensive way.
4. **Trust and verification.** In an open network you must verify that a provider's "H100" is real and not throttled/shared/spoofed, that they're not snooping the workload, and that they'll be online for the job's duration. That verification infrastructure is itself a large product.

**My recommendation:** architect so Phase 5 *remains possible* — and it will be, because the agent + overlay + scheduler + ledger primitives are identical — but **do not let the business depend on it, and do not architect the company around it.** If you pursue decentralized supply, pursue it as a *specific product* (decentralized batch inference / fine-tuning on a curated mix of prosumer and small-datacenter idle capacity), with verification and reputation built in, targeting the narrow workload class where location genuinely doesn't matter. Frame it as an *optional supply source* feeding the Phase 3 marketplace, not as the company's destiny.

The real prize is Phases 1–4: the GPU cloud OS for organizations, evolving into a curated marketplace of *reliable* supply (datacenters, farms, universities, prosumers). That is a very large business on its own, and it does not require betting on the hardest, most-failed version of the idea. Build the substrate that *could* go decentralized; sell the version that *works* today.

---

## 17. Risk Analysis

| Risk | Category | Severity | Mitigation |
|---|---|---|---|
| Scheduler double-allocates / leaks GPUs | Technical | High | Optimistic concurrency + fencing tokens + node-level admission + reconciliation (§8.3, §8.6); invariant tests |
| Networking fails behind real corporate NAT/proxies | Technical | High | Outbound-only agent; relay fallback when P2P fails; test against egress-proxy-only environments early (§11) |
| GPU driver/hardware faults (Xid/ECC) corrupt jobs | Technical | Med-High | DCGM health monitoring → cordon/migrate (§8.6, §13); never silently reuse a degraded GPU |
| Distributed-systems split-brain / data corruption | Technical | High | Leader election + fenced leases; durable state machine; idempotent commands (§8, §9) |
| **Multi-tenant isolation escape** (container breakout across tenants) | Security | **Critical** | VM/Kata + MIG for cross-tenant (§7.3, §12.3); shared-kernel containers only within a trust domain; pen-testing |
| Agent compromise → node compromise → lateral movement | Security | Critical | Short-lived SVIDs; least privilege; signed binaries; per-tenant network segmentation limits blast radius (§9.4, §11.6) |
| Supply-chain attack via template images | Security | High | Signed + scanned images; SBOMs; admission control (§12.4) |
| Secrets leakage | Security | High | Vault/KMS; dynamic short-lived secrets; no secrets at rest on agents (§12.4) |
| Wrong buyer / weak Phase-1 → Phase-3 conversion | Business | High | Validate the *marketplace* buyer separately; don't assume Phase-1 customers convert (§16.2) |
| Marketplace chicken-and-egg (no supply ↔ no demand) | Business | High | Seed supply with platform-owned + design-partner capacity before opening demand; curate, don't open-flood |
| Commoditization / price war with RunPod/Vast/hyperscalers | Business | High | Compete on *control + experience for owned hardware*, not price; the wedge avoids a day-one price war (§1.1) |
| GPU market shifts (cheaper cloud GPUs, new accelerators) | Business | Med | Hardware-agnostic abstraction (GPUs are just typed resources); support non-NVIDIA accelerators behind the same interface over time |
| Customer concentration (one big logo = most revenue) | Business | Med | Diversify design partners; don't over-fit the product to one customer |
| Supporting heterogeneous hardware you don't control | Operational | High | Strict supported-config matrix; agent reports capability; refuse/flag unsupported configs rather than best-effort |
| On-call for nodes you can't physically touch | Operational | High | Deep observability + remote diagnostics over the tunnel; clear support boundary (customer owns the physical layer) |
| Fleet upgrade breaks customer nodes | Operational | High | Ring rollout + signed artifacts + auto-rollback + version-skew tolerance (§9.5); reconcile-don't-restart |
| Over-engineering before PMF | Operational | Med | The Stage-A discipline (§15): modular monolith, defer cells/sharding/microservices |

---

## 18. MVP Roadmap

The goal of the MVP is **one design-partner org running real GPU work through the platform**, not a scalable architecture. Build the thin vertical slice through every layer, defer everything else, but get the §1.5 "expensive-to-retrofit" pieces structurally right.

### Milestone 0 — Thinnest end-to-end slice (the "it works" moment)

Deliver, for a single design partner, the complete launch→work→meter loop:

- **Agent:** install via `curl | sh`, enroll (join token → mTLS SVID), report inventory (NVML), heartbeat, establish outbound tunnel.
- **Control plane (modular monolith, Go):** OIDC login, org/team/project model, inventory, a **dead-simple scheduler** (single-node, whole-GPU only — no fractional, no preemption, no DRF), instance state machine, provisioning orchestrator.
- **Data plane:** container path only (containerd + NVIDIA Container Toolkit/CDI), whole-GPU attach.
- **Networking:** relayed SSH gateway (short-lived SSH certs) + reverse proxy for **JupyterLab** URL. One relay, one region.
- **Metering + ledger:** append-only usage events + double-entry ledger + a basic chargeback report. (Build the ledger now — §14.3.)
- **Frontend (Next.js + TS):** launch wizard, instance list/detail with live status, SSH string + Jupyter URL, basic usage view.
- **Deliberately NOT in M0:** VMs/Kata, fractional GPU, multi-org, marketplace, SAML/SCIM, advanced scheduling, multi-region, VSCode (Jupyter first).

### Milestone 1 — Make it usable and trusted (single org, in production with the design partner)

- **Idle auto-stop** (the killer cost feature, §2.3) with warning + grace.
- **Fractional GPU:** MIG on datacenter cards; time-slicing/MPS on consumer cards (same-trust only).
- **Quotas + RBAC** (org→team→project), **VSCode** endpoint, **templates catalog** (curated, scanned images).
- **Observability:** DCGM health → cordoning; Grafana fleet/utilization dashboards; per-instance logs in the UI.
- **Chargeback reports** by team/project/cost-center (the buyer's retention hook).

### Milestone 2 — Harden for isolation and a second/third org

- **VM/Kata isolation** path + cross-tenant placement rules (MIG/passthrough only).
- **Scheduler maturity:** filter/score with anti-fragmentation + topology awareness; priorities + preemptible tier; reservations.
- **SAML/SCIM** (first enterprise), **Vault** secrets, **SOC 2** controls underway.
- **Mesh client** option for high-bandwidth users; second region/relay.

### Milestone 3 — Multi-org SaaS (Phase 2) and marketplace groundwork

- Org self-service signup/billing; real payments (Stripe) on the existing ledger.
- `PlacementBackend` abstraction proven with a second backend (platform-owned region or a K8s-integration adapter).
- Begin curated provider onboarding (Phase 3 seed).

**Sequencing principle:** every milestone is shippable and produces a working product for a real user. Resist the urge to build M2's scheduler in M0. But *do* spend the extra day in M0 to make the agent outbound-only, the data model org-scoped, and the ledger double-entry — those three are the retrofits that would otherwise cost you a quarter each.

---

## 19. Team Structure

### 19.1 Founding team shape (first ~5 engineers)

At MVP you do not staff by service; you staff by **vertical-slice owners** who are senior enough to own a domain end-to-end and specialize as you grow. The scarce, must-have skills:

- **Distributed systems / control plane** — owns scheduler, orchestrator, consistency. The hardest correctness work.
- **Low-level systems / Linux / GPU / virtualization** — owns the agent, containerd/Kata/KVM, NVML/DCGM, GPU attach. **This is the rarest hire** — someone who genuinely understands GPU virtualization *and* Linux internals *and* can reason about isolation. Prioritize finding this person.
- **Networking** — owns the overlay, relays, NAT traversal, ingress. The make-or-break differentiator (§11); easy to underestimate.
- **Full-stack / product** — owns the Next.js console + API/BFF, turns the platform into a product people want to use.
- **Founder/CTO (you)** — architecture coherence, the §1.5 "get-right-early" calls, security posture, and saying no to scope.

Plus fractional/shared early: **SRE/platform** (becomes a full role fast — you're operating a fleet) and **security** (advisory early, dedicated by the time you chase SOC 2 / multi-org).

### 19.2 Scaling the org (by ~20–40 engineers)

Form pods around the natural service seams (§6.1), which is why those seams were drawn carefully:

- **Control Plane** (scheduler, orchestrator, inventory, API)
- **Data Plane & Agent** (runtimes, GPU, isolation, agent lifecycle)
- **Networking** (overlay, relays, ingress, isolation)
- **Frontend & Product** (consoles, UX, SDKs)
- **Platform/SRE** (our-cloud K8s, CI/CD, on-call, fleet ops)
- **Security** (isolation, compliance, supply chain)
- **Billing/Payments** (metering, ledger, marketplace money — stands up around Phase 3)

The org chart deliberately mirrors the architecture (Conway's Law, used on purpose): teams own the components whose boundaries you already designed for independent scaling and failure.

### 19.3 What to outsource / buy, not build

Buy identity (OIDC/SAML via the IdPs themselves), payments (Stripe/Stripe Connect), and managed Postgres/object storage in your own cloud. Build the things that are your moat: the agent, the networking, the scheduler, the metering/ledger correctness. Don't spend founding-team time rebuilding what's a commodity.

---

## 20. Final Recommended Architecture

### 20.1 The one-paragraph version

Build a **Go control plane** (modular monolith first, service-split at Stage B, cell-partitioned at Stage C) over a fleet of **single-binary Go agents** that run on customer hardware and **dial outbound** to a **regional relay fleet** (Rust on the data path), giving connectivity across any NAT/firewall. Provision via **tiered isolation** (containerd for same-org trust; Kata/KVM + MIG/VFIO for cross-tenant), placed by a **two-level, optimistic-concurrency scheduler** with DRF fairness, anti-fragmentation bin-packing, topology awareness, priorities, preemption, and reservations. Persist truth in **PostgreSQL** (org-sharded later), meter into an **append-only ClickHouse stream** feeding a **double-entry ledger** built from day one, and connect users via an **overlay + edge-authenticated SSH/HTTP gateways** issuing short-lived certs. Instrument everything with **OpenTelemetry** (push-based across NAT) + DCGM. Frontend in **Next.js/React/TypeScript** with TanStack Query and realtime sockets. Reject Kubernetes-on-customer-hardware and Firecracker-for-GPU; embrace Kubernetes for *our* services and borrow its DRA scheduling concepts. Architect so the marketplace (Phases 3–4) is a natural evolution of the same primitives — and treat fully-decentralized consumer compute (Phase 5) as an optional, narrow supply source, never the company's foundation.

### 20.2 The decision ledger (what we chose and why, at a glance)

| Decision | Choice | One-line rationale |
|---|---|---|
| Control-plane / agent language | **Go** | Cloud-native ecosystem + single-binary agent + fan-out concurrency |
| Data-path language | **Rust** (carved out) | No-GC determinism + memory safety on untrusted-input surface |
| Frontend | **Next.js + React + TS** (SPA escape hatch) | Right for the product; don't over-use Server Components for the console |
| Orchestration on customer HW | **Custom agent + scheduler**, not K8s | K8s assumes a cluster you control; customer HW is NAT'd & heterogeneous |
| Orchestration for our services | **Kubernetes** | That's what it's for |
| GPU isolation (cross-tenant) | **MIG / VM passthrough**, never time-slice | Only hardware isolation is a tenant boundary |
| Runtime | **Tiered**: containerd → Kata → KVM | Match isolation to trust; don't pay VM cost for same-org work |
| Firecracker for GPU | **Rejected** | No GPU passthrough as of 2026 (PCIe work paused) |
| Networking | **WireGuard overlay + outbound agent + relays** | The only model that works behind NAT without inbound ports |
| SSH access | **Edge gateway + short-lived SSH certs over the tunnel** | Central, audited, revocable; no static keys on instances |
| System of record | **PostgreSQL** (→ Citus shard by org) | Boring, correct, transactional |
| Metering | **Append-only events → ClickHouse** | Billing-grade time-series at scale; never in Postgres |
| Billing | **Double-entry ledger from day one** | Retrofitting accounting correctness is brutal |
| Scheduler | **Two-level, optimistic concurrency, DRF** | Scales without a global lock; fair across heterogeneous demand |
| Observability | **OTel + Prometheus/VictoriaMetrics + Loki + Tempo + DCGM**, push-based | One standard; pull is impossible across NAT |
| Scale pattern | **Modular monolith → services → cells** | Don't build Stage D on day one; don't corner yourself either |
| Decentralized Phase 5 | **Keep possible, don't depend on it** | Graveyard for good reasons; the substrate makes it optional |

### 20.3 The five things that, if gotten right, make everything else tractable

1. **The outbound agent + overlay network** — the foundation that makes "compute on hardware you don't own" actually work.
2. **The instance state machine + optimistic-concurrency scheduler with fencing** — the correctness core that prevents leaked GPUs and double-bills.
3. **Org-scoped data model + tiered isolation** — the tenancy spine that lets you go from one org to a marketplace without a rewrite.
4. **Append-only metering + double-entry ledger** — the money correctness that survives the jump to real payments and payouts.
5. **OpenTelemetry push-based observability + DCGM health** — the only way to operate a fleet you can't physically touch.

Get these five right and the rest of the document is execution. Get any one of them wrong and you will be rewriting the company in eighteen months.

---

*End of document. This is a living blueprint — revisit the §1.5 "get-right-early" list and the §20.2 decision ledger at each milestone, and update the §16 strategic read as the market (and your design partners) teach you what's actually true.*




