"use client";
// Marketing motion orchestrator (Phase 2). GSAP + ScrollTrigger are loaded here
// and ONLY here, via dynamic import, so they never enter the shared first-load
// bundle (lib/motion.ts stays gsap-free by policy — see DESIGN_SYSTEM.md).
//
// Contract for every consumer:
//  - Markup renders the FINAL state. Tweens are fromTo()s that hide/offset only
//    after JS + GSAP arrive, so no-JS and reduced-motion users see content
//    instantly and there is no flash of hidden content.
//  - `useGsap` no-ops entirely under prefers-reduced-motion.
//  - Everything runs inside gsap.context() scoped to the section root and is
//    reverted on unmount (kills ScrollTriggers, clears inline styles).
import { useEffect, useRef } from "react";
import { DUR, EASE, STAGGER, prefersReducedMotion } from "@/lib/motion";

// Type-only imports are erased at compile time — no runtime gsap in this chunk.
type GSAP = typeof import("gsap").gsap;
type ScrollTriggerClass = typeof import("gsap/ScrollTrigger").ScrollTrigger;
type GsapContext = ReturnType<GSAP["context"]>;

export interface GsapBundle {
  gsap: GSAP;
  ScrollTrigger: ScrollTriggerClass;
}

let bundle: Promise<GsapBundle> | null = null;

/** Singleton lazy loader — one network fetch + one plugin registration per page. */
export function loadGsap(): Promise<GsapBundle> {
  if (!bundle) {
    bundle = Promise.all([import("gsap"), import("gsap/ScrollTrigger")]).then(
      ([g, st]) => {
        g.gsap.registerPlugin(st.ScrollTrigger);
        return { gsap: g.gsap, ScrollTrigger: st.ScrollTrigger };
      },
    );
  }
  return bundle;
}

/**
 * Section-scoped GSAP hook. `setup` receives the loaded bundle and the root
 * element; everything it creates is scoped + auto-reverted. Never called when
 * the user prefers reduced motion — the server-rendered markup IS the fallback.
 */
export function useGsap<T extends HTMLElement = HTMLElement>(
  setup: (args: GsapBundle & { root: T }) => void,
) {
  const rootRef = useRef<T>(null);
  // The setup closure is captured once on mount by design (sections animate once).
  const setupRef = useRef(setup);
  setupRef.current = setup;

  useEffect(() => {
    if (prefersReducedMotion()) return;
    let ctx: GsapContext | undefined;
    let cancelled = false;
    loadGsap().then(({ gsap, ScrollTrigger }) => {
      if (cancelled || !rootRef.current) return;
      const root = rootRef.current;
      ctx = gsap.context(() => setupRef.current({ gsap, ScrollTrigger, root }), root);
    });
    return () => {
      cancelled = true;
      ctx?.revert();
    };
  }, []);

  return rootRef;
}

/**
 * House entrance: fade-rise with the expressive ease, triggered when the
 * element scrolls into view. fromTo so content is visible pre-JS.
 */
export function reveal(
  gsap: GSAP,
  targets: gsap.TweenTarget,
  opts: {
    /** null tolerated (querySelector) — falls back to an immediate tween. */
    trigger?: Element | string | null;
    y?: number;
    stagger?: number;
    delay?: number;
    duration?: number;
  } = {},
) {
  const { trigger, y = 28, stagger = STAGGER.base, delay = 0, duration = DUR.slow } = opts;
  return gsap.fromTo(
    targets,
    { autoAlpha: 0, y },
    {
      autoAlpha: 1,
      y: 0,
      duration,
      ease: EASE.expressive,
      stagger,
      delay,
      ...(trigger
        ? { scrollTrigger: { trigger, start: "top 80%", once: true } }
        : {}),
    },
  );
}
