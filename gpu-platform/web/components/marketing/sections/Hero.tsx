"use client";
// Hero — the one viewport that owns the brand gradient (one gradient CTA, one
// gradient phrase, per the design-system restraint rule). Headline and CTAs are
// server-rendered and visible immediately; the 3D lattice hydrates behind them.
import Link from "next/link";
import { ArrowRight, KeyRound, ScrollText, ShieldCheck, Server } from "lucide-react";
import { DUR, EASE, STAGGER } from "@/lib/motion";
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
    gsap.fromTo(
      root.querySelectorAll("[data-hero-reveal]"),
      { autoAlpha: 0, y: 26 },
      {
        autoAlpha: 1,
        y: 0,
        duration: DUR.hero,
        ease: EASE.expressive,
        stagger: STAGGER.base,
        delay: 0.08,
      },
    );
  });

  return (
    <section ref={root} className="relative overflow-hidden">
      <Hero3D className="absolute inset-0" />
      {/* Legibility scrim: darker up top (under the glass nav), clear in the
          middle, fading to canvas so the lattice dissolves into the next
          section instead of ending at a hard edge. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_bottom,rgb(var(--c-canvas)/0.55),transparent_26%,transparent_70%,rgb(var(--c-canvas)))]"
      />

      <div className="relative mx-auto flex min-h-[88vh] max-w-6xl flex-col items-center justify-center px-6 pb-32 pt-24 text-center sm:pt-28">
        <span
          data-hero-reveal
          className="glass inline-flex items-center gap-2 rounded-full px-3.5 py-1.5 text-xs font-medium text-ink-muted"
        >
          <span className="h-1.5 w-1.5 animate-nh-pulse rounded-full bg-accent" aria-hidden />
          Private GPU cloud for enterprises
        </span>

        <h1
          data-hero-reveal
          className="mt-7 max-w-4xl font-display text-display-xl text-ink"
        >
          Secure GPU workstations for your team,{" "}
          <span className="text-gradient">anywhere.</span>
        </h1>

        <p
          data-hero-reveal
          className="mt-6 max-w-2xl font-body text-base leading-relaxed text-ink-muted sm:text-lg"
        >
          NodeHive turns the GPUs you already own into a private cloud. Your
          engineers get on-demand workstations from wherever they work — IT
          keeps the hardware, the data, and the spend under control.
        </p>

        <div data-hero-reveal className="mt-9 flex flex-wrap items-center justify-center gap-3.5">
          <a
            href="mailto:demo@nodehive.cloud?subject=NodeHive%20demo"
            className="inline-flex h-11 items-center gap-2 rounded-md bg-gradient-brand px-6 text-sm font-medium text-white shadow-glow-brand transition-[box-shadow,filter] duration-150 ease-snappy hover:shadow-glow-brand-lg hover:brightness-110 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
          >
            Book a demo <ArrowRight size={16} aria-hidden />
          </a>
          <a
            href="#how-it-works"
            className="glass inline-flex h-11 items-center rounded-md px-6 text-sm font-medium text-ink transition-colors duration-150 ease-snappy hover:bg-white/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
          >
            See how it works
          </a>
        </div>

        <p data-hero-reveal className="mt-5 text-xs text-ink-muted">
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
          data-hero-reveal
          className="mt-14 flex flex-wrap items-center justify-center gap-2.5"
          aria-label="Security guarantees"
        >
          {TRUST_CHIPS.map(({ icon: Icon, label }) => (
            <li
              key={label}
              className="glass inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs text-ink-muted"
            >
              <Icon size={13} className="text-accent" aria-hidden />
              {label}
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
