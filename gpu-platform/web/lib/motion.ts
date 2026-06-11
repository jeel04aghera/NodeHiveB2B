// Motion tokens — the single source of truth for animation timing, mirrored from
// the CSS variables in app/globals.css so CSS transitions and GSAP tweens feel like
// one system.
//
// IMPORTANT: this module must stay free of `gsap` imports. GSAP is loaded lazily
// (dynamic import) by the marketing orchestrator in Phase 2 so it never lands in
// the shared/app bundles; importing it here would drag it everywhere these tokens
// are used.

/** Durations in seconds (GSAP convention). CSS equivalents: --dur-*. */
export const DUR = {
  fast: 0.15, // micro-interactions: hovers, toggles
  base: 0.24, // standard UI transitions
  slow: 0.42, // section entrances, card staggers
  hero: 0.8, // hero/headline reveals
} as const;

/** Easing — CSS strings (match --ease-* in globals.css). */
export const EASE_CSS = {
  expressive: "cubic-bezier(0.22, 1, 0.36, 1)",
  snappy: "cubic-bezier(0.5, 0, 0.15, 1)",
} as const;

/** Easing — GSAP-native names approximating the CSS curves above. */
export const EASE = {
  expressive: "power4.out",
  snappy: "power2.inOut",
} as const;

/** Stagger steps in seconds for grouped entrances (cards, list rows, nav items). */
export const STAGGER = {
  tight: 0.06,
  base: 0.09,
} as const;

/**
 * Reduced-motion gate. Every scroll/entrance animation must check this and fall
 * back to instantly-visible content (no tween, no pin). Safe during SSR (returns
 * false on the server; effects re-check on the client).
 */
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}
