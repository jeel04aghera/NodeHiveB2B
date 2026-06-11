"use client";
// Final CTA — the second (and last) viewport region allowed a gradient CTA +
// gradient phrase. Gradient-border frame over the mesh, ambient glow accents
// drifting motion-safe only.
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { DUR, EASE, STAGGER } from "@/lib/motion";
import { useGsap } from "../useGsap";

export function FinalCta() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    gsap.fromTo(
      root.querySelector("[data-panel]"),
      { autoAlpha: 0, y: 36, scale: 0.985 },
      {
        autoAlpha: 1,
        y: 0,
        scale: 1,
        duration: DUR.hero,
        ease: EASE.expressive,
        scrollTrigger: { trigger: root, start: "top 78%", once: true },
      },
    );
    gsap.fromTo(
      root.querySelectorAll("[data-reveal]"),
      { autoAlpha: 0, y: 22 },
      {
        autoAlpha: 1,
        y: 0,
        duration: DUR.slow,
        ease: EASE.expressive,
        stagger: STAGGER.base,
        delay: 0.15,
        scrollTrigger: { trigger: root, start: "top 78%", once: true },
      },
    );
  });

  return (
    <section ref={root} className="relative px-6 pb-28 pt-8">
      <div data-panel className="gradient-border relative mx-auto max-w-5xl overflow-hidden rounded-2xl">
        <div className="bg-mesh relative px-8 py-16 text-center sm:px-12 sm:py-20">
          {/* Drifting accent glows — decorative, paused under reduced motion. */}
          <div
            aria-hidden
            className="absolute -left-16 -top-16 h-56 w-56 rounded-full opacity-50 motion-safe:animate-nh-float"
            style={{ background: "radial-gradient(closest-side, rgb(var(--grad-a) / 0.35), transparent)" }}
          />
          <div
            aria-hidden
            className="absolute -bottom-20 -right-12 h-64 w-64 rounded-full opacity-50 motion-safe:animate-nh-float [animation-delay:-4.5s]"
            style={{ background: "radial-gradient(closest-side, rgb(var(--grad-c) / 0.28), transparent)" }}
          />

          <h2 data-reveal className="relative mx-auto max-w-2xl font-display text-display-lg text-ink">
            Give your team GPUs that <span className="text-gradient">travel with them.</span>
          </h2>
          <p data-reveal className="relative mx-auto mt-4 max-w-xl text-base leading-relaxed text-ink-muted">
            Your hardware, their laptops, zero compromise. See NodeHive on your
            own fleet — most teams are running the same day.
          </p>
          <div data-reveal className="relative mt-9 flex flex-wrap items-center justify-center gap-3.5">
            <a
              href="mailto:demo@nodehive.cloud?subject=NodeHive%20demo"
              className="inline-flex h-11 items-center gap-2 rounded-md bg-gradient-brand px-6 text-sm font-medium text-white shadow-glow-brand transition-[box-shadow,filter] duration-150 ease-snappy hover:shadow-glow-brand-lg hover:brightness-110 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
            >
              Book a demo <ArrowRight size={16} aria-hidden />
            </a>
            <Link
              href="/signup"
              className="glass inline-flex h-11 items-center rounded-md px-6 text-sm font-medium text-ink transition-colors duration-150 ease-snappy hover:bg-white/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
            >
              Start a free trial
            </Link>
          </div>
          <p data-reveal className="relative mt-5 text-xs text-ink-subtle">
            Free on up to 2 GPUs — no card, no procurement cycle.
          </p>
        </div>
      </div>
    </section>
  );
}
