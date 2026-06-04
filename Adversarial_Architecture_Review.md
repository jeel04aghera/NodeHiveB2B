# Adversarial Architecture Review — GPU Cloud Platform

**Review type:** Pre-implementation architecture review (funding gate)
**Document under review:** *GPU Cloud Platform — Technical Design Document v1*
**Panel:** AWS Distinguished Engineer · Kubernetes SIG-Architecture reviewer · NVIDIA infrastructure architect · Databricks founding infra engineer · Snowflake principal architect · Infra startup CTO · Enterprise security reviewer
**Stance:** Adversarial. The panel's job is to find what kills the company, not to validate the vision. Nothing below is a defense of the document.
**Date:** 2026-06-01

---

## Panel funding recommendation (read this first)

**CONDITIONAL — DO NOT FUND AS WRITTEN.** Fund a re-scoped 18-month plan, not this one.

The document is a genuinely strong *10-year north-star architecture* and a *dangerous 18-month build plan*. Those are different artifacts and the author has conflated them. The design is internally coherent and the hard problems are correctly identified. But as a funding request it fails three tests:

1. **It signs a 6-person team up to build three separate hard-tech products at once** — a distributed scheduler, a global overlay network, and a fleet agent — each of which is a well-funded company on its own (Nomad/Borg, Tailscale, the entire device-plugin ecosystem). This is the dominant risk and it is a runway-death risk, not a technical one.
2. **Its single hardest component is already free.** NVIDIA acquired Run:ai and open-sourced its **KAI Scheduler** (Apache-2.0, Kubernetes-native, gang-scheduling + quotas + fairness — i.e., 80% of §8), now a CNCF Sandbox project, plus it donated the **DRA driver** to CNCF. Building a bespoke GPU scheduler in 2026 is rowing directly against the GPU vendor's own open-source current.
3. **The biggest risk in the whole plan is not in the document.** It is non-technical: *enterprise security teams will resist installing a third-party, outbound-connected, remote-command-executing root daemon on their GPU servers.* The entire architecture is load-bearing on that install succeeding, and the doc treats it as solved.

The document also never once mentions **Slurm** or **Run:ai** — the actual incumbents for "orchestrate the GPUs we already own" in the exact segments it targets (research labs, universities, enterprises). A design that proposes to win a market without naming its incumbents has not yet been pressure-tested against reality.

What follows: section-by-section teardown (Part I), the 20 risks ranked (Part II), over- and under-engineering audits (Parts III–IV), a decision-reversal-cost matrix (Part V), and the architecture the panel would actually build with 2 founders / 6 engineers / 18 months (Part VI). A scorecard against the stated success criteria is in the appendix.

---

# Part I — Section-by-Section Review

> Each section carries a one-line **Scorecard** across the nine evaluation lenses (Correct / Premature / Complexity / Scales-to-100k-GPU / Scales-organizationally / Lock-in / Ops-burden / 10-yr-survival), then the eight review fields. Lenses are abbreviated; "—" means not-applicable.

## Review of §1 — Executive Summary

**Scorecard —** Correct: mostly · Premature: the *framing* invites premature building · Complexity: signals high · Scales-100k: aspirationally · Org-scale: not as written · Lock-in: low · Ops-burden: foreshadows high · 10-yr: survives as vision.

- **Current recommendation (doc):** Build a custom Go control plane + agents over an outbound overlay; tiered isolation; custom scheduler; double-entry ledger from day one; reject K8s-on-customer-HW and Firecracker-for-GPU.
- **Strengths:** The "outbound agent is the whole ballgame" insight is correct and is the document's best idea. The §1.5 get-right-early/defer table is exactly the right instinct. The honesty about Phase 5 is commendable.
- **Weaknesses:** The summary lists ~13 infrastructure systems and three from-scratch hard-tech builds as if they were a normal stack. It preaches "modular monolith, defer" while summarizing a Stage-C system. A funder reads this and sees 36 months of platform work before a customer is delighted.
- **Hidden assumptions:** (1) that you must *build* the overlay/scheduler/agent rather than *assemble* them; (2) that the control plane is SaaS (customers phone home); (3) NVIDIA-only.
- **Failure modes:** Team spreads across three hard problems, none reaches production quality, runway ends pre-PMF.
- **Alternative approaches:** Reframe the summary around the *one* defensible build (the access/UX/metering layer over existing orchestrators) and an explicit buy-list for the rest.
- **What AWS would do:** AWS *would* build all of it bespoke (they built Nitro, their own hypervisor, their own everything) — because they have 10,000 engineers and a decade. That is precisely why copying AWS's build-everything posture is the wrong lesson for a startup.
- **What a startup should do:** Treat this summary as the Series-B architecture. Write a separate, smaller seed-stage summary.
- **Verdict:** Excellent vision doc; unsafe build charter. Keep as north star, do not fund as plan.

## Review of §2 — Product Architecture

**Scorecard —** Correct: yes · Premature: no · Complexity: low · Scales-100k: — · Org-scale: yes · Lock-in: low · Ops-burden: low · 10-yr: yes.

- **Current recommendation (doc):** Three dashboards over one platform; eight first-principles capabilities; idle-auto-stop and templates as first-class.
- **Strengths:** This is the strongest section. The eight-capability decomposition is sound, idle-auto-stop is correctly identified as the highest-ROI feature, and "templates are the product, not the OS" is exactly right.
- **Weaknesses:** The personas are described without their *incumbent context*. The "org admin" buyer very often already owns Slurm or a K8s+Kubeflow/Run:ai stack; the product's job is then *integration/replacement*, not greenfield. That changes the product materially and is absent.
- **Hidden assumptions:** That the buyer has "no modern cloud experience" — many target buyers have *some* (Slurm queues, a K8s cluster) and the pain is UX/chargeback, not absence.
- **Failure modes:** Building a self-service console that ignores the queue/scheduler the customer already trusts; users keep using Slurm and your console becomes shelfware.
- **Alternative approaches:** Position explicitly as "the self-service + chargeback layer on top of what you run today (Slurm, K8s, raw nodes)."
- **What AWS would do:** Obsess over the console's time-to-first-instance; AWS would A/B the launch funnel relentlessly.
- **What a startup should do:** Same — and pick ONE persona/segment to delight first (probably the developer at a GPU-owning AI startup), not all three.
- **Verdict:** Keep nearly as-is; add incumbent-awareness. Lowest-risk part of the document.

## Review of §3 — High-Level System Design

**Scorecard —** Correct: yes · Premature: partially · Complexity: medium-high · Scales-100k: yes · Org-scale: medium · Lock-in: low-medium · Ops-burden: high · 10-yr: yes.

