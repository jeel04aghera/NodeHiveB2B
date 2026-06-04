# NodeHive — TODO (ordered by priority)

## ✅ Completed 2026-06-03 (this session)

- [x] **SSH/Jupyter now actually work** — Root cause: dev mode used a *simulated* executor that returned fake `localhost:2200x` endpoints connected to nothing. Fixed: dev mode now keeps synthetic GPU discovery/metrics (no NVIDIA needed) but runs **real Docker containers** with GPU passthrough off. Verified end-to-end: SSH password login succeeds into a real container; JupyterLab serves HTTP 200.
  - Decoupled GPU passthrough from dev mode (`AGENT_GPU_PASSTHROUGH`, `AGENT_ADVERTISE_HOST`).
  - Executor now installs+starts the daemons it advertises: sshd for SSH, and actually launches `jupyter lab` (was previously only exposing the port and running the image default, so Jupyter never started). SSH+Jupyter can both be enabled on one workload.
  - Agent falls back to the simulated executor only when Docker is absent.
- [x] **Removed dummy data** — Deleted 8 fabricated workloads (fake endpoints), the seed-only `dev-node-1` decoration node + its phantom GPUs, and stale synthetic metrics. `seed_dev.sql` no longer seeds fake nodes/GPUs — the fleet view only shows nodes that really enrolled.
- [x] **Fixed Jupyter-only workloads being inaccessible in the UI** — The access panel and tab were gated entirely behind `expose_ssh`, so a Jupyter-only workload showed a blank panel with no link. Replaced the "SSH" tab with an "Access" tab that renders SSH and Jupyter sections independently, with a proper "Open Jupyter" button.
- [x] **Offline/stuck workload sweep** — Verified already implemented and scheduled (`SweepStuck` on a 30s ticker marks workloads `failed` when their node goes offline or they're stuck `stopping` >2min, and frees GPUs). The earlier P0 was stale.

## P0 — Required before first pilot (blocking)

- [ ] **mTLS enforcement** — gRPC is TLS-capable now (`make certs` generates certs) but `GRPC_INSECURE=true` is still the default. Flip the default, enforce in prod, document in .env.example.
- [ ] **Token expiry + rotation** — JWT sessions expire after 24h (good). Enrollment tokens don't rotate. Add `POST /enrollment-tokens/{id}/revoke`. Add `last_used_at` tracking.
- [ ] **Credential rotation** — Agent credentials (stored as hash in `agent_credentials`) have no expiry or rotation. Add `expires_at`, rotate on reconnect after threshold.
- [x] ~~Offline agent TTL job~~ — Done. `SweepStuck` (30s ticker) marks active workloads `failed` when their node is offline or stuck `stopping` >2min, and frees GPUs.
- [ ] **End-to-end test on real GPU box** — Run `make dev && make backend && make agent` (no `--dev`) on a Linux machine with NVIDIA drivers + Container Toolkit. Confirm GPU passthrough (`--gpus`) actually binds, metrics come from `nvidia-smi`, and SSH/Jupyter work with a GPU attached. (Dev-mode path is now verified on a GPU-less Mac.)

## ✅ Completed 2026-06-03 (session 2 — prebuilt images + lifecycle)

- [x] **Root-caused "containers stop shortly after launch / no SSH endpoint"** — runtime package install in arbitrary images: `ubuntu:22.04` crashed (`/etc/ssh` missing, apt silently failed under `set -e`); `jupyter/datascience-notebook` crashed (non-root `jovyan` can't `mkdir /run/sshd`). Container died → ports `[]` → no endpoint.
- [x] **Prebuilt workload images** (`deploy/images/`, `build.sh`): `nodehive/ssh-base` (120MB), `nodehive/jupyter-base` (506MB), `nodehive/ssh-jupyter-base` (554MB). Daemons pre-installed, run as root, entrypoint reads `SSH_PASSWORD`/`JUPYTER_TOKEN` from env. **Zero runtime install** → instant start.
- [x] **Executor rewrite** — selects the prebuilt image by access flags (no `--entrypoint` overlay), proper ephemeral host-port mapping (`-p 127.0.0.1::22`), and a **liveness gate**: confirms `State.Running` after launch; if the container exited it captures logs and reports `failed` (no more fake `running`).
- [x] **Jupyter token** generated per-workload, endpoint returned as `host:port/lab?token=…` so "Open Jupyter" opens authenticated.
- [x] **Failure reason propagation** — agent now sends exit code + container logs on launch failure; persisted to the workload `logs` field.
- [x] **Honest dev GPU labels** — synthetic GPUs are `Development GPU (Synthetic)` (was fake `NVIDIA RTX 4090`); CPU `Development CPU (synthetic)`, driver/cuda `n/a (dev)`.

### Still open from this area
- [ ] **Custom image + SSH/Jupyter overlay** — access workloads currently always run the nodehive base image; the user's chosen `template_id` is ignored when SSH/Jupyter is on. To run SSH/Jupyter *inside* a user's own image, build a derived image (FROM user image) at enroll/launch, or inject a static sshd binary. Update the launch-form template list to reflect this.
- [ ] **Jupyter open-token security** — token is the SSH password (or random); fine for dev, but it travels in the URL. For prod, prefer a one-time token + reverse proxy.
- [ ] **Combo supervision** — sshd backgrounds, Jupyter foregrounds; if Jupyter dies the container exits even if sshd was healthy. Add tini + a supervisor for combo workloads.
- [ ] **Registry distribution** — `build.sh` builds locally on one host. Push `nodehive/*` to a registry so multi-node fleets don't each rebuild.
- [ ] **Decide fate of frontend demo mode** (`web/lib/demo-data.ts`) — last fake-data source, opt-in via `nh_demo`.

## P1 — Core product quality

- [ ] **Real-time UI updates via SSE** — Currently polling (15s nodes, 10s workloads). Add `GET /events` Server-Sent Events stream; frontend subscribes and invalidates TanStack Query cache on relevant events. Removes the polling lag.
- [ ] **Workload logs live streaming** — Currently: agent sends logs on stop only. Add periodic log push every 30s while workload is running. Frontend already has the logs panel wired.
- [ ] **Jupyter URL auto-open** — When `jupyter_endpoint` is set, show a proper "Open Jupyter" button that opens `http://<host>:<port>` in a new tab. Currently shown but needs token handling if the image requires one.
- [ ] **Node detail GPU utilization per-GPU chart** — `/metrics/utilization?scope=gpu&id=<gpu_id>` exists, the node detail page only shows node-level. Add per-GPU sparklines.
- [ ] **Idle cost alert banner** — If `idle_cost_24h > threshold` (configurable per org), show a dismissible banner on the dashboard overview. Data is already in `FleetSummary`.
- [ ] **Chargeback CSV export** — Backend returns JSON. Add `Accept: text/csv` header handling on `GET /billing/chargeback`. Frontend billing page already has a CSV download button that builds CSV client-side — wire to server for completeness.

## P2 — Hardening + ops

- [ ] **`sqlc` adoption** — `sqlc.yaml` is staged. Replace hand-written pgx queries with generated type-safe code. Reduces SQL typo risk.
- [ ] **Metrics partitioned table maintenance** — `gpu_metrics` is range-partitioned by `ts`. Add a monthly cron job that creates next month's partition and drops partitions older than 90 days.
- [ ] **Rate limiting on HTTP API** — Add a simple token-bucket middleware (e.g. `golang.org/x/time/rate`). Without it the API is unbounded.
- [ ] **Structured error codes on frontend** — API errors return `{"error":{"code":"...","message":"..."}}`. The frontend catches raw error strings. Parse the `code` field and show user-friendly messages per code.
- [ ] **Agent self-update** — `UpdateAgent` proto message is defined but not handled. Add signed binary fetch + restart flow. V1: just log the command and ignore.
- [ ] **Audit log UI page** — `GET /audit-logs` endpoint exists and writes events. Build a simple `/audit` frontend page with date filter and action search.

## P3 — Multi-tenancy prep (post-PMF)

- [ ] **Organization CRUD** — Currently single-org hardcoded via seed. Add `POST /orgs`, `PUT /orgs/{id}/settings` for idle threshold, default rate, currency.
- [ ] **Per-org RBAC** — Role is `admin|user`. Add `viewer` role (read-only). Consider project-level access control.
- [ ] **SSO / SAML** — WorkOS integration for enterprise orgs. Current auth is email+password only.
- [ ] **Multi-AZ control plane** — Dispatcher is in-memory (single process). Replace with Redis pub/sub so multiple control-plane replicas can route commands to any agent.

## P4 — V2 product surface

- [ ] **Workload templates CRUD** — `template_id` is just an image string right now. Add a `templates` table with name, image, default env, GPU requirements. UI to manage templates.
- [ ] **Project assignment on workloads** — `project_id` is in the schema but not exposed in the launch form. Add project picker in the launch modal; required for accurate chargeback.
- [ ] **Idle auto-stop alerts** — `SweepIdle` stops workloads silently. Add `POST /orgs/{id}/webhooks` or email notification before auto-stop (5min warning).
- [ ] **Provider marketplace mode** — Phase 3 of the roadmap. Owners list idle capacity. Separate repo/service.

---

## Current backend processes (running)

```
Postgres   localhost:5432   (docker)
Control plane  localhost:8080 (HTTP) + :9090 (gRPC)
Next.js    localhost:3000

Login: admin@dev.local / admin123
```
