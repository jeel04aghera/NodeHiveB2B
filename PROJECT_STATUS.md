# NodeHive — Project Status

> Last updated: 2026-06-02  
> For full engineering context, read `AI_HANDOFF.md`.  
> For task tracking, read `TASKS.md`.

---

## Where we are

**Milestone 5 (Workload launch + reclaim) is functionally complete in dev.**  
The platform runs against a real Postgres with real data. All core flows are wired end-to-end. The next gate is testing on a real GPU box.

## Milestone status

| Milestone | Outcome | Status |
|---|---|---|
| M1 — Agent registers node | Enrollment, heartbeat, `GET /nodes` | ✅ Complete |
| M2 — GPU inventory visible | nvidia-smi discovery → DB → UI | ✅ Complete |
| M3 — Metrics pipeline | 30s sampling → `gpu_metrics` → charts | ✅ Backend complete. Chart needs real GPU agent for data |
| M4 — Usage, idle & chargeback | Usage records, idle auto-stop, chargeback report | ✅ Complete |
| M5 — Workload launch + reclaim | Docker launch, SSH access, idle reclaim | ✅ Complete in dev. Needs GPU box test |
| M6 — Pilot deployment | Install script, security hardening, first partner | 🟡 In progress |

## Currently running (dev machine)

```
Postgres       localhost:5432   (docker)
Control plane  localhost:8080 / :9090  (go run ./cmd/controlplane)
Frontend       localhost:3000   (npm run dev)

Login: admin@dev.local / admin123
```

## What works right now (verified 2026-06-03)

- Login with JWT, 24h session
- Fleet shows only nodes that really enrolled (fake seed node removed)
- **Workload launch with REAL SSH + Jupyter** — dev mode runs actual Docker
  containers (GPU passthrough off). Verified end-to-end on 2026-06-03:
  - SSH: password login into a live container succeeds (`alpine:3.19`, port mapped, sshd up).
  - Jupyter: `jupyter lab` serves HTTP 200 (`python:3.11-slim`, `/api` → version 2.19.0).
  - Stop tears the container down and frees GPUs.
- Chargeback report by project or user
- Rate card CRUD (Settings → Rate Cards)
- User management (Settings → Users)
- Enrollment token generation
- Node detail page with GPU list
- GPU inventory with status filter
- Offline/stuck workload sweep (30s ticker)

## Run it (dev, GPU-less Mac/laptop)

```
make dev                      # postgres + migrate + seed (no fake nodes)
go run ./cmd/controlplane     # :8080 HTTP, :9090 gRPC
go run ./cmd/agent --server localhost:9090 --insecure --token dev-enroll-token --dev
cd web && npm run dev         # :3000
```
`--dev` ⇒ synthetic GPU inventory/metrics (no NVIDIA needed) **but real Docker
containers** for workloads, advertised as `localhost:<port>`. Requires Docker running.

## What needs a real GPU box to test

- GPU discovery (nvidia-smi enumeration)
- Metrics sampling (utilization chart population)
- Workload SSH access (Docker + NVIDIA Container Toolkit)
- Idle auto-stop (needs real utilization data)

## Pilot readiness blockers (P0)

1. mTLS not enforced by default
2. Enrollment token revoke missing
3. Workloads stuck `running` when agent crashes
4. Never tested on real GPU hardware

## Progress

- Backend: 90%
- Agent: 75%
- Frontend: 80%
- Infrastructure: 65%
- Security: 40%
- Pilot readiness: 45%