- **Current recommendation (doc):** Two-plane split; relay tier distinct from control services; metering one-way and off the critical path.
- **Strengths:** The control/data-plane split is textbook-correct. Separating the relay (data path) from control services (decision path) is a senior insight and right. Metering-off-the-critical-path is correct.
- **Weaknesses:** The diagram shows ~10 control-plane services + a relay fleet + a command bus + four datastores as the *baseline* picture. For a seed-stage system this is the Stage-C diagram; it will anchor the team toward microservices prematurely (contradicting §6's "modular monolith" caveat). The two messages fight.
- **Hidden assumptions:** That the control plane is centrally hosted (no air-gapped/on-prem deployment), which several target customers will reject.
- **Failure modes:** Team builds the 10-box diagram; integration overhead between services consumes the runway; nothing ships.
- **Alternative approaches:** Show two diagrams — "what you run at 10 customers" (monolith + Postgres + bought overlay) vs. "what it becomes." The doc only shows the second.
- **What AWS would do:** Exactly this diagram, as separate services, with separate teams per box.
- **What a startup should do:** Collapse boxes 1–10 into one deployable; keep the *interfaces* clean so they can split later.
- **Verdict:** Right structure, wrong stage emphasis. Add the "today" diagram.

## Review of §4 — Detailed Component Design

**Scorecard —** Correct: yes · Premature: yes (service count) · Complexity: high · Scales-100k: yes · Org-scale: no (as 10 services / 7 pods for 6 eng) · Lock-in: low · Ops-burden: very high · 10-yr: yes.

- **Current recommendation (doc):** Ten logical services with consistency tiers; instance state machine; "modular monolith first" caveat.
- **Strengths:** The instance state machine (§4.3) is the correct backbone and is genuinely well-specified — this is the kind of correctness that prevents leaked-GPU/double-bill bugs. The consistency-tier table is mature thinking.
- **Weaknesses:** The "modular monolith first" sentence is one line buried under a ten-row services table; teams build what they see in the table. The provisioning-orchestrator-vs-scheduler split is correct but is a Stage-B refinement presented as Stage-A.
- **Hidden assumptions:** That you can staff ten service-shaped responsibilities with a founding team. §19 then proposes seven pods — for six engineers. The org math doesn't close.
- **Failure modes:** Premature service decomposition → distributed-monolith with all the ops cost and none of the scaling benefit.
- **Alternative approaches:** One service, ten *packages*. Enforce boundaries with module/lint rules, not network calls.
- **What AWS would do:** Ten services, ten two-pizza teams.
- **What a startup should do:** One binary, clean packages; split only the scheduler and metering when load forces it.
- **Verdict:** Keep the state machine verbatim; demote the services table to "future decomposition."

## Review of §5 — Technology Selection

**Scorecard —** Correct: Go=yes, Rust-now=no · Premature: the Rust carve-out and SAML-defer · Complexity: two languages = high · Scales-100k: yes · Org-scale: Go yes, two-langs harder hiring · Lock-in: low · Ops-burden: medium · 10-yr: yes.

- **Current recommendation (doc):** Go for control plane + agent; Rust carved out for the data path; Next.js/React/TS frontend with SPA escape hatch; Python in its lane.
- **Strengths:** The Go reasoning (single static agent binary, cloud-native ecosystem, fan-out concurrency) is correct and well-argued, not cargo-culted. The frontend analysis (don't over-use Server Components for an authenticated console) is unusually honest and right.
- **Weaknesses:** **Introducing Rust pre-PMF is premature optimization.** It doubles the toolchain, the hiring pool, and the cognitive load for a 6-person team, to optimize a relay you should not be building yet (see §11). The doc even justifies Rust *for the data-path proxy* — but if you buy the overlay (Tailscale/Cloudflare), that proxy doesn't exist, so the Rust rationale evaporates. **OIDC-first / SAML-later directly contradicts "assume enterprise customers"** — enterprise procurement gates on SAML+SCIM; deferring it can block the very deals targeted.
- **Hidden assumptions:** That a data-path proxy will be built in-house at all; that early customers won't demand SAML.
- **Failure modes:** Two-language sprawl; a half-finished Rust relay; an enterprise deal stalls on missing SAML.
- **Alternative approaches:** **Go everywhere.** Revisit Rust only when a *measured* bottleneck survives profiling. Buy SSO/SAML/SCIM from WorkOS/Auth0 (turns a multi-month build into a config) — this simultaneously kills the SAML-defer problem.
- **What AWS would do:** Polyglot by service, because each team chooses; AWS can afford Rust specialists.
- **What a startup should do:** One backend language. Buy auth. Defer Rust indefinitely.
- **Verdict:** Go: approved. Rust-now: rejected as premature. Buy auth instead of phasing it.

## Review of §6 — Control Plane Design

**Scorecard —** Correct: yes · Premature: boundaries yes, as-services yes · Complexity: medium · Scales-100k: yes · Org-scale: medium · Lock-in: low · Ops-burden: medium · 10-yr: yes.

- **Current recommendation (doc):** Service boundaries by "fails-together/scales-differently"; gRPC sync + NATS async; tiered consistency.
- **Strengths:** The boundary-drawing rationale is correct and the consistency tiering (strong for money/identity, read-recent for inventory, eventual for telemetry) is exactly how a mature platform reasons.
- **Weaknesses:** Same monolith-vs-services tension. NATS-now-then-Kafka-later (stated in §5.3/§10) is a *self-inflicted migration*: you will rewrite producers/consumers. Pick one bus or use Postgres-as-queue until volume is real.
- **Hidden assumptions:** That you need a message bus at all at 10 customers (you can run on Postgres `LISTEN/NOTIFY` + a jobs table far longer than the doc implies).
- **Failure modes:** Two messaging systems in production; a migration nobody scheduled.
- **Alternative approaches:** Postgres-backed queue → one durable bus (pick NATS *or* Redpanda) when metering volume justifies it. Don't plan a NATS→Kafka swap.
- **What AWS would do:** SQS/Kinesis from day one (managed; no ops).
- **What a startup should do:** Managed queue (SQS/PubSub) or Postgres queue; avoid running your own broker early.
- **Verdict:** Sound reasoning; eliminate the planned bus migration; defer the bus itself.

## Review of §7 — Data Plane Design

**Scorecard —** Correct: container-tier yes, three-tier-now no · Premature: Kata/KVM yes · Complexity: three runtimes = high · Scales-100k: yes · Org-scale: medium · Lock-in: NVIDIA (high) · Ops-burden: high · 10-yr: yes.

- **Current recommendation (doc):** Tiered isolation (containerd → Kata → KVM/VFIO); Firecracker rejected for GPU; MIG-only cross-tenant; K8s-on-customer-HW rejected; borrow DRA.
- **Strengths:** The Firecracker rejection is *correct and current* (verified: no GPU passthrough in 2026). The MIG-only-cross-tenant rule is correct and the kind of invariant that prevents a CVE. The K8s-on-heterogeneous-customer-HW skepticism is defensible.
- **Weaknesses:** **Specifying three runtimes (container, Kata, full VM) is building for a multi-tenant future you don't have.** Phase 1 is single-org; you need *containers only*. Kata + KVM + VFIO passthrough is months of hardening for demand that doesn't exist yet. The whole section is also **NVIDIA-locked** (NVML/DCGM/MIG/CDI) with AMD/ROCm and other accelerators relegated to a single risk-table line — a real 10-year lock-in for an "OS for compute."
- **Hidden assumptions:** That you'll do cross-tenant placement on shared hosts early (you won't, in Phase 1); that all GPUs are NVIDIA.
- **Failure modes:** Engineering sunk into VM/Kata paths nobody uses; a future AMD/Inferentia customer requires re-plumbing the agent.
- **Alternative approaches:** Containers only now. Add VM/Kata exactly when the first multi-org/sensitive-workload deal requires it (it's *additive*, low regret — see Part V). Abstract the GPU vendor behind a `DeviceManager` interface even if only NVIDIA is implemented.
- **What AWS would do:** Build a custom hypervisor (Nitro) and offer bare-metal + VM isolation — because they sell to mutually-hostile tenants at scale.
- **What a startup should do:** Containers + honest isolation messaging for single-org; defer VMs.
- **Verdict:** Right *menu*, wrong *timing*. Cook one dish now. Fix the NVIDIA-only assumption at the interface level cheaply.

## Review of §8 — Scheduler Design  ⚠️ (highest-stakes section)

**Scorecard —** Correct: as engineering yes, as a *build* no · Premature: **yes, dangerously** · Complexity: **very high** · Scales-100k: yes if you survive building it · Org-scale: needs a dedicated team you don't have · Lock-in: low · Ops-burden: high · 10-yr: the *capability* yes, the *bespoke build* no.

- **Current recommendation (doc):** Build a two-level, optimistic-concurrency scheduler with DRF fairness, anti-fragmentation bin-packing, topology awareness, priorities, preemption, reservations, fencing.
- **Strengths:** The *engineering* is correct and senior — optimistic concurrency + fenced leases + node-level admission is the right design, the pseudocode is sound, the failure-handling (§8.6) is thorough. If you must build a scheduler, this is roughly how.
- **Weaknesses:** **You should not be building this at all in the first 18 months.** This is the single most expensive, highest-reversal-cost decision in the document (Part V: *extremely expensive to change*). A correct distributed GPU scheduler with DRF + preemption + topology is a multi-year effort by a specialized team — and **NVIDIA just open-sourced exactly this as the KAI Scheduler (Apache-2.0, CNCF Sandbox), gang-scheduling/quotas/fairness included, plus the DRA driver donated to CNCF.** Spending your scarcest talent rebuilding the GPU vendor's free, community-maintained scheduler is the clearest "could-kill-the-company" misallocation in the plan.
- **Hidden assumptions:** That your scheduling needs at 10–100 customers (each with tens of nodes) require DRF/preemption/topology. They do not — first-fit over a small per-customer fleet is adequate for a year-plus.
- **Failure modes:** 6–12 engineer-months sunk into scheduler correctness while the console, billing, and onboarding starve; subtle preemption/fairness bugs erode trust; you ship later than a competitor who used KAI/DRA off the shelf.
- **Alternative approaches:** **For customers on Kubernetes:** KAI Scheduler + DRA, with your value-add as a thin admission/quota/chargeback layer and the UX. **For raw nodes:** the dumbest possible first-fit placer (filter feasible, pick least-loaded), no DRF/preemption/topology until a customer's fleet provably needs it. Keep the `PlacementBackend` interface (the doc's one great hedge here) so KAI/DRA/first-fit are swappable.
- **What AWS would do:** Build it bespoke (EC2 placement is proprietary) — with a 50-person team and a decade.
- **What a startup should do:** **Buy/adopt KAI + DRA. Build quota + chargeback + UX.** Your moat is not the scheduler; it's the experience and the metering.
- **Verdict:** **Reject as a build.** Best engineering in the doc, worst use of runway. Adopt KAI/DRA; differentiate above it.

## Review of §9 — Agent Design

**Scorecard —** Correct: yes · Premature: no (this you do build) · Complexity: high but core · Scales-100k: yes · Org-scale: medium · Lock-in: this *is* your lock-in on customers (intentional) · Ops-burden: very high (fleet upgrades) · 10-yr: yes.

- **Current recommendation (doc):** Single static Go binary; outbound-only; crash-only; enrollment via join token → SPIFFE SVID; bidi gRPC; ring-based signed upgrades; version-skew tolerance.
- **Strengths:** This is the *right* thing to build in-house — it's the integration point and the moat. Outbound-only + crash-only + reconcile-don't-restart are all correct. Ring upgrades + signed artifacts + skew tolerance show the author understands fleet ops.
- **Weaknesses:** The section under-weights the two things that actually determine success: (1) **the enterprise security review of a remote-command root daemon** (covered as a risk below — it's the company's biggest non-technical risk and barely appears here); (2) **the testing burden** — "test relentlessly" is not a plan for reconciling distributed state across an unreliable fleet, which is where the bugs and the 3am pages live.
- **Hidden assumptions:** That customers will accept a root agent that phones home and executes remote commands; that outbound 443 to your cloud is always permitted (air-gapped/proxy-locked environments break this).
- **Failure modes:** Security team blocks the install; a bad signed upgrade bricks a customer's nodes (even with rollback, the trust hit is severe); reconcile bugs leak GPUs or double-bill.
- **Alternative approaches:** Offer a **rootless / reduced-privilege mode** (delegate device ops to a tightly-scoped helper) and an **agent security whitepaper + SBOM + third-party pen-test** as sales artifacts from day one. Support a relay/proxy-friendly and (eventually) air-gapped mode.
- **What AWS would do:** AWS owns the hardware, so it ships firmware/hypervisor agents (Nitro) it fully controls — it never faces the "customer's security team vetoes our daemon" problem. You do. That asymmetry is under-acknowledged.
- **What a startup should do:** Build the agent (yes), but invest equally in the *trust package* (security docs, least-privilege mode) because that's the actual gating factor for the buyer.
- **Verdict:** Correct to build; budget as much for the security-trust story as for the code.

## Review of §10 — Database Design

**Scorecard —** Correct: Postgres yes, ClickHouse-now maybe-no · Premature: ClickHouse/Citus framing · Complexity: medium · Scales-100k: yes with sharding · Org-scale: yes · Lock-in: low (standard SQL) · Ops-burden: medium-high (multiple stores) · 10-yr: yes.

- **Current recommendation (doc):** Postgres system-of-record; ClickHouse for usage; separate ledger schema; partitioned hash-chained audit; Citus sharding at Stage C.
- **Strengths:** Postgres-as-source-of-truth is the correct boring choice. The schema is concrete, well-indexed (partial indexes on hot subsets), and org-scoped for future sharding — genuinely good. Separating metering from the relational primary is right *in principle*.
- **Weaknesses:** **ClickHouse on day one is another stateful system to operate** for usage volume you don't have at 10–100 customers — Postgres (partitioned `usage_events`) handles early metering fine. The hash-chained audit table is elegant but the chain becomes a single-writer serialization point at high write rates (a future bottleneck the doc doesn't flag). Citus/sharding detail invites premature adoption.
- **Hidden assumptions:** That metering volume justifies a columnar store early; that a per-row hash chain scales.
- **Failure modes:** Operating ClickHouse + Postgres + Redis + a broker with 6 engineers → on-call burnout; the audit hash-chain throttling writes under load.
- **Alternative approaches:** One Postgres (managed) for *everything* including usage (partitioned) and audit, until volume forces ClickHouse. Batch-hash audit (Merkle over periodic batches) instead of per-row chaining.
- **What AWS would do:** Purpose-built store per workload (Aurora + Redshift/Timestream + QLDB-style ledger) — managed, so no ops cost to them.
- **What a startup should do:** Managed Postgres, one system, until metrics force a split. Use a managed warehouse later, don't self-host ClickHouse early.
- **Verdict:** Schema: approved. Polyglot-now: defer. Collapse to one managed Postgres at seed stage.

