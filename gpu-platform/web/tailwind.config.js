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
      },
      boxShadow: {
        card: "0 1px 2px 0 rgb(0 0 0 / 0.35)",
        pop: "0 8px 28px -6px rgb(0 0 0 / 0.55), 0 2px 8px -2px rgb(0 0 0 / 0.4)",
        glow: "0 0 0 1px rgb(16 185 129 / 0.15), 0 0 24px -6px rgb(16 185 129 / 0.25)",
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
      },
      animation: {
        "nh-pulse": "nh-pulse 2s ease-in-out infinite",
        "nh-fade-up": "nh-fade-up 240ms ease-out",
      },
    },
  },
  plugins: [],
};
