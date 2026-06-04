# Roadmap — NodeHive

> Near-term is committed and concrete; long-term is the north star and is **gated** —
> each phase only begins once the prior one has proven out. Discipline: *fund the wedge,
> earn the platform.* Detail: `GPU_Cloud_Business_Strategy.md`, `GPU_Platform_V1_Engineering_Spec.md`.

## Where we are
End of **Milestone 1** — the enroll-a-node loop works (agent → gRPC → control plane →
Postgres → HTTP). Moving into inventory + metrics. Ticket status lives in `TASKS.md`.

## Near-term — V1 (months 0–6): prove the wedge
The goal is one delighted, paying design partner running NodeHive weekly on real owned GPUs.

| Milestone | Outcome | Status |
|---|---|---|
| **M1 — Agent registers node** | Agent enrolls, heartbeats persist, `GET /nodes` works | ✅ done |
| **M2 — GPU inventory visible** | Agent enumerates GPUs; node + GPU detail in the UI | ▶ next |
| **M3 — Metrics pipeline** | DCGM/NVML sampling → utilization dashboard (fleet/node/GPU) | ◻ |
| **M4 — Usage, idle & chargeback** | Immutable usage records, idle-cost view, CSV chargeback | ◻ |
| **M5 — Workload launch + reclaim** | One-click GPU container (SSH/Jupyter), idle auto-stop | ◻ |
| **M6 — Pilot deployment** | Self-host packaging + first design partner live | ◻ |

The order is deliberate: **visibility first** (fastest learning + trust), reclaim/chargeback
next (the hard-dollar ROI story), launch last (the riskiest piece).

## Mid-term (post-PMF signals, ~months 6–18)
Multi-tenant hardening; SSO/SAML/SCIM (via WorkOS); on-prem / air-gap deployment mode if the
beachhead demands it; basic multi-AZ HA; cost/chargeback maturity. Series-A story = "N orgs,
M GPUs under management, $X chargeback tracked, install-to-first-instance < 1 day."

## Long-term vision — the five phases (NORTH STAR, not now)
Each phase is a separate bet, unlocked only by the previous one working:

1. **Phase 1 — Owned-GPU self-service (current).** Visibility, reclaim, self-serve, chargeback for a single org's own hardware.
2. **Phase 2 — Multi-org.** Same platform serving many organizations with hard tenancy isolation.
3. **Phase 3 — Provider marketplace.** Owners list idle capacity; the install base seeds liquidity (a *warm* start the pure marketplaces never had).
4. **Phase 4 — Hybrid cloud.** Burst from owned hardware to public cloud / neoclouds under one control plane.
5. **Phase 5 — Decentralized compute network.** The long-horizon "operating system for compute."

Why gated: Phases 3–5 are capital-intensive or two-sided markets already contested by
incumbents (Vast, RunPod, CoreWeave). We earn the right to attempt them by first becoming the
system of record for owned-GPU usage and cost. Building them early is the documented
runway-death risk.

## What success looks like at the V1 gate (month ~6)
Install-to-value < 1 day · time-to-first-instance < 5 min · a measured before/after
utilization number · one finance buyer who accepts the chargeback report · one paying pilot.
If that gate is missed, re-scope — don't push on.