## Review of §11 — Networking Design  ⚠️ (second highest-stakes)

**Scorecard —** Correct: model yes, build-it-yourself no · Premature: **building it, yes** · Complexity: **very high** · Scales-100k: yes if you survive building it · Org-scale: needs a networking team you don't have · Lock-in: low if abstracted · Ops-burden: **very high (you're on the data path)** · 10-yr: capability yes, bespoke build no.

- **Current recommendation (doc):** Build a WireGuard overlay + regional relay fleet + edge SSH gateway with short-lived certs + reverse proxy + optional mesh client; the `ssh user@instance` walkthrough.
- **Strengths:** The problem analysis is *the best in the document* — the recognition that there is "no routable instance IP" and that everything must ride the agent's outbound tunnel is exactly right, and the SSH walkthrough is correct. Short-lived SSH certs at the edge (vs static keys) is the correct security model.
- **Weaknesses:** **This is a second from-scratch company.** A production overlay with NAT traversal, DERP-style relays, anycast gateways, an SSH CA, and a mesh client *is Tailscale*. The doc says "inspired by Tailscale/Cloudflare" and then specs building it. Worse, **the relay sits on the data path for every byte of SSH/HTTP and large dataset transfers** — that is a direct COGS/egress hit and a margin problem the doc mentions only as "bandwidth cost," plus a platform-wide SPOF class (relay down = nobody reaches their instances).
- **Hidden assumptions:** That you must build connectivity rather than embed Tailscale/Headscale or Cloudflare Tunnel; that routing customer bulk data through your relays is economically viable.
- **Failure modes:** 9–12 months on networking; relay egress eats gross margin; a relay-region outage takes out access for all customers in it; mesh-client install friction.
- **Alternative approaches:** **Embed Tailscale/Headscale (or Cloudflare Tunnel) as the connectivity substrate in v1.** Your agent installs and manages it; you build the *control* (which user reaches which instance, short-lived authz) on top. This removes the single largest networking risk and the data-path egress problem (P2P where possible). Abstract it behind a `Connectivity` interface so you can in-source later if it becomes a moat.
- **What AWS would do:** Build it all (VPC, PrivateLink, their own SDN) — they own the network and the economics.
- **What a startup should do:** Buy/borrow the overlay. Build the authz/UX layer above it. In-source only if/when networking becomes a proven differentiator.
- **Verdict:** Brilliant analysis, **reject the build**. Stand on Tailscale/Cloudflare; own the control layer. This is a top-3 runway-saver.

## Review of §12 — Security Design

**Scorecard —** Correct: yes · Premature: Cedar/OPA + per-tenant-keys early · Complexity: medium-high · Scales-100k: yes · Org-scale: yes · Lock-in: low · Ops-burden: medium · 10-yr: yes.

