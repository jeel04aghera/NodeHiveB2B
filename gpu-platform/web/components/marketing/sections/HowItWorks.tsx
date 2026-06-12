"use client";
// How it works — scroll storytelling. One directional company→employee flow:
// IT provisions → employee connects → work runs on owned GPUs → IT observes.
//
// Motion model (progressive enhancement, three tiers):
//  - No JS / reduced motion: everything in static flow — step list at full
//    opacity, visuals stacked beside it (desktop) or inline (mobile).
//  - Mobile + motion: simple staggered reveals, no pinning.
//  - Desktop + motion: GSAP converts the visual stack to an absolute
//    cross-fade deck and pins the section while scroll scrubs through steps.
import { Eye, MonitorCheck, PlugZap, Cpu } from "lucide-react";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

const STEPS = [
  {
    icon: PlugZap,
    title: "IT provisions the fleet",
    desc: "Install the agent on the GPU machines you own. They join the control plane over mTLS and show up in the fleet in minutes.",
  },
  {
    icon: MonitorCheck,
    title: "Employees connect securely",
    desc: "Engineers pick a template and get an SSH or JupyterLab workstation on demand — no tickets, no VPN gymnastics, from anywhere.",
  },
  {
    icon: Cpu,
    title: "Work runs on your GPUs",
    desc: "Training, inference, notebooks — isolated per user and scheduled on your hardware, inside your network.",
  },
  {
    icon: Eye,
    title: "IT sees usage and cost",
    desc: "Live utilization, budgets, and per-department chargeback. Every GPU hour is attributed, every action audit-logged.",
  },
];

/* Abstract product vignettes — illustrative UI sketches, not screenshots or
   real data. Kept as cheap divs (no images, no backdrop-filter in long lists). */
function VisualProvision() {
  return (
    <div className="space-y-2.5">
      {["gpu-node-01", "gpu-node-02", "gpu-node-03"].map((n, i) => (
        <div
          key={n}
          className="flex items-center justify-between rounded-lg border border-line bg-subtle/80 px-4 py-3"
        >
          <span className="flex items-center gap-2.5 font-mono text-xs text-ink">
            <span className={`h-1.5 w-1.5 rounded-full ${i < 2 ? "bg-accent" : "bg-accent motion-safe:animate-nh-pulse"}`} aria-hidden />
            {n}
          </span>
          <span className="rounded-full border border-line px-2 py-0.5 text-[10px] uppercase tracking-wide text-ink-muted">
            agent · mTLS
          </span>
        </div>
      ))}
      <p className="pt-1 text-xs text-ink-muted">3 nodes joined the fleet</p>
    </div>
  );
}

function VisualConnect() {
  return (
    <div className="overflow-hidden rounded-lg border border-line bg-subtle/80 font-mono text-xs leading-6">
      <div className="flex items-center gap-1.5 border-b border-line px-4 py-2.5">
        <span className="h-2 w-2 rounded-full bg-line-strong" aria-hidden />
        <span className="h-2 w-2 rounded-full bg-line-strong" aria-hidden />
        <span className="h-2 w-2 rounded-full bg-line-strong" aria-hidden />
      </div>
      <div className="px-4 py-3.5 text-ink-muted">
        <p>
          <span className="text-brand-cyan">$</span> <span className="text-ink">nodehive connect ws-04</span>
        </p>
        <p>→ workstation ready · 1× GPU · pytorch-jupyter</p>
        <p>→ ssh ana@ws-04 · JupyterLab on :8888</p>
      </div>
    </div>
  );
}

function VisualCompute() {
  const BARS = [82, 64, 91, 47]; // illustrative utilization sketch
  return (
    <div className="space-y-3">
      {BARS.map((w, i) => (
        <div key={i} className="space-y-1.5">
          <div className="flex justify-between font-mono text-[11px] text-ink-muted">
            <span>GPU {i}</span>
            <span className="tnum">{w}%</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-subtle">
            <div className="h-full rounded-full bg-gradient-brand" style={{ width: `${w}%` }} />
          </div>
        </div>
      ))}
      <p className="pt-1 text-xs text-ink-muted">Isolated per user · scheduled by the control plane</p>
    </div>
  );
}

