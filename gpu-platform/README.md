# GPU Platform — `nodehive/gpu-platform`

Turn the GPUs you already own into a self-service internal cloud:
**visibility → reclaim → self-service → chargeback.** V1 monorepo, single-org.

**Stack:** Go (control plane + agent) · PostgreSQL · gRPC · Next.js/React · monorepo.

> This is a **scaffold**. The structure, contracts (proto/SQL/REST), and interfaces are real;
> the implementations are stubs to be filled in starting at ticket **T-001**.
> Start here: [`docs/ENGINEERING_FOUNDATION.md`](docs/ENGINEERING_FOUNDATION.md).

## Repo map (short)
```
cmd/            entrypoints: controlplane (monolith), agent (node binary)
internal/       control-plane modules (domain, identity, inventory, telemetry,
                policy, workloads, billing, audit, agentgw, platform)
agent/          agent-only packages (discovery, metrics, executor, transport, updater)
proto/          gRPC contracts: node/metrics/workload/agent v1
gen/            generated code (proto, sqlc) — git-ignored, produced by `make generate`
migrations/     forward-only SQL (goose)
web/            Next.js frontend
deploy/         docker-compose + install scripts
scripts/        seed data, dev tooling
docs/           the engineering foundation (read this first)
```

## Prerequisites
- Go **1.23+**, Node **20+**, Docker + Docker Compose
- Dev tools (installed by `make tools`): `buf`, `sqlc`, `golangci-lint`, `goose`, `air`

## Quickstart — git clone → running system in < 15 minutes
```bash
cp .env.example .env
docker compose -f deploy/docker-compose.yml up -d   # Postgres + Adminer
make tools          # one-time: buf, sqlc, goose, golangci-lint, air
make generate       # proto + sqlc codegen -> gen/
make tidy           # go mod tidy (pins deps)
make migrate        # apply migrations to local Postgres
make seed           # dev org + projects + rate cards + sample fleet
make backend        # terminal 1: control plane (HTTP :8080, gRPC :9090)
make web            # terminal 2: Next.js dev server (:3000)
make agent          # terminal 3 (optional): run an agent against localhost
```
On first run the control plane bootstraps an admin from `DEV_BOOTSTRAP_ADMIN` in `.env` (default `admin@local` / `admin123`).

## Common commands
```bash
make generate   # regenerate proto + sqlc
make migrate    # goose up
make test       # go test ./... (+ web tests)
make lint       # golangci-lint + buf lint
make build      # build controlplane + agent into ./bin
```

## License / status
Internal V1 scaffold. Module path `github.com/nodehive/gpu-platform` — rename before first push if needed.
