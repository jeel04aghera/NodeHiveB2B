"use client";
// Trust band — B2B credibility pillars. Every claim here maps to a shipped
// capability (mTLS transport, hashed credentials, RBAC, audit logs, health
// checks/observability, budgets/credits/chargeback) — no invented compliance
// badges. A customer-logo row intentionally does NOT exist yet:
// TODO(logos): add a logo strip here once real customers approve usage.
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
    reveal(gsap, root.querySelectorAll("[data-card]"), {
      trigger: root.querySelector("[data-cards]"),
      y: 32,
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
        <div data-cards className="mt-14 grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
          {PILLARS.map(({ icon: Icon, title, desc }) => (
            <div
              key={title}
              data-card
              className="glass group rounded-xl p-6 transition-[border-color,box-shadow] duration-150 ease-snappy hover:border-white/20"
            >
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-brand-indigo/15 text-brand-cyan ring-1 ring-inset ring-brand-indigo/25">
                <Icon size={19} aria-hidden />
              </div>
              <h3 className="mt-4 font-display text-base font-semibold text-ink">{title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-ink-muted">{desc}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