- **Current recommendation (doc):** OIDC→SAML→SCIM; Cedar/OPA policy; defense-in-depth tenant isolation; Vault; SOC 2 first.
- **Strengths:** Defense-in-depth isolation table is correct, and "a shared-kernel container is not a tenant boundary" is the right hard rule. SOC-2-first sequencing is correct for enterprise B2B.
- **Weaknesses:** **Externalized policy (Cedar/OPA) is premature** at 10 customers — a hardcoded RBAC matrix is simpler and sufficient until customers demand custom policy. **Vault is another heavy system**; cloud KMS + SOPS covers early needs. Most importantly, the doc's Phase-1 trust model — *"the tenant boundary is the org, so containers are fine"* — is **doing enormous load-bearing work and is shakier than stated**: insider threat, compromised creds, and confidential intra-org projects (M&A, security teams) mean "employees trust each other" often fails *inside* a single org.
- **Hidden assumptions:** That intra-org = mutual trust; that you need a policy engine and Vault early.
- **Failure modes:** A cross-team data exposure inside a customer org (shared-kernel container, time-sliced GPU) becomes your first security incident and reference-customer killer.
- **Alternative approaches:** Hardcoded RBAC + cloud KMS now; Cedar/OPA + Vault when contracts require. Offer MIG/VM isolation as an option for sensitive intra-org projects earlier than Phase 2.
- **What AWS would do:** Cedar (they built it), full KMS/HSM, formal isolation proofs.
- **What a startup should do:** Simplest correct authz; buy secrets via KMS; start SOC 2 controls early (cheap if built-in).
- **Verdict:** Right destination; defer the policy engine and Vault; harden the intra-org trust assumption sooner.

## Review of §13 — Observability Design

**Scorecard —** Correct: yes · Premature: self-hosting LGTM · Complexity: high (many components) · Scales-100k: yes · Org-scale: medium · Lock-in: low (OTel) · Ops-burden: **very high if self-hosted** · 10-yr: yes.

- **Current recommendation (doc):** OTel standard; push across NAT; Prometheus/VictoriaMetrics + Loki + Tempo + Grafana + DCGM; Alertmanager/PagerDuty.
- **Strengths:** The push-not-pull-across-NAT insight is correct and easy to get wrong. OTel-as-standard is the right long-term bet. DCGM-mandatory is correct.
- **Weaknesses:** **Self-hosting the full LGTM stack + VictoriaMetrics is 5+ stateful systems on your on-call rotation** — for 6 engineers, that's a part-time SRE you don't have. Observability of *your* platform should be bought early.
- **Hidden assumptions:** That you'll self-host telemetry storage from day one.
- **Failure modes:** Your monitoring stack pages you more than your product does.
- **Alternative approaches:** Managed (Grafana Cloud / Datadog / Chronosphere) for your platform telemetry; keep OTel instrumentation so you can in-source later. Self-host only the *customer-facing* GPU dashboards if needed.
- **What AWS would do:** CloudWatch (their own managed stack).
- **What a startup should do:** Buy observability; instrument with OTel; revisit at scale.
- **Verdict:** Standard is right; ownership model is wrong early. Buy it.

## Review of §14 — Billing Design

**Scorecard —** Correct: yes · Premature: **double-entry ledger for no-money chargeback** · Complexity: medium-high · Scales-100k: yes · Org-scale: yes · Lock-in: low · Ops-burden: medium · 10-yr: yes.

- **Current recommendation (doc):** Append-only events → rating → double-entry ledger → invoice; build the ledger from day one even for internal chargeback; idempotent ingestion; reconciliation.
- **Strengths:** Idempotent metering and immutable events are correct and *do* matter early (double-billing kills trust). Billing-from-the-state-machine cross-checked with heartbeats is right.
- **Weaknesses:** **A full double-entry ledger when no real money moves is gold-plating for Phase 1.** The doc's defense ("expensive to retrofit") is partly true but overstated — *immutable, ledger-shaped usage events* are the thing that's expensive to retrofit; the double-entry *posting* layer can be added when real money appears (Phase 3) without redoing the events. The hardest real problem — **metering across disconnects ("who pays for a node that vanished mid-job?")** — is raised and left unresolved, and that ambiguity is exactly what produces invoice disputes.
- **Hidden assumptions:** That chargeback needs accounting-grade double-entry before there's revenue; that disconnect-billing policy can be deferred.
- **Failure modes:** Effort spent on ledger mechanics pre-revenue; later, a billing dispute over a disconnect with no defined policy.
- **Alternative approaches:** Immutable events + a simple rating/aggregation + chargeback reports now (keep schema ledger-ready). Add double-entry posting at first real payment. **Define the disconnect/grace policy explicitly now** — it's a product decision, not a schema.
- **What AWS would do:** Massive metering pipeline + a real ledger — they have billions in revenue to protect.
- **What a startup should do:** Immutable events now, double-entry when money is real, disconnect policy decided day one.
- **Verdict:** Keep immutable events; defer the double-entry layer; resolve the disconnect policy immediately.

## Review of §15 — Scalability Strategy

**Scorecard —** Correct: yes · Premature: describing D invites building toward it · Complexity: — (it's a plan) · Scales-100k: yes (that's the point) · Org-scale: yes · Lock-in: low · Ops-burden: — · 10-yr: yes.

- **Current recommendation (doc):** Stage A monolith → B service-split → C cells+sharding+regional schedulers → D global mesh; "what breaks" per stage.
- **Strengths:** This is the *correct* mental model and the "Stage A risk is over-engineering — ship the monolith" line is the best advice in the document. Naming what-breaks-first per stage is exactly right.
- **Weaknesses:** Stage C/D are described in enough loving detail (cells, Citus, regional schedulers, anycast mesh) that an ambitious team will *build toward them*. The doc's own discipline ("don't build D now") competes with its thoroughness.
- **Hidden assumptions:** That the team will resist the gravitational pull of the detailed future design (teams rarely do).
- **Failure modes:** Premature cells/sharding; "we'll need it at 100k GPUs" justifying complexity at 100 GPUs.
- **Alternative approaches:** Keep Stage A in the build plan; move Stage C/D to an appendix explicitly labeled "do not build until the named metric trips."
- **What AWS would do:** Cells from early on (they operate at cell scale natively).
- **What a startup should do:** Monolith; instrument the trip-wires (Postgres write IOPS, scheduler decision latency); evolve reactively.
- **Verdict:** Best-reasoned section; quarantine the future stages so they don't leak into the roadmap.

## Review of §16 — Future Marketplace Evolution

**Scorecard —** Correct: yes (esp. the Phase-5 pushback) · Premature: — · Complexity: — · Scales: — · Lock-in: — · Ops: — · 10-yr: yes.

- **Current recommendation (doc):** Phases 1→4 green-lit; Phase 5 (decentralized consumer compute) challenged as a graveyard; build substrate that *could* go decentralized without depending on it.
- **Strengths:** The Phase-5 takedown is correct, specific, and commercially honest (ECC, interconnect, data governance, verification economics). This is exactly the co-founder pushback that should be in a design doc.
- **Weaknesses:** It under-states the **chicken-and-egg of the Phase-3 marketplace** (you need supply to get demand and vice-versa) and the **buyer-discontinuity** between Phase 1 (IT/platform team) and Phase 3 (developers + providers) — acknowledged but not costed. It also doesn't confront that **NVIDIA (Run:ai) and the hyperscalers are moving into the same space**, which compresses the window.
- **Hidden assumptions:** That Phase-1 customers and supply convert into a marketplace; that there's a durable window before NVIDIA/clouds close it.
- **Failure modes:** A marketplace with supply and no demand (or vice-versa); being out-distributed by NVIDIA's bundled stack.
- **Alternative approaches:** Treat Phase 3 as a *separate company thesis* requiring its own validation, not an automatic graduation.
- **What AWS would do:** Not relevant (AWS is the incumbent being routed around).
- **What a startup should do:** Nail Phase 1; validate the marketplace buyer independently before investing in marketplace primitives.
- **Verdict:** Keep the Phase-5 honesty; add a chicken-and-egg and competitive-window analysis.

## Review of §17 — Risk Analysis

**Scorecard —** Correct: as far as it goes · Premature: — · Complexity: — · Coverage: incomplete · 10-yr: —.

