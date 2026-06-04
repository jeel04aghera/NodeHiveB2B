# Project Context — NodeHive

> Read this first. It orients a new engineer (or an AI agent) in two minutes, then
> points to the deeper docs. Keep it short and current.

## What this is
NodeHive is a B2B platform that turns the GPUs an organization **already owns** into a
self-service internal cloud. The product loop is: **see real utilization → reclaim idle
capacity → let engineers self-serve launch → hand finance a chargeback report.** V1 is
single-org and installs on the customer's own hardware via a lightweight agent.

## Why (the thesis)
Enterprise GPU clusters run at roughly **5% utilization** (Cast AI, 2026) — a widely cited
"$401B" waste problem — and for the first time since 2006 GPU prices are *rising*, so the
waste keeps getting more expensive. Owners have no cloud-like self-service and no cost
accountability for hardware they bought in the 2023–2025 buying spree.

We are the **cost-recovery + self-service layer for owned GPUs**. We are explicitly **not**:
- a GPU **marketplace** (Vast/RunPod already won the cheap long tail), or
- a **neocloud** that rents GPUs (CoreWeave/Lambda own the frontier).

Full reasoning: see `GPU_Cloud_Business_Strategy.md`.

## Constraints
2 founders · 6 engineers · 18 months runway · no customers/brand/distribution yet.
Optimize for a **delighted paying design partner in ~6 months**, not a platform in 36.

## Current state
The first executable vertical slice is working: an **agent enrolls over gRPC, heartbeats
persist to Postgres, and `GET /api/v1/nodes` returns the node.** Tickets T-001–T-004 are
done. See `TASKS.md` for exact status and `ROADMAP.md` for what's next.

## Repo layout
```
.
├── PROJECT_CONTEXT.md                  # this file
├── ARCHITECTURE.md                     # current (V1) system shape
├── ROADMAP.md                          # near-term milestones + long-term phases
├── TASKS.md                            # the 30-ticket backlog + status
├── GPU_Cloud_Platform_Technical_Design.md   # 10-year north-star architecture (reference)
├── Adversarial_Architecture_Review.md       # what we deliberately did NOT build, and why
├── GPU_Cloud_Business_Strategy.md           # market, wedge, GTM, investor view
├── GPU_Platform_V1_Engineering_Spec.md      # the approved V1 MVP spec
└── gpu-platform/                       # the actual codebase (Go + Next.js monorepo)
    ├── README.md                       # how to run it locally (< 15 min)
    └── docs/ENGINEERING_FOUNDATION.md  # implementation detail, contracts, full ticket list
```

## V1 guardrails (non-negotiable without a written decision)
- **No marketplace.** **No custom scheduler** (first-fit now; adopt NVIDIA KAI/DRA later).
- **No custom overlay network** (buy Tailscale/Cloudflare Tunnel).
- **No multi-region, no HA, single org per deployment.**
- **Don't position as "a GPU cloud."** The pitch is "reclaim + self-serve the GPUs you own."

These exist because the team's biggest risk is building three hard-tech products at once and
running out of money before PMF (see the adversarial review).

## How to run it
See `gpu-platform/README.md`. TL;DR: `make tools && make generate && make dev`, then
`make backend` and `make agent` in two terminals, then `curl localhost:8080/api/v1/nodes`.

## The two questions that actually decide V1 (keep them in view)
1. **Can the agent get installed and dial out** from a real customer's locked-down GPU box?
2. **Is the owned-GPU pain as acute as we think** — will an owner pay to reclaim it?
Everything is sequenced so the first pilot answers both, cheaply.