function VisualObserve() {
  const DEPTS = [
    { name: "ml-research", share: 56 },
    { name: "platform", share: 28 },
    { name: "rendering", share: 16 },
  ]; // illustrative chargeback sketch
  return (
    <div className="space-y-2.5">
      {DEPTS.map((d) => (
        <div
          key={d.name}
          className="flex items-center gap-3 rounded-lg border border-line bg-subtle/80 px-4 py-3"
        >
          <span className="w-24 shrink-0 font-mono text-xs text-ink">{d.name}</span>
          <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-canvas">
            <div className="h-full rounded-full bg-gradient-brand" style={{ width: `${d.share}%` }} />
          </div>
          <span className="tnum w-9 text-right font-mono text-xs text-ink-muted">{d.share}%</span>
        </div>
      ))}
      <p className="pt-1 text-xs text-ink-muted">GPU hours attributed per department</p>
    </div>
  );
}

const VISUALS = [VisualProvision, VisualConnect, VisualCompute, VisualObserve];

function VisualCard({ index }: { index: number }) {
  const Visual = VISUALS[index];
  return (
    <div className="glass rounded-xl p-5">
      <p className="mb-4 font-display text-[11px] font-semibold uppercase tracking-[0.16em] text-ink-muted">
        Step {index + 1} — {STEPS[index].title}
      </p>
      <Visual />
    </div>
  );
}

