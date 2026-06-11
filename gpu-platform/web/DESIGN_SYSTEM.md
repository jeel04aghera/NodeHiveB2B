# NodeHive Design System (UI overhaul)

Single source of truth for the gradient + glass visual system. Tokens live in
`app/globals.css` (`:root`) and are exposed to Tailwind in `tailwind.config.js`;
motion values are mirrored for GSAP in `lib/motion.ts`. Living showcase:
**`/style-preview`** (unlinked route, delete after launch).

Positioning voice for all marketing copy: **private B2B GPU cloud** — a company
gives its own employees secure, on-demand access to GPU compute it owns. Buyer is
IT / engineering leadership; users are employees. Never marketplace language
(no "providers", no "earnings").

## Color

Dark canvas is the base everywhere (the `.nh-light` landing scope is retired in
Phase 2). Existing neutral + status tokens are unchanged — emerald remains the
live/success signal and all dashboard status mappings (`components/ui/status.ts`)
stay as they are.

| Token | Value | Use |
|---|---|---|
| `--c-canvas` | `#0b0b0c` | page background |
| `--c-surface` / `--c-subtle` | `#161618` / `#1e1e21` | opaque cards, inputs |
| `--c-line` / `--c-line-strong` | `#2a2a2e` / `#3a3a40` | hairlines |
| `--c-ink` / `--c-ink-muted` / `--c-ink-subtle` | `#f4f4f5` / `#a1a1aa` / `#71717a` | text hierarchy |
| `--c-accent` | emerald `#10b981` | live/success/status only |
| `--grad-a → b → c` | violet `#8b5cf6` → indigo `#6366f1` → cyan `#22d3ee` | the brand gradient |

**Brand gradient** (`--gradient-brand`, 135°): the conversion color. Tailwind:
`bg-gradient-brand`, text via `.text-gradient`, tints via `brand-violet/indigo/cyan`
with alpha (e.g. `bg-brand-indigo/20`). **Restraint rule: at most one gradient CTA
and one gradient text phrase per viewport region.** Status colors never gradient.

## Glass

One recipe, never hand-rolled blur values:

- `.glass` — `rgb(255 255 255 / 0.05)` fill, `backdrop-blur(16px) saturate(140%)`,
  1px `rgb(255 255 255 / 0.09)` border, inset top highlight.
- `.glass-strong` — 0.09 fill / 0.14 border / 24px blur (nav, panels over imagery).
- `.gradient-border` — 1px brand-gradient edge over an opaque surface (highlighted
  plan cards). Opaque by design: don't combine with `.glass` on the same node.

**Rules:** glass is page *chrome* — nav, hero cards, feature panels. Never on table
rows, list items, or long scroll containers (`backdrop-filter` is GPU-expensive per
layer). Text on glass: `text-ink` / `text-ink-muted` only — `ink-subtle` fails AA
on translucent fills. Backgrounds behind glass: `.bg-mesh` (pure-CSS radial
gradient mesh, no image payload).

## Typography

Loaded via `next/font` as CSS variables in `app/layout.tsx` — **opt-in classes
only** until Phase 4 (the dashboard keeps the system stack until then):

- `font-display` — Space Grotesk: headings, nav wordmark, stat numerals.
- `font-body` — Inter: marketing body copy.

Fluid display scale (Tailwind `fontSize`, clamp() — no breakpoint jumps):
`text-display-xl` (2.75→4.5rem, -0.03em), `text-display-lg` (2.1→3.25rem),
`text-display-md` (1.6→2.25rem). All weight 600, tight leading.

## Motion

Tokens in CSS (`--ease-*`, `--dur-*`) and `lib/motion.ts` (`DUR`, `EASE`,
`EASE_CSS`, `STAGGER`):

| Token | Value | Use |
|---|---|---|
| `expressive` | `cubic-bezier(0.22,1,0.36,1)` ≈ GSAP `power4.out` | entrances, hero (420–800ms) |
| `snappy` | `cubic-bezier(0.5,0,0.15,1)` ≈ GSAP `power2.inOut` | micro-interactions (150–240ms) |
| durations | fast 150 / base 240 / slow 420 / hero 800 ms | |
| stagger | tight 60ms / base 90ms | grouped card/list entrances |

Tailwind: `ease-expressive`, `ease-snappy`.

**Policy:** GSAP is *always* dynamically imported (never in shared bundles —
`lib/motion.ts` must stay free of `gsap` imports). Every scroll/entrance animation
checks `prefersReducedMotion()` and falls back to instantly-visible content; the
`.anim-reveal` class is additionally hard-killed by a global
`prefers-reduced-motion` CSS rule. Hover/focus transitions (user-initiated,
≤240ms) are allowed under reduced motion.

## Shadows & depth

`shadow-card` / `shadow-pop` (existing, unchanged) for opaque surfaces;
`shadow-glow-brand` / `shadow-glow-brand-lg` for gradient CTAs and highlighted
glass cards. Radii: `rounded-md` controls, `rounded-xl` cards, `rounded-2xl`
hero/section frames.

## Component API guarantee

`components/ui/*` public APIs are frozen through the overhaul. Phase 1 added only
the `gradient` Button variant; existing variants/sizes render identically. The
dashboard's *appearance* changes only in Phase 4, by re-pointing token values and
component internals — never call sites.
