"use client";
// Hero — the one viewport that owns the brand gradient (one gradient CTA, one
// gradient phrase, per the design-system restraint rule). Headline and CTAs are
// server-rendered and visible immediately; the 3D lattice hydrates behind them.
//
// Entrance is choreographed, not uniformly staggered: the headline leads (it's
// the reason the page exists), support copy and actions follow, ambient chrome
// (badge, trust chips) settles last.
import Link from "next/link";
import { ArrowRight, KeyRound, ScrollText, ShieldCheck, Server } from "lucide-react";
import { DUR, EASE } from "@/lib/motion";
import { Hero3D } from "../Hero3D";
import { useGsap } from "../useGsap";

const TRUST_CHIPS = [
  { icon: ShieldCheck, label: "mTLS encrypted" },
  { icon: KeyRound, label: "Role-based access" },
  { icon: ScrollText, label: "Audit logged" },
  { icon: Server, label: "Your hardware" },
];

export function Hero() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    const el = (s: string) => root.querySelector(s);
    const tl = gsap.timeline({ defaults: { ease: EASE.expressive, duration: DUR.slow } });
    tl.fromTo(
      el("[data-hero-h1]"),
      { autoAlpha: 0, y: 34 },
      { autoAlpha: 1, y: 0, duration: DUR.hero, ease: EASE.hero },
      0,
    )
      .fromTo(el("[data-hero-badge]"), { autoAlpha: 0, y: -10 }, { autoAlpha: 1, y: 0 }, 0.08)
      .fromTo(el("[data-hero-lede]"), { autoAlpha: 0, y: 22 }, { autoAlpha: 1, y: 0 }, 0.16)
      .fromTo(el("[data-hero-cta]"), { autoAlpha: 0, y: 18 }, { autoAlpha: 1, y: 0 }, 0.28)
      .fromTo(el("[data-hero-link]"), { autoAlpha: 0 }, { autoAlpha: 1 }, 0.4)
      .fromTo(
        root.querySelectorAll("[data-hero-chip]"),
        { autoAlpha: 0, y: 12 },
        { autoAlpha: 1, y: 0, stagger: 0.05, duration: DUR.base },
        0.48,
      );
  });

  return (
    <section ref={root} className="relative overflow-hidden">
      <Hero3D className="absolute inset-0" />
      {/* Legibility scrim: darker up top (under the glass nav), a pooled radial
          behind the text block so the lattice never crowds the headline, then a
          fade to canvas so the scene dissolves into the next section. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0"
        style={{
          background: [
            "linear-gradient(to bottom, rgb(var(--c-canvas) / 0.55), transparent 26%, transparent 70%, rgb(var(--c-canvas)))",
            "radial-gradient(48rem 26rem at 50% 42%, rgb(var(--c-canvas) / 0.62), transparent 68%)",
          ].join(","),
        }}
      />

      <div className="relative mx-auto flex min-h-[88vh] max-w-6xl flex-col items-center justify-center px-6 pb-32 pt-24 text-center sm:pt-28">
        <span
          data-hero-badge
          className="glass inline-flex items-center gap-2 rounded-full px-3.5 py-1.5 text-xs font-medium text-ink-muted"
        >
          <span
            className="h-1.5 w-1.5 rounded-full bg-brand-cyan motion-safe:animate-nh-pulse"
            aria-hidden
          />
          Private GPU cloud for enterprises
        </span>

        <h1 data-hero-h1 className="mt-7 max-w-4xl font-display text-display-xl text-ink">
          Secure GPU workstations for your team,{" "}
          <span className="text-gradient">anywhere.</span>
        </h1>

        <p
          data-hero-lede
          className="mt-6 max-w-xl font-body text-base leading-relaxed text-ink-muted sm:text-lg"
        >
          NodeHive turns GPUs you already own into a private cloud. Engineers
          get on-demand workstations wherever they work; IT keeps the hardware,
          the data, and the spend.
        </p>

        <div data-hero-cta className="mt-9 flex flex-wrap items-center justify-center gap-3.5">
          <a
            href="mailto:demo@nodehive.cloud?subject=NodeHive%20demo"
            className="group inline-flex h-11 items-center gap-2 rounded-lg bg-gradient-brand px-6 text-sm font-medium text-white shadow-glow-brand transition-[box-shadow,filter] duration-150 ease-snappy hover:shadow-glow-brand-lg hover:brightness-110 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas"
          >
            Book a demo
            <ArrowRight
              size={16}
              aria-hidden
              className="transition-transform duration-150 ease-snappy group-hover:translate-x-0.5"
            />
          </a>
          <a
            href="#how-it-works"
            className="glass inline-flex h-11 items-center rounded-lg px-6 text-sm font-medium text-ink transition-[background-color,border-color] duration-150 ease-snappy hover:border-white/25 hover:bg-white/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60 focus-visible:ring-offset-2 focus-visible:ring-offset-canvas"
          >
            See how it works
          </a>
        </div>

        <p data-hero-link className="mt-5 text-xs text-ink-muted">
          Or{" "}
          <Link
            href="/signup"
            className="rounded-sm font-medium text-ink underline decoration-line-strong underline-offset-4 transition-colors duration-150 hover:decoration-ink focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
          >
            start a free trial
          </Link>{" "}
          on your own hardware.
        </p>

        <ul
          className="mt-14 flex flex-wrap items-center justify-center gap-2.5"
          aria-label="Security guarantees"
        >
          {TRUST_CHIPS.map(({ icon: Icon, label }) => (
            <li
              key={label}
              data-hero-chip
              className="glass inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs text-ink-muted"
            >
              <Icon size={13} className="text-brand-cyan" aria-hidden />
              {label}
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