export function HowItWorks() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    reveal(gsap, root.querySelectorAll("[data-reveal]"), { trigger: root });

    const mm = gsap.matchMedia(root);

    mm.add("(min-width: 1024px)", () => {
      const steps = Array.from(root.querySelectorAll<HTMLElement>("[data-step]"));
      const visuals = Array.from(root.querySelectorAll<HTMLElement>("[data-visual-deck] [data-visual]"));
      const rail = root.querySelector<HTMLElement>("[data-rail-fill]");
      const railTrack = root.querySelector<HTMLElement>("[data-rail]");
      const railDot = root.querySelector<HTMLElement>("[data-rail-dot]");
      const glows = Array.from(root.querySelectorAll<HTMLElement>("[data-step-glow]"));

      // Convert the static visual stack into an absolute cross-fade deck.
      // Height = the tallest card, measured while still in flow — no dead space.
      const deckH = Math.max(...visuals.map((v) => v.offsetHeight));
      gsap.set("[data-visual-deck]", { position: "relative", height: deckH });
      gsap.set(visuals, { position: "absolute", inset: 0, autoAlpha: 0, y: 18, scale: 0.985, transformOrigin: "center top" });
      gsap.set(visuals[0], { autoAlpha: 1, y: 0, scale: 1 });
      gsap.set(steps.slice(1), { opacity: 0.38 });
      if (glows[0]) gsap.set(glows[0], { opacity: 1 });
      gsap.set(rail, { scaleY: 0, transformOrigin: "top" });

      const tl = gsap.timeline({
        defaults: { ease: "none" },
        scrollTrigger: {
          trigger: root.querySelector("[data-pin]"),
          start: "top 96px",
          end: "+=1700",
          scrub: 0.45,
          pin: true,
        },
      });
      tl.to(rail, { scaleY: 1, duration: 3 }, 0);
      // Comet head rides the rail fill's leading edge (separate element — the
      // fill is scaleY'd, which would squash anything inside it).
      if (railDot && railTrack) {
        gsap.set(railDot, { autoAlpha: 1 });
        tl.to(railDot, { y: railTrack.offsetHeight - 6, duration: 3 }, 0);
      }
      const FADE = 0.18; // short overlap — outgoing and incoming never double-expose
      STEPS.forEach((_, i) => {
        if (i === 0) return;
        const at = i - FADE;
        tl.to(visuals[i - 1], { autoAlpha: 0, y: -14, scale: 0.985, duration: FADE }, at);
        tl.to(visuals[i], { autoAlpha: 1, y: 0, scale: 1, duration: FADE }, at);
        tl.to(steps[i - 1], { opacity: 0.38, duration: FADE }, at);
        tl.to(steps[i], { opacity: 1, duration: FADE }, at);
        if (glows[i - 1]) tl.to(glows[i - 1], { opacity: 0, duration: FADE }, at);
        if (glows[i]) tl.to(glows[i], { opacity: 1, duration: FADE }, at);
      });
    });

    mm.add("(max-width: 1023px)", () => {
      reveal(gsap, root.querySelectorAll("[data-step]"), {
        trigger: root.querySelector("[data-pin]"),
        y: 32,
      });
    });
  });

  return (
    <section ref={root} id="how-it-works" className="relative scroll-mt-24 py-24 sm:py-28">
      {/* Faint fabric texture behind the story, masked so it dissolves at the edges. */}
      <div
        aria-hidden
        className="bg-grid absolute inset-0 [mask-image:radial-gradient(60rem_32rem_at_50%_40%,black,transparent)]"
      />
      <div className="relative mx-auto max-w-6xl px-6">
        <SectionHeading
          kicker="How it works"
          title="From your rack to a remote engineer in four steps"
          lede="One control plane connects the GPUs you own to the people who need them."
        />

        <div data-pin className="mt-16 lg:grid lg:grid-cols-2 lg:items-center lg:gap-16">
          <ol className="relative space-y-10 lg:space-y-12">
            {/* Progress rail (desktop): gradient fill + comet head scrub with
                the timeline. The fill glows so progress reads at a glance. */}
            <div
              aria-hidden
              data-rail
              className="absolute bottom-2 left-[19px] top-2 hidden w-px bg-white/[0.08] lg:block"
            >
              <div
                data-rail-fill
                className="h-full w-full bg-gradient-brand shadow-[0_0_12px_rgb(var(--grad-b)/0.55)]"
              />
              <div
                data-rail-dot
                className="absolute -left-[2.5px] top-0 h-1.5 w-1.5 rounded-full bg-brand-cyan opacity-0 shadow-[0_0_10px_2px_rgb(var(--grad-c)/0.5)]"
              />
            </div>
            {STEPS.map(({ icon: Icon, title, desc }, i) => (
              <li key={title} data-step className="relative flex gap-5 lg:pl-0">
                <div className="glass relative z-10 flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-brand-cyan">
                  {/* Brand ring that lights up while this step is active (desktop scrub). */}
                  <span
                    data-step-glow
                    aria-hidden
                    className="absolute inset-0 rounded-lg opacity-0 shadow-[0_0_0_1px_rgb(var(--grad-b)/0.5),0_0_16px_-2px_rgb(var(--grad-b)/0.6)]"
                  />
                  <Icon size={18} aria-hidden />
                </div>
                <div>
                  <h3 className="font-display text-lg font-semibold text-ink">{title}</h3>
                  <p className="mt-1.5 max-w-md text-sm leading-relaxed text-ink-muted">{desc}</p>
                  {/* Inline visual (mobile flow only). */}
                  <div aria-hidden className="mt-4 lg:hidden">
                    <VisualCard index={i} />
                  </div>
                </div>
              </li>
            ))}
          </ol>

          {/* Visual deck (desktop). Static = stacked; GSAP turns it into a
              pinned cross-fade. Decorative — the steps carry the content. */}
          <div data-visual-deck aria-hidden className="hidden space-y-4 lg:block">
            {STEPS.map((_, i) => (
              <div key={i} data-visual>
                <VisualCard index={i} />
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