- **Current recommendation (doc):** A solid risk table across technical/security/business/operational.
- **Strengths:** Good coverage of *technical* risk; the isolation-escape and split-brain risks are correctly rated Critical.
- **Weaknesses:** **Misses or under-rates the risks that actually decide the outcome:** (1) enterprise security vetoing the root agent — *the* go-to-market risk, absent; (2) Slurm/Run:ai/KAI incumbents — absent; (3) SaaS-vs-air-gapped control-plane bifurcation — absent; (4) relay-egress margin erosion — under-rated; (5) NVIDIA-as-competitor (Run:ai) — under-rated; (6) no COGS/unit-economics in a *funding* doc — absent. A risk register that omits the company-killers is the most dangerous kind.
- **Hidden assumptions:** That the listed risks are the top risks (they're the *technical* ones).
- **Failure modes:** Team mitigates the listed (technical) risks and gets killed by an unlisted (go-to-market) one.
- **Alternative approaches:** Re-rank with go-to-market and economic risks first (see Part II).
- **What AWS would do:** Risk-review across the same axes but with a dedicated security/GTM function.
- **What a startup should do:** Put the agent-acceptance risk and the incumbent risk at the *top* of the register.
- **Verdict:** Necessary but incomplete; Part II below supersedes it.

## Review of §18 — MVP Roadmap

**Scorecard —** Correct: directionally · Premature: M0 still too big · Complexity: M0 too high · Scales: — · 10-yr: —.

- **Current recommendation (doc):** M0 = agent + monolith + simple scheduler + container launch + relay SSH + Jupyter + metering + ledger + Next.js; then fractional GPU, isolation, quotas, marketplace groundwork.
- **Strengths:** Correct instinct to ship a thin vertical slice to one design partner; correct to flag the get-right-early pieces.
- **Weaknesses:** **M0 still includes a custom scheduler, a custom relay/overlay, and a double-entry ledger** — three things this review says to buy/defer. M0 is a 9–12 month build as written, not an MVP. A true MVP is 8–12 weeks.
- **Hidden assumptions:** That "simple scheduler" and "relay SSH gateway" are small (they're not, as specified).
- **Failure modes:** "MVP" slips two quarters; design partner loses interest.
- **Alternative approaches:** M0 = agent + monolith + **Tailscale connectivity** + **first-fit placement (or KAI for K8s customers)** + container launch + Jupyter/SSH + **immutable usage events + chargeback report** + minimal console. Cut Kata/VM, fractional GPU, ledger-posting, marketplace.
- **What AWS would do:** Internal MVP, dogfood, then GA — they don't have runway pressure.
- **What a startup should do:** 8–12 week MVP on bought primitives; one delighted design partner; iterate.
- **Verdict:** Right shape, still over-scoped. Cut to bought primitives (Part VI).

## Review of §19 — Team Structure

**Scorecard —** Correct: at scale yes · Premature: 7 pods for 6 eng · Complexity: — · Org-scale: **the math doesn't close** · 10-yr: yes.

- **Current recommendation (doc):** 5 founding vertical-slice owners; scale to ~7 pods (Control Plane, Data Plane/Agent, Networking, Frontend, SRE, Security, Billing).
- **Strengths:** Correctly identifies the rarest hire (GPU-virtualization + Linux + networking) and the buy-don't-build list (identity, payments, managed infra).
- **Weaknesses:** **The org implied by the architecture cannot be staffed by the team.** Seven pods need ~25–40 engineers; the architecture (custom scheduler + custom overlay + agent + 13 systems) *requires* roughly that many to operate, but the company has six. The architecture and the org are inconsistent — and the resolution is to shrink the architecture (Part VI), not to plan the big org.
- **Hidden assumptions:** That you'll have the people the architecture needs.
- **Failure modes:** Six engineers spread across work that needs thirty; everything is 30%-done.
- **Alternative approaches:** Design the architecture *down* to what six can build and operate (buy scheduler/overlay/observability/auth). Then the team math closes.
- **What AWS would do:** Staff every pod fully.
- **What a startup should do:** One team, bought primitives, ruthless focus on the agent + console + chargeback.
- **Verdict:** Conway's-law alarm: the architecture is sized for an org you won't have for years. Shrink the architecture.

## Review of §20 — Final Recommended Architecture

**Scorecard —** Correct: as a destination · Premature: as a starting point · Complexity: very high · Scales-100k: yes · Org-scale: not at seed · Lock-in: low · Ops-burden: very high · 10-yr: yes.

- **Current recommendation (doc):** The one-paragraph synthesis + decision ledger + "five things to get right."
- **Strengths:** The "five things" (outbound agent, state-machine+scheduler correctness, org-scoped tenancy, immutable metering, push observability) are *mostly* the right five — four of them are correct and cheap-ish.
- **Weaknesses:** Two of the "five things" (build-the-scheduler, and the implied build-the-overlay) are exactly what this panel says *not* to build. The decision ledger codifies the build-everything posture that endangers the runway.
- **Hidden assumptions:** That "get these five right" = "build these five." Three should be *bought*.
- **Failure modes:** The summary becomes the build charter; see §1.
- **Alternative approaches:** Replace "build scheduler" with "adopt KAI/DRA + build quota/UX"; replace "build overlay" with "embed Tailscale + build authz."
- **What AWS would do:** This, bespoke.
- **What a startup should do:** Part VI.
- **Verdict:** Keep four of the five truths; swap two builds for buys; that's the whole review in miniature.

# Part II — Top 20 Architectural Risks (ranked)

Ranked by a blend of **severity × likelihood ÷ time-to-failure**. 💀 = could kill the company if wrong. "TTF" = time-to-failure (when it bites if unaddressed). "Solve" = now / soon / later.

| # | Risk | Severity | Likelihood | TTF | Solve |
|---|---|---|---|---|---|
| 1 💀 | Building scheduler + overlay + agent simultaneously | Critical | High | 12–18 mo (runway) | **Now** |
| 2 💀 | Enterprise security vetoes the root, phone-home agent | Critical | High | 1st enterprise sale | **Now** |
| 3 💀 | Custom scheduler vs. free KAI/DRA (misallocated talent) | Critical | High | 6–12 mo | **Now** |
| 4 💀 | Ignoring Slurm / Run:ai incumbents → no real wedge | Critical | Med-High | 6–18 mo | **Now** |
| 5 💀 | Over-engineering pre-PMF; team math doesn't close | Critical | High | Ongoing | **Now** |
| 6 | Custom overlay vs. Tailscale + relay-egress COGS | High | High | 6–12 mo | **Now** |
| 7 💀 | SaaS-only control plane vs. air-gapped/on-prem demand | High | Med-High | 1st regulated deal | **Now** |
| 8 | 13-system operational surface → 6-eng on-call burnout | High | High | 3–9 mo | **Now** |
| 9 | Multi-tenant isolation escape (incl. *intra-org*) | Critical | Low-Med | When sharing starts | Now (policy) / Later (full) |
| 10 | Distributed-state reconcile bugs → leaked GPU / double-bill | High | Med-High | Ongoing post-launch | **Now** |
| 11 | NVIDIA-only lock-in + NVIDIA (Run:ai) as competitor | High | Medium | Multi-year | Now (interface) / Later (impl) |
| 12 | Bad signed fleet-upgrade bricks customer nodes | High | Medium | Post-launch | Soon |
| 13 | Relay SPOF (region relay down = no access for its tenants) | High | Medium | First relay outage | Soon |
| 14 | Metering disconnect → billing disputes (undefined policy) | Med-High | Medium | First dispute | Now (policy) |
| 15 | Control-plane SPOF (single region, Stages A–B) | Med-High | Medium | First CP outage | Soon |
| 16 | SAML/SCIM deferral blocks the targeted enterprise deals | Med-High | Med-High | 1st enterprise sale | Now (buy it) |
| 17 | Premature microservices → distributed monolith | High | Medium | 6–12 mo | **Now** (avoid) |
| 18 | NATS→Kafka self-inflicted migration | Medium | Medium | Stage B | Now (avoid) |
| 19 | Phase-3 marketplace chicken-and-egg (supply↔demand) | High | Medium | Phase 3 | Later |
| 20 | No COGS / unit-economics model in a funding doc | Med-High | Medium | At scale / pricing | **Now** (model it) |

**Detail for the company-killers (💀):**

1. **Three hard-tech builds at once.** *Consequence:* none reaches production quality; runway ends pre-PMF. *Mitigation:* build only the agent; buy the overlay (Tailscale/Cloudflare); adopt KAI/DRA or first-fit for scheduling. *Solve now* — it's the whole re-scope.
2. **Root agent rejected by security.** *Consequence:* the install — the foundation of everything — is blocked at the buyer's security gate; deals die in procurement. *Mitigation:* least-privilege/rootless mode, security whitepaper + SBOM + third-party pen-test as sales artifacts, air-gapped story. *Solve now* — validate with a real enterprise security team before building.
3. **Rebuilding KAI.** *Consequence:* 6–12 engineer-months spent on what NVIDIA gives away (Apache-2.0, CNCF), shipping later than competitors who adopt it. *Mitigation:* adopt KAI+DRA for K8s customers, first-fit for raw nodes, keep `PlacementBackend` seam. *Solve now.*
4. **Incumbent blindness.** *Consequence:* you build a scheduler/console for buyers who already run Slurm/Run:ai and won't switch; no wedge. *Mitigation:* position as the self-service+chargeback *layer over* Slurm/K8s/raw nodes; integrate, don't replace. *Solve now* — it's a positioning decision.
5. **Over-engineering vs. team size.** *Consequence:* six engineers spread across a thirty-engineer architecture; everything 30%-done at month 18. *Mitigation:* shrink the architecture to the buy-heavy Part VI design. *Solve now.*
7. **SaaS-only assumption.** *Consequence:* research labs, universities, defense, and data-sensitive enterprises (your stated targets) require on-prem/air-gapped; a SaaS-only control plane locks you out of them and a late pivot is a re-architecture. *Mitigation:* decide the deployment model *now*; if on-prem is in-scope, design the control plane to be deployable into the customer's environment (which also reshapes observability/upgrades). *Solve now.*
9. **Isolation escape.** *Consequence:* first security incident is a cross-tenant (or cross-team) data exposure → reference-customer and reputation killer. *Mitigation:* MIG/VM for any cross-trust sharing; offer isolated mode for sensitive intra-org projects; never time-slice across trust. *Policy now, full isolation when sharing begins.*

# Part III — Over-Engineering Audit

What is built ahead of need, by customer count. "Cut" = remove from the build entirely until the trigger; "Simplify" = keep a cheaper version.

### Over-engineered for **10 customers**

| Component | Verdict | Simplification |
|---|---|---|
| Custom GPU scheduler (DRF, preemption, topology, reservations) | **Cut** | First-fit placement; or KAI/DRA off-the-shelf |
| Custom WireGuard overlay + relay fleet + mesh client | **Cut** | Embed Tailscale/Headscale or Cloudflare Tunnel |
| Tiered isolation (Kata + KVM/VFIO) | **Cut** | Containers only (containerd + NVIDIA toolkit) |
| Cells, Citus sharding, regional schedulers | **Cut** | Single managed Postgres, single region |
| ClickHouse + Kafka | **Cut** | Postgres (partitioned usage_events); no broker or Postgres queue |
| Double-entry ledger | **Simplify** | Immutable usage events + rating job; add posting at first $ |
| Cedar/OPA policy engine + Vault | **Cut** | Hardcoded RBAC + cloud KMS/SOPS |
| Self-hosted LGTM + VictoriaMetrics | **Cut** | Managed observability (Grafana Cloud/Datadog) + OTel |
| Rust data-path | **Cut** | Go everywhere |
| Microservices (10 services) | **Cut** | One modular monolith |
| SAML/SCIM build | **Simplify** | Buy via WorkOS/Auth0 (config, not build) |
| Fractional GPU (MIG/MPS/time-slice) | **Simplify** | Whole-GPU only until a customer asks |

### Over-engineered for **100 customers**

Still cut/defer: cells, Citus, regional schedulers, Kafka, custom overlay, Kata/KVM (unless a multi-org/sensitive deal landed), self-hosted observability, Rust. **Now genuinely needed:** RBAC + quotas, basic chargeback reports, MIG (if datacenter cards + sharing demand), maybe a second region for latency, read replicas. The scheduler can still be first-fit/KAI.

### Over-engineered for **1,000 customers**

Defer still: full cell architecture, Citus sharding (likely — measure first), the decentralized Phase-5 substrate, anycast relay mesh. **Now needed:** split out scheduler + metering as services, ClickHouse/managed warehouse for metering, real multi-region, the double-entry ledger (real money flowing), Vault/Cedar if enterprise contracts demand, fleet-upgrade tooling matured. Even here, a *single well-tuned Postgres primary with replicas* often suffices — don't shard on a hunch.

**Bottom line:** roughly **70% of the document's components are over-engineered for the first 100 customers.** The design is correct for the company you hope to be in year 7 and wrong for the company you are in month 1.

# Part IV — Under-Engineering Audit

Where the design (or its *simplified* startup form) will break *earlier* than expected, and why.

### Will strain before **1,000 GPUs**

- **Postgres connection exhaustion.** Thousands of agents heartbeating + API pods will blow past Postgres connection limits fast. *Why:* naive connection-per-agent or chatty heartbeats. *Fix early:* PgBouncer, coarse heartbeats, push deltas not full state.
- **Control-plane SPOF (single region).** A control-plane outage makes the *entire* fleet unmanageable; if running instances can't even be observed, that's a trust event. *Why:* Stage A/B is single-region by design. *Fix early:* multi-AZ at least; ensure running instances survive CP downtime and reconcile on recovery.
- **Reconciliation correctness.** Leaked GPUs / double-bills appear with the *first* unreliable customer node, not at scale. *Why:* distributed state is hard from node #1. *Fix early:* invariant tests, idempotency, the state machine — these earn their keep immediately.
- **The relay as data-path bottleneck/SPOF.** Even one busy customer doing dataset transfers through your relay exposes bandwidth/SPOF issues well before 1,000 GPUs. *Fix early:* P2P-first (Tailscale), relay only as fallback.

### Will strain before **10,000 GPUs**

- **Single global scheduler throughput.** Per-second churn across thousands of nodes + a pending queue stresses one scheduler. *Why:* single leader. *Fix:* this is exactly why adopting KAI/DRA (battle-tested) beats a homegrown scheduler that's never seen this load.
- **Postgres write contention on hot tables** (`instances`, usage, audit hash-chain). *Why:* single primary, serialized audit chain. *Fix:* move usage to a columnar/managed store; batch-hash the audit; read replicas.
- **Message bus throughput for metering.** A Postgres queue or single NATS stream will choke on the usage firehose. *Fix:* dedicated streaming (Redpanda/Kafka) for metering only.
- **Fleet-upgrade blast radius.** A bad upgrade across thousands of nodes you don't control is a mass-incident. *Why:* rings help but aren't enough without strong canary signals. *Fix:* hard automated rollback gates, per-customer opt-in rings.

### Will strain before **100,000 GPUs**

- **Single Postgres primary** — unavoidable shard point. *Fix:* org-sharding (Citus/Vitess) — but only here, not earlier.
- **Single-region everything** — latency and blast radius. *Fix:* cells + multi-region active-active; this is where the doc's Stage-C design finally earns itself.
- **Audit hash-chain** as a global serialization point. *Fix:* Merkle-batched audit, per-cell chains.
- **Ledger at payout scale** — double-entry posting volume + reconciliation across providers. *Fix:* partitioned ledger, async posting, dedicated reconciliation.
- **Global identity/quota consistency across cells.** *Fix:* small strongly-consistent global core; everything else regional/eventual.
- **Relay → global anycast mesh.** Coordinating thousands of relays worldwide is its own system. *Fix:* this is the strongest argument for *never* having built the overlay yourself — at this scale you want Tailscale/Cloudflare's decade of investment, not your 6-person team's.

**Bottom line:** the things that break *early* (Postgres connections, control-plane SPOF, reconciliation, relay-as-data-path) are mostly *operational* and *correctness* issues the document treats as Stage-C concerns. The things the document obsesses over (cells, DRF, sharding) don't bite until 10k–100k GPUs. **The doc is under-engineered exactly where it's under-built (ops/correctness/availability) and over-engineered exactly where it's over-built (scale-out machinery).**

# Part V — Decision Reversal Cost Matrix

The right way to read this matrix: **reversal cost should drive build-vs-buy.** Build the expensive-to-reverse things that *differentiate* you (the agent, the tenancy schema, immutable usage capture). Buy or adopt the expensive-to-reverse things that *don't* (the scheduler, the overlay). Defer the cheap-to-reverse things (marketplace, isolation tiers, the ledger). The document gets this backwards in two places — it builds two of the most expensive-to-reverse, least-differentiating components from scratch.

| Decision | Doc's choice | Reversal cost | Why that cost | Implication |
|---|---|---|---|---|
| **Language** | Go (CP+agent), Rust data path | **Easy–Moderate** | Language is invisible behind APIs/protocol; a control-plane rewrite is bounded and internal. The *Rust-now* carve-out is the only risky part — it splits a 6-person team's expertise. | Go everywhere until a profiler forces Rust. "Go→add Rust later" is additive (Easy); "Rust now→consolidate" is waste. |
| **Scheduler** | Custom two-level optimistic scheduler | **Extremely expensive** | Once quotas, fairness, gang-scheduling and reservation semantics are encoded, customers depend on *observable scheduling behavior* — including bug-compatible quirks. Switching schedulers changes who-runs-when. | This is the #1 reason to **adopt KAI/DRA now.** Don't hand-build the component you can least afford to reverse when a free, battle-tested one exists. |
| **Agent** | Outbound-only root daemon on customer HW | **Extremely expensive** (highest in the doc) | Installed on hardware you don't own, behind a security review you had to win. Changing the connection model re-triggers every customer's security review and a fleet redeploy you can't force. Wire protocol must be versioned from commit #1. | **Build it — deliberately.** It's both core differentiation *and* correctly chosen. The expensive decision hiding inside it (SaaS vs on-prem control plane) must be made explicitly, not by default. |
| **Database** | Postgres SoR + ClickHouse + NATS→Kafka | **Easy–Moderate** (engine) / **Extremely expensive** (schema) | The *engine* sits behind a repository layer and can be sharded (Citus) or swapped with effort. The *multi-tenant schema* (tenant-id on every row, idempotency keys, immutable event tables) is near-impossible to retrofit. | Get the tenancy schema right on day one; treat engine choice as reversible. **Cancel the NATS→Kafka migration** — pick one bus, it's a self-inflicted Moderate reversal. |
| **Storage** | Local NVMe + MinIO/S3-compatible | **Easy** | The S3 API is a stable abstraction; swapping MinIO→S3/Ceph behind it is bounded. The only sticky part is the durability *promise* you make — a contract, not an engine. | Use real S3/GCS behind the S3 API; never over-promise durability on ephemeral local disk. |
| **Networking** | Custom WireGuard overlay + relay fleet | **Extremely expensive** *if built* | Addressing, identity and NAT-traversal behavior bake into every agent and every customer firewall rule. **This is the worst pairing in the matrix: maximum reversal cost on a non-differentiating component the doc chooses to build.** | **Buy it** (Tailscale/Headscale/Cloudflare Tunnel). Buying drops reversal cost to Moderate (re-abstractable SDK integration) *and* deletes the build. |
| **Isolation** | Tiered: containerd → Kata → KVM/VFIO | **Moderate, additive** | Stronger tiers stack *above* a working container baseline; you add them, you don't migrate off. | **Containers-only now**; cross-tenant on shared GPUs = whole-GPU or MIG. The tiered model is the right destination with cheap deferral. |
| **Billing** | Double-entry ledger + immutable events + ClickHouse | **Split: events = Extremely expensive, ledger = Moderate** | Usage you don't capture cleanly from instance #1 *cannot be reconstructed* — lost metering is lost, unauditable money. The double-entry ledger on top is a transformation you can build when real money flows. | **Capture immutable, idempotent usage events now** (cheap to do, catastrophic to skip). **Defer the ledger** until payouts exist. |
| **Marketplace** | Provider marketplace (Phase 3) | **Easy to defer** | The marketplace is additive on the multi-org foundation. The one expensive seed is the Phase-1 tenancy/billing-isolation model it inherits. | Defer the marketplace entirely; just don't poison the tenancy model now. |

**The synthesis the document is missing:** of its nine decisions, the two it should *most* avoid hand-building (scheduler, overlay) are exactly the two it commits to building from scratch — and both are available off-the-shelf. Meanwhile the genuinely irreversible thing it gets right (the agent) hides an unmade decision (SaaS vs on-prem) that is itself irreversible. Fix those three and the rest of the matrix is forgiving.

---

# Part VI — The Architecture We'd Actually Build

> Constraints: 2 founders, 6 engineers, 18 months of runway. Brutally honest. The goal is a *delighted paying customer in ~6 months*, not a platform in 36.

**Thesis (one line):** Build the self-service access + metering + chargeback layer over GPUs customers already own, with an install experience measured in minutes — and buy or defer everything that merely *looks* like infrastructure.

**The wedge you sell:** *"Install in an afternoon. Tomorrow your team launches GPU instances from a console — SSH/Jupyter/VSCode, idle auto-stop, per-team chargeback — on the hardware you already own."* Note what this is **not**: it is not "we built a better scheduler." Nobody buys a scheduler; they buy time-to-first-instance and a chargeback report their finance team accepts.

## Build (the only defensible core)

1. **The agent** — Go, outbound-only, single static binary. Versioned wire protocol from commit #1, reconcile-on-reconnect, self-update with rollback rings. This is the moat and the hardest thing to reverse, so it gets your best engineers.
2. **Control plane / API** — one Go *modular monolith*. Org/project/user model, instance lifecycle state machine, admission + quota, template catalog. Modular so it can be split later; monolith so it can be operated by humans now.
3. **Launch UX** — Next.js console. The product metric is *time-to-first-instance*. Idle auto-stop and templates are first-class because they are the highest-ROI features in the original doc and that judgment was correct.
4. **Metering + chargeback** — immutable usage events captured by the agent, simple rollups, per-team cost reports. This is the wedge's payoff and what makes the product sticky *inside* an organization (finance starts depending on it).

## Buy / adopt (everything else)

- **Connectivity/overlay:** Tailscale/Headscale or Cloudflare Tunnel. Do not write WireGuard plumbing or run a relay fleet.
- **SSO / SAML / SCIM:** WorkOS or Auth0. Enterprise deals require it; hand-rolling SAML is a multi-quarter tar pit with zero differentiation.
- **Scheduling:** customers on Kubernetes → **KAI Scheduler / DRA**. Raw nodes → **first-fit / bin-pack placement inside your control plane** (this is *allocation*, ~300 lines, not a general scheduler). Do not build the two-level optimistic scheduler.
- **Observability:** Grafana Cloud or Datadog + OpenTelemetry (push, NAT-friendly) + **DCGM** for GPU health/Xid/ECC. Don't operate your own Prometheus/Loki/Tempo fleet yet.
- **Secrets:** cloud KMS + SOPS. Defer Vault.
- **Object storage:** real S3/GCS behind the S3 API. MinIO only where on-prem demands it.

## Isolation

Containers only — containerd + NVIDIA Container Toolkit (CDI). Cross-tenant sharing of a physical GPU = **whole-GPU or MIG**, never time-slicing/MPS across trust boundaries. No Kata, no KVM/VFIO, no Firecracker (which still has no GPU passthrough). Keep the tiered model in the roadmap as the answer to the first regulated customer who asks.

## Data

**One managed Postgres** (multi-tenant schema, tenant-id on every row, idempotency keys, an append-only `usage_events` table) + **Redis** for ephemeral state/queues. No ClickHouse, no Kafka, no Citus, no double-entry ledger yet. The schema is the expensive part — get it right; the rest is reversible.

## The one decision you cannot defer: SaaS vs. customer-hosted / air-gapped

The document silently assumes a phone-home SaaS control plane. Your highest-value buyers — defense, finance, sovereign, frontier-AI labs — may *forbid* an outbound third-party daemon. This is the single most expensive thing to reverse (it's baked into the agent's trust model), so **decide your beachhead segment now and let it dictate topology.** If the beachhead is air-gap-friendly, the control plane must be deployable inside the customer's network from day one. Pick the segment, then pick the topology. Do not try to build both.

## Team allocation (2 founders + 6 engineers, 4 surfaces)

- **2 eng — agent + data path** (the moat).
- **2 eng — control plane / API + scheduler integration** (KAI/DRA adapter + first-fit).
- **1 eng — frontend / launch UX.**
- **1 eng — connectivity integration (Tailscale/Cloudflare) + SRE/on-call + fleet upgrade.**
- **Founder A — product + GTM + design-partner relationships.**
- **Founder B — security/compliance posture (the agent's security-review packet, SOC 2 path) + architecture.**

This is **4 surfaces for 6 engineers**, not the document's 7 pods for 6 engineers. The team math now closes — because most of the doc's surfaces became *buy*.

## The 18-month arc

- **M0–3:** agent + control-plane skeleton + Tailscale integration. Launch one instance on a design partner's single node, by hand-to-console. Capture usage events from day one.
- **M3–6:** launch UX, templates, idle auto-stop, quota/admission. 3–5 design partners on *real owned hardware*. First chargeback report. ← *This is the "delighted customer" gate; if you miss it, re-scope, don't push on.*
- **M6–12:** multi-node first-fit placement, KAI/DRA adapter for K8s customers, SSO/SCIM via WorkOS, fleet self-update with rollback rings, SOC 2 Type I underway. First paying customers.
- **M12–18:** multi-tenant hardening, on-prem/air-gap deployment mode *if* the beachhead demands it, basic HA (multi-AZ control plane), chargeback maturity. **Series-A story:** "N orgs, M GPUs under management, $X of chargeback tracked, install-to-first-instance under one day."

## Brutally honest — what still kills the *leaner* plan

1. **The install/security-review wall doesn't go away.** A leaner architecture doesn't make a root daemon more welcome. Mitigate aggressively: least-privilege, auditable, *consider open-sourcing the agent*, air-gap-capable. This is still risk #1.
2. **The incumbents are free and entrenched.** Slurm owns research queues; Run:ai is now NVIDIA's and effectively free; cloud + RunPod/Vast own "rent GPUs." Your wedge is the narrow seam of *owned-hardware self-service + chargeback*. **Validate that seam with design partners before building much** — if it's too narrow, there is no company.
3. **NVIDIA is both dependency and potential competitor** (it owns Run:ai and gives away KAI/DRA). Never build what NVIDIA gives away; differentiate on the multi-tenant access/UX/chargeback layer it doesn't care about.
4. **"Owned-GPU" TAM is unproven.** The bet is that the 2023–2025 buying spree left many orgs with GPUs they can't share well. Plausible, not proven. Your first job is proving it, not building Stage C.
5. **Build even half the original document and you die before PMF.** That is the whole point of this rewrite. The original is the correct architecture for the company you hope to be in year 7 and a fatal build charter for the company you are in month 1.

**Final line:** *Fund the wedge, not the platform.* The platform in the original document is what you earn the right to build — after the wedge works.

---

# Appendix — Scorecard Against the Stated Success Criteria

| Criterion | Target | Delivered | Where |
|---|---|---|---|
| Major architectural critiques | ≥ 20 | **24** | Part I (per-section) + Part II (ranked) |
| Concrete simplifications | ≥ 10 | **13** | Part III + Part VI |
| Hidden assumptions surfaced | ≥ 10 | **12** | Part I "Hidden assumptions" fields + below |
| Ideal-vs-startup distinction | required | **explicit** | "What AWS would do" vs "What a startup should do" in every §; Parts III & VI |
| Company-killer flags | required | **7 flagged 💀** | Part II |

**The 24 major critiques (condensed):** (1) builds three hard-tech products at once; (2) custom scheduler duplicates free KAI/DRA; (3) custom overlay duplicates Tailscale/Cloudflare; (4) never names incumbents Slurm/Run:ai; (5) root outbound agent vs enterprise security review — unaddressed; (6) SaaS-vs-air-gapped bifurcation unaddressed; (7) double-entry ledger premature; (8) ClickHouse premature; (9) NATS→Kafka self-inflicted migration; (10) Rust carve-out splits a 6-person team; (11) Kata/KVM/Firecracker tiers premature; (12) cells/sharding premature; (13) ~13-system ops surface for 6 engineers; (14) team math doesn't close (7 pods/6 eng); (15) no COGS/unit economics in a funding doc; (16) relay egress as data path = COGS + SPOF; (17) Cedar/OPA + Vault premature; (18) product ignores incumbent context → shelfware risk; (19) conflates 10-yr north-star with 18-mo build plan; (20) NVIDIA-as-competitor dependency unflagged; (21) HA/multi-region under-built early while scale-out machinery over-built; (22) reconciliation correctness treated as Stage-C but bites at node #1; (23) Postgres connection exhaustion under agent fleet unaddressed; (24) "owned-GPU" TAM unvalidated.

**The 13 simplifications:** one Go modular monolith (not cells/microservices); buy the overlay; adopt KAI/DRA + first-fit (not custom scheduler); containers-only (defer Kata/KVM); one managed Postgres (defer ClickHouse); one message bus (cancel NATS→Kafka); buy SSO/SAML/SCIM; buy observability; cloud KMS/SOPS (defer Vault); capture events, defer the ledger; Go-only (defer Rust); defer marketplace/multi-region/sharding; pick one persona, not three.

**The 12 hidden assumptions:** (1) you must *build*, not assemble, overlay/scheduler/agent; (2) the control plane is SaaS (customers phone home); (3) NVIDIA-only forever; (4) enterprises will install a root outbound daemon; (5) buyers have "no modern cloud experience" (ignores existing Slurm/K8s); (6) relay bandwidth/egress is cheap and non-load-bearing; (7) a 6-person team can operate ~13 systems; (8) "owned-GPU self-service" is a large, reachable market; (9) the scheduler's semantics won't need to match an incumbent's; (10) single-region is fine until Stage C; (11) NVIDIA won't compete (it owns Run:ai); (12) usage can be reconstructed later if not captured cleanly now.

**The 7 company-killers (💀, full detail in Part II):** build-three-things-at-once; root-agent security veto; rebuilding what KAI/DRA give free; incumbent blindness (Slurm/Run:ai); over-engineering to runway death; unmade SaaS-vs-air-gap decision; cross-tenant isolation escape on shared GPUs.

---

# Sources

Fast-moving infrastructure claims in this review were re-verified against primary sources (June 2026):

- Firecracker has no GPU passthrough and its PCIe work is paused for lack of resources — [GPU (and PCIe) Support in Firecracker · Discussion #4845](https://github.com/firecracker-microvm/firecracker/discussions/4845); [The State of MicroVM Isolation in 2026](https://emirb.github.io/blog/microvm-2026/)
- Kubernetes DRA graduated to GA in v1.34 (Sept 2025) — [Kubernetes v1.34: DRA has graduated to GA](https://kubernetes.io/blog/2025/09/01/kubernetes-v1-34-dra-updates/); reinforced by [DRA GA in Red Hat OpenShift 4.21 (Mar 2026)](https://developers.redhat.com/articles/2026/03/25/dynamic-resource-allocation-goes-ga-red-hat-openshift-421-smarter-gpu)
- NVIDIA open-sourced Run:ai's scheduler as KAI Scheduler (Apache-2.0, Kubernetes-native, gang-scheduling/quotas/fairness via podgrouper) and it is now a CNCF Sandbox project; NVIDIA also donated the DRA driver to the Kubernetes community — [NVIDIA Open Sources Run:ai Scheduler](https://developer.nvidia.com/blog/nvidia-open-sources-runai-scheduler-to-foster-community-collaboration/); [NVIDIA/KAI-Scheduler (GitHub)](https://github.com/NVIDIA/KAI-Scheduler); [NVIDIA Donates DRA Driver for GPUs to Kubernetes](https://blogs.nvidia.com/blog/nvidia-at-kubecon-2026/)
- GPU sharing isolation hierarchy — MIG = hardware isolation (dedicated SMs/memory/cache, up to 7 instances), MPS = software compute/memory fractions (weaker isolation, fault can cross clients), time-slicing = no memory/fault isolation — [GPU Sharing in Kubernetes: MIG vs MPS vs Time-Slicing (ScaleOps)](https://scaleops.com/blog/kubernetes-gpu-sharing/); [MIG vs Time-Slicing (OpenMetal)](https://openmetal.io/resources/blog/mig-vs-time-slicing-gpu-sharing/)
- Managed-GPU-rental incumbents for context — [RunPod vs Vast.ai comparison](https://www.zenml.io/blog/kai-scheduler-vs-runai)



