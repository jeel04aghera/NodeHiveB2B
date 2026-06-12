/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./app/**/*.{ts,tsx}", "./lib/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // Semantic surfaces driven by CSS variables so the app can be dark while the
        // public marketing pages opt into a light scope (.nh-light). Refined neutral
        // "mission control" palette — color reserved for status + the emerald accent.
        canvas: "rgb(var(--c-canvas) / <alpha-value>)",
        surface: "rgb(var(--c-surface) / <alpha-value>)",
        subtle: "rgb(var(--c-subtle) / <alpha-value>)",
        line: "rgb(var(--c-line) / <alpha-value>)",
        "line-strong": "rgb(var(--c-line-strong) / <alpha-value>)",
        ink: "rgb(var(--c-ink) / <alpha-value>)",
        "ink-muted": "rgb(var(--c-ink-muted) / <alpha-value>)",
        "ink-subtle": "rgb(var(--c-ink-subtle) / <alpha-value>)",
        accent: "rgb(var(--c-accent) / <alpha-value>)",
        "accent-soft": "rgb(var(--c-accent-soft) / <alpha-value>)",
        // UI-overhaul brand hues (Phase 1, additive). Use for tints/glows; the full
        // gradient is `bg-gradient-brand` / `.text-gradient`.
        "brand-violet": "rgb(var(--grad-a) / <alpha-value>)",
        "brand-indigo": "rgb(var(--grad-b) / <alpha-value>)",
        "brand-cyan": "rgb(var(--grad-c) / <alpha-value>)",
      },
      backgroundImage: {
        "gradient-brand": "var(--gradient-brand)",
      },
      fontFamily: {
        // Opt-in faces (CSS variables set in app/layout.tsx). `font-sans` default is
        // deliberately untouched until Phase 4 so the dashboard doesn't re-font.
        display: ["var(--font-display)", "ui-sans-serif", "system-ui", "sans-serif"],
        body: ["var(--font-sans)", "ui-sans-serif", "system-ui", "sans-serif"],
      },
      fontSize: {
        // Fluid display scale for marketing headings (clamp = no breakpoint jumps).
        // Optical tracking tightens as size grows (large display sizes need
        // noticeably negative tracking to read "typeset" rather than "sized").
        "display-xl": ["clamp(2.75rem, 1.4rem + 5vw, 4.5rem)", { lineHeight: "1.02", letterSpacing: "-0.042em", fontWeight: "600" }],
        "display-lg": ["clamp(2.1rem, 1.3rem + 3vw, 3.25rem)", { lineHeight: "1.06", letterSpacing: "-0.032em", fontWeight: "600" }],
        "display-md": ["clamp(1.6rem, 1.25rem + 1.5vw, 2.25rem)", { lineHeight: "1.12", letterSpacing: "-0.022em", fontWeight: "600" }],
      },
      boxShadow: {
        card: "0 1px 2px 0 rgb(0 0 0 / 0.35)",
        pop: "0 8px 28px -6px rgb(0 0 0 / 0.55), 0 2px 8px -2px rgb(0 0 0 / 0.4)",
        glow: "0 0 0 1px rgb(16 185 129 / 0.15), 0 0 24px -6px rgb(16 185 129 / 0.25)",
        // Brand-gradient glow for primary CTAs / highlighted glass cards.
        "glow-brand": "0 0 0 1px rgb(var(--grad-b) / 0.35), 0 10px 38px -10px rgb(var(--grad-b) / 0.55)",
        "glow-brand-lg": "0 0 0 1px rgb(var(--grad-b) / 0.45), 0 16px 56px -12px rgb(var(--grad-b) / 0.7), 0 0 24px -4px rgb(var(--grad-c) / 0.35)",
      },
      transitionTimingFunction: {
        expressive: "var(--ease-expressive)",
        snappy: "var(--ease-snappy)",
      },
      keyframes: {
        "nh-pulse": {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0.4" },
        },
        "nh-fade-up": {
          from: { opacity: "0", transform: "translateY(6px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        // Phase 2 (marketing): canvas fade-in over the static fallback, and a
        // slow ambient drift for decorative glow accents (motion-safe: only).
        "nh-fade-in-slow": {
          from: { opacity: "0" },
          to: { opacity: "1" },
        },
        "nh-float": {
          "0%, 100%": { transform: "translateY(0)" },
          "50%": { transform: "translateY(-10px)" },
        },
      },
      animation: {
        "nh-pulse": "nh-pulse 2s ease-in-out infinite",
        "nh-fade-up": "nh-fade-up 240ms ease-out",
        "nh-fade-in-slow": "nh-fade-in-slow 1.2s cubic-bezier(0.22, 1, 0.36, 1) both",
        "nh-float": "nh-float 9s ease-in-out infinite",
      },
    },
  },
  plugins: [],
};
