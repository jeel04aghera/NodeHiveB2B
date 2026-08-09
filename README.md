# NodeHive

A B2B GPU cloud platform for provisioning, orchestrating, and billing GPU compute across a fleet of nodes. Built with Go, gRPC, PostgreSQL, and Next.js.

## Overview

NodeHive lets GPU cloud operators onboard compute nodes, allocate them to customer workloads, track utilization in real time, and bill usage automatically. A lightweight agent runs on every GPU node and streams telemetry back to a central control plane over a persistent gRPC connection, giving operators a live, accurate picture of fleet capacity at all times.

The platform is designed around three goals: **efficient allocation** (get workloads onto available GPUs fast), **cost control** (don't pay for idle capacity), and **accountability** (every action is authenticated, billed, and audited).

## Features

- **Node Enrollment** — Lightweight agent auto-registers GPU nodes on startup and streams real-time telemetry (utilization, health, availability) over a persistent gRPC connection.
- **Workload Orchestration** — Automated GPU allocation using first-fit placement, with full launch/stop lifecycle management for customer workloads.
- **Fleet Utilization Dashboards** — Time-partitioned metrics give operators a live view of GPU usage, capacity, and trends across the entire fleet.
- **Idle Auto-Stop** — Policy-based scheduling detects idle workloads and automatically stops them to prevent wasted spend.
- **Usage-Based Billing** — Every allocation is metered and converted into chargeback reports for accurate, usage-based invoicing.
- **Secure Access** — JWT authentication on all API and gRPC operations, with full audit logging for compliance and traceability.
- **Containerized Deployment** — Docker-based deployment for reproducible, horizontally scalable infrastructure.

## Tech Stack

| Layer | Technology |
|---|---|
| Agent / Backend | Go, gRPC |
| Database | PostgreSQL |
| Frontend | Next.js |
| Deployment | Docker |
| Auth | JWT |

## Architecture

```
                                   ┌──────────────────────────┐
                                   │        Dashboard          │
                                   │   (Next.js Frontend)      │
                                   │  fleet metrics · billing  │
                                   │  audit logs · admin UI    │
                                   └────────────┬──────────────┘
                                                │ REST / HTTPS
                                                ▼
                        ┌───────────────────────────────────────────┐
                        │              Control Plane (Go)            │
                        │                                             │
                        │  ┌───────────────┐   ┌───────────────────┐ │
                        │  │  Allocation    │   │  Scheduler         │ │
                        │  │  Engine        │   │  (idle auto-stop,  │ │
                        │  │  (first-fit    │   │   policy-based)    │ │
                        │  │   placement)   │   └───────────────────┘ │
                        │  └───────────────┘                         │
                        │  ┌───────────────┐   ┌───────────────────┐ │
                        │  │  Auth          │   │  Billing /         │ │
                        │  │  (JWT + audit  │   │  Chargeback        │ │
                        │  │   logging)     │   │  Engine            │ │
                        │  └───────────────┘   └───────────────────┘ │
                        └───────────────┬─────────────────┬──────────┘
                                        │ SQL              │ gRPC (bidi stream)
                                        ▼                  ▼
                        ┌───────────────────────┐  ┌─────────────────────────┐
                        │      PostgreSQL         │  │     GPU Node Fleet       │
                        │  nodes · allocations    │  │                          │
                        │  usage records · audit  │  │  ┌────────┐ ┌────────┐  │
                        │  logs                   │  │  │ Agent  │ │ Agent  │  │
                        └───────────────────────┘  │  │ Node 1 │ │ Node 2 │  │
                                                     │  └────────┘ └────────┘  │
                                                     │       ...  N nodes      │
                                                     └─────────────────────────┘
```

**Flow:**
1. The **agent** on each GPU node registers with the control plane and opens a persistent gRPC stream, continuously pushing telemetry (utilization, health, availability).
2. When a workload request comes in, the **allocation engine** picks a node using first-fit placement and instructs the agent to launch it via gRPC.
3. The **scheduler** watches live telemetry and stops workloads that go idle, per configured policy.
4. Every allocation and stop event is metered by the **billing engine**, which generates chargeback reports.
5. All requests pass through the **auth layer**, which validates JWTs and writes to the audit log.
6. The **dashboard** polls the control plane over REST to display fleet metrics, billing, and audit history.

## Getting Started

### Prerequisites

- Go 1.21+
- Node.js 18+
- PostgreSQL 14+
- Docker

### Setup

```bash
# Clone the repo
git clone https://github.com/Jeel1210/NodeHive.git
cd NodeHive

# Backend
cd backend
go mod download
go run main.go

# Frontend
cd ../frontend
npm install
npm run dev
```

### Environment Variables

```env
DATABASE_URL=postgres://user:password@localhost:5432/nodehive
JWT_SECRET=your_jwt_secret
GRPC_PORT=50051
```

## Usage

1. Start the control plane and dashboard.
2. Deploy the agent binary on a GPU node — it auto-registers via gRPC.
3. Use the dashboard to allocate workloads, monitor telemetry, and view billing reports.

## Roadmap

- [ ] Multi-region node support
- [ ] Advanced scheduling policies (bin-packing, priority queues)
- [ ] Webhook-based billing exports
