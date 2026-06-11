"use client";
// Use cases — immersive hover cards. The "spotlight" follow-cursor highlight is
// two CSS custom properties updated on pointermove (no re-render, no GSAP), so
// it costs nothing at rest and is inherently reduced-motion safe (it only
// responds to the user's own pointer).
import { Briefcase, Globe2, Laptop, Lock, TrendingUp } from "lucide-react";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

const CASES = [
  {
    icon: Globe2,
    title: "Remote & hybrid teams",
    desc: "Engineers get the same GPU workstation at home, in the office, or on the road — the hardware never moves, only the session does.",
  },
  {
    icon: Lock,
    title: "Lockdown continuity",
    desc: "When the office is unreachable, work isn't. The fleet keeps serving secure sessions wherever your people are.",
  },
  {
    icon: Briefcase,
    title: "Contractors, safely",
    desc: "Give external collaborators a workstation, not your data. Access is scoped, time-boxed, and audit-logged end to end.",
  },
  {
    icon: Laptop,
    title: "Workstation replacement",
    desc: "Stop shipping ₹4-lakh laptops. Any thin client becomes a CUDA workstation backed by the GPUs in your rack.",
  },
  {
    icon: TrendingUp,
    title: "Burst capacity",
    desc: "Deadline crunch? Pool idle GPUs across departments and burst where the work is — with chargeback keeping it fair.",
  },
];

function setSpotlight(e: React.PointerEvent<HTMLElement>) {
  const el = e.currentTarget;
  const r = el.getBoundingClientRect();
  el.style.setProperty("--mx", `${e.clientX - r.left}px`);
  el.style.setProperty("--my", `${e.clientY - r.top}px`);
}

export function UseCases() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    reveal(gsap, root.querySelectorAll("[data-reveal]"), { trigger: root });
    reveal(gsap, root.querySelectorAll("[data-card]"), {
      trigger: root.querySelector("[data-cards]"),
      y: 34,
    });
  });

  return (
    <section ref={root} id="product" className="relative scroll-mt-24 py-24 sm:py-28">
      <span id="use-cases" className="absolute -top-24" aria-hidden />
      <div className="mx-auto max-w-6xl px-6">
        <SectionHeading
          kicker="Use cases"
          title="One fleet, every way your team works"
          lede="The same private cloud covers the everyday and the exceptional."
        />
        <div data-cards className="mt-14 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {CASES.map(({ icon: Icon, title, desc }, i) => (
            <article
              key={title}
              data-card
              onPointerMove={setSpotlight}
              className={`group relative overflow-hidden rounded-xl border border-line bg-surface p-6 transition-[border-color,box-shadow,transform] duration-150 ease-snappy hover:-translate-y-0.5 hover:border-brand-indigo/40 hover:shadow-glow-brand ${
                i === 4 ? "sm:col-span-2 lg:col-span-1" : ""
              }`}
            >
              {/* Cursor spotlight + sheen, GPU-cheap (opacity-only transition). */}
              <div
                aria-hidden
                className="pointer-events-none absolute inset-0 opacity-0 transition-opacity duration-300 group-hover:opacity-100"
                style={{
                  background:
                    "radial-gradient(220px circle at var(--mx, 50%) var(--my, 50%), rgb(var(--grad-b) / 0.14), transparent 70%)",
                }}
              />
              <div className="relative">
                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-brand-violet/15 text-brand-cyan ring-1 ring-inset ring-brand-violet/25">
                  <Icon size={19} aria-hidden />
                </div>
                <h3 className="mt-4 font-display text-base font-semibold text-ink">{title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-ink-muted">{desc}</p>
              </div>
            </article>
          ))}
        </div>
      </div>
    </section>
  );
}
