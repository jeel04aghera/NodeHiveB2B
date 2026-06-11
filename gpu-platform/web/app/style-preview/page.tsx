"use client";
// Internal style preview (Phase 1 gate artifact) — not linked from anywhere.
// Renders the new design system on the dark canvas: tokens, type, glass, buttons,
// motion. Safe to delete once the overhaul ships; until then it's the living spec.
import { MarketingNav } from "@/components/marketing/Nav";
import { MarketingFooter } from "@/components/marketing/Footer";
import { Button, Badge, Dot } from "@/components/ui";
import { Cpu, ShieldCheck, Activity, ArrowRight } from "lucide-react";

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mx-auto max-w-6xl px-6 py-12">
      <h2 className="text-xs font-semibold uppercase tracking-wider text-ink-subtle">{title}</h2>
      <div className="mt-5">{children}</div>
    </section>
  );
}

export default function StylePreviewPage() {
  return (
    <div className="min-h-screen bg-canvas">
      <MarketingNav />

      {/* Hero-ish mesh block: type scale + gradient on glass */}
      <div className="bg-mesh border-b border-line">
        <div className="mx-auto max-w-6xl px-6 py-20">
          <p className="inline-flex items-center gap-2 rounded-full border border-line bg-surface/60 px-3 py-1 text-xs font-medium text-ink-muted">
            <Dot tone="green" /> Design system preview — Phase 1
          </p>
          <h1 className="mt-6 max-w-3xl font-display text-display-xl text-ink">
            GPU workstations for your team, <span className="text-gradient">from anywhere</span>
          </h1>
          <p className="mt-5 max-w-xl font-body text-lg leading-relaxed text-ink-muted">
            Body face is Inter, display face is Space Grotesk, sized with fluid clamp() —
            this block demonstrates the marketing voice on the dark mesh background.
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Button variant="gradient">Book a demo <ArrowRight size={15} aria-hidden /></Button>
            <Button variant="secondary">See how it works</Button>
          </div>
        </div>
      </div>

      <Section title="Typography scale">
        <div className="space-y-6">
          <h1 className="font-display text-display-xl text-ink">Display XL — clamp(2.75 → 4.5rem)</h1>
          <h2 className="font-display text-display-lg text-ink">Display LG — clamp(2.1 → 3.25rem)</h2>
          <h3 className="font-display text-display-md text-ink">Display MD — clamp(1.6 → 2.25rem)</h3>
          <p className="max-w-2xl font-body text-base leading-relaxed text-ink-muted">
            Body / Inter 16px — Secure, on-demand GPU access for employees, contractors and
            burst capacity, with usage and cost visible to IT per department and per project.
          </p>
          <p className="font-body text-sm text-ink-subtle">Muted small / 14px — captions, metadata, table chrome.</p>
        </div>
      </Section>

      <Section title="Gradient + palette">
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="h-24 rounded-xl bg-gradient-brand shadow-glow-brand" aria-label="Brand gradient: violet → indigo → cyan" />
          <div className="flex h-24 items-end rounded-xl bg-brand-violet/20 p-3 text-xs text-ink-muted ring-1 ring-inset ring-brand-violet/40">brand-violet tints</div>
          <div className="flex h-24 items-end rounded-xl bg-brand-indigo/20 p-3 text-xs text-ink-muted ring-1 ring-inset ring-brand-indigo/40">brand-indigo tints</div>
          <div className="flex h-24 items-end rounded-xl bg-brand-cyan/20 p-3 text-xs text-ink-muted ring-1 ring-inset ring-brand-cyan/40">brand-cyan tints</div>
        </div>
        <p className="mt-4 max-w-2xl text-sm text-ink-muted">
          Existing status colors are untouched — emerald stays the live/success signal:{" "}
          <Badge tone="green">running</Badge> <Badge tone="amber">pending</Badge>{" "}
          <Badge tone="red">failed</Badge> <Badge tone="blue">queued</Badge>{" "}
          <Badge tone="neutral">stopped</Badge>
        </p>
      </Section>

      <Section title="Glass surfaces (on mesh)">
        <div className="bg-mesh rounded-2xl border border-line p-8">
          <div className="grid gap-5 md:grid-cols-3">
            <div className="glass rounded-xl p-6">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-gradient-brand text-white"><Cpu size={18} aria-hidden /></div>
              <h3 className="mt-4 font-display text-base font-semibold text-ink">.glass</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-muted">
                5% white fill, 16px blur, hairline border, inner highlight. Default card chrome.
              </p>
            </div>
            <div className="glass glass-strong rounded-xl p-6">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-gradient-brand text-white"><ShieldCheck size={18} aria-hidden /></div>
              <h3 className="mt-4 font-display text-base font-semibold text-ink">.glass-strong</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-muted">
                9% fill, 24px blur — nav bars and elevated panels that sit over imagery.
              </p>
            </div>
            <div className="gradient-border rounded-xl p-6 shadow-glow-brand">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-gradient-brand text-white"><Activity size={18} aria-hidden /></div>
              <h3 className="mt-4 font-display text-base font-semibold text-ink">.gradient-border</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-muted">
                1px brand-gradient edge over an opaque surface — the “highlighted plan” card.
              </p>
            </div>
          </div>
        </div>
      </Section>

      <Section title="Buttons — all variants keep their existing API">
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="gradient">Gradient · Book a demo</Button>
          <Button variant="primary">Primary</Button>
          <Button variant="secondary">Secondary</Button>
          <Button variant="ghost">Ghost</Button>
          <Button variant="danger">Danger</Button>
          <Button variant="gradient" size="sm">Gradient sm</Button>
          <Button variant="secondary" size="sm">Secondary sm</Button>
          <Button variant="gradient" disabled>Disabled</Button>
        </div>
        <p className="mt-4 text-sm text-ink-muted">
          Hover the gradient button: glow deepens + 10% brightness, 150ms on the snappy ease.
          Micro-interactions are CSS-only (user-initiated → allowed under reduced motion).
        </p>
      </Section>

      <Section title="Motion tokens">
        <div className="grid gap-4 sm:grid-cols-2">
          {[
            { name: "expressive", desc: "cubic-bezier(0.22, 1, 0.36, 1) — entrances, hero reveals (420–800ms)" },
            { name: "snappy", desc: "cubic-bezier(0.5, 0, 0.15, 1) — hovers, toggles (150–240ms)" },
          ].map((e) => (
            <div
              key={e.name}
              className="glass group cursor-default rounded-xl p-5 transition-[transform,box-shadow] duration-300 ease-expressive hover:-translate-y-1 hover:shadow-glow-brand"
            >
              <div className="font-mono text-sm text-ink">--ease-{e.name}</div>
              <p className="mt-1 text-sm text-ink-muted">{e.desc}</p>
            </div>
          ))}
        </div>
        <p className="mt-4 text-sm text-ink-subtle">
          Scroll/entrance animation (GSAP + ScrollTrigger) arrives in Phase 2, gated behind
          prefers-reduced-motion via lib/motion.ts.
        </p>
      </Section>

      <MarketingFooter />
    </div>
  );
}
