"use client";
// Trust band — B2B credibility pillars. Every claim here maps to a shipped
// capability (mTLS transport, hashed credentials, RBAC, audit logs, health
// checks/observability, budgets/credits/chargeback) — no invented compliance
// badges. A customer-logo row intentionally does NOT exist yet:
// TODO(logos): add a logo strip here once real customers approve usage.
//
// Form: one glass ledger panel divided by hairlines (a spec sheet, not four
// floating cards) — indexed entries, top light edge, a faint brand wash that
// follows the panel's top-left corner.
import { Activity, HardDrive, ShieldCheck, Wallet } from "lucide-react";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

const PILLARS = [
  {
    icon: ShieldCheck,
    title: "Security in depth",
    desc: "mTLS on every connection, hashed credentials, role-based access, and a full audit trail of who did what, when.",
  },
  {
    icon: Activity,
    title: "Reliable by design",
    desc: "Health checks, metrics, and observability are built into the control plane — not bolted on after an incident.",
  },
  {
    icon: HardDrive,
    title: "Your hardware, your data",
    desc: "Workloads run on machines you own, inside your network. Code, models, and data never leave your perimeter.",
  },
  {
    icon: Wallet,
    title: "Spend under control",
    desc: "Budgets, credits, and per-department chargeback show exactly where every GPU hour goes.",
  },
];

export function TrustBand() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    reveal(gsap, root.querySelectorAll("[data-reveal]"), { trigger: root });
    reveal(gsap, root.querySelectorAll("[data-pillar]"), {
      trigger: root.querySelector("[data-panel]"),
      y: 24,
    });
  });

  return (
    <section ref={root} id="security" className="relative scroll-mt-24 py-24 sm:py-28">
      <div className="mx-auto max-w-6xl px-6">
        <SectionHeading
          kicker="Built for IT"
          title="Enterprise-grade from day one"
          lede="The controls your security review asks about are the foundation, not the roadmap."
        />
        <div
          data-panel
          className="glass relative mt-14 overflow-hidden rounded-2xl"
        >
          {/* Brand wash anchored to the panel's corner — light with direction,
              not a uniform haze. */}
          <div
            aria-hidden
            className="pointer-events-none absolute inset-0"
            style={{
              background:
                "radial-gradient(42rem 20rem at 8% -20%, rgb(var(--grad-a) / 0.1), rgb(var(--grad-b) / 0.04) 45%, transparent 70%)",
            }}
          />
          <div className="relative grid sm:grid-cols-2 lg:grid-cols-4">
            {PILLARS.map(({ icon: Icon, title, desc }, i) => (
              <div
                key={title}
                data-pillar
                className={`group relative p-7 ${
                  i > 0 ? "border-t border-white/[0.06] sm:border-t-0" : ""
                } ${i % 2 === 1 ? "sm:border-l sm:border-white/[0.06]" : ""} ${
                  i >= 2 ? "sm:border-t sm:border-white/[0.06] lg:border-t-0" : ""
                } ${i > 0 ? "lg:border-l lg:border-white/[0.06]" : ""}`}
              >
                <div className="flex items-center justify-between">
                  <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-brand-indigo/15 text-brand-cyan ring-1 ring-inset ring-brand-indigo/25">
                    <Icon size={19} aria-hidden />
                  </div>
                  <span aria-hidden className="font-mono text-[11px] tracking-[0.12em] text-ink-subtle">
                    0{i + 1}
                  </span>
                </div>
                <h3 className="mt-5 font-display text-base font-semibold text-ink">{title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-ink-muted">{desc}</p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
