"use client";
// Metrics — one instrument panel: a hairline-divided stat ledger over an
// engineered utilization curve. Same physical-glass ledger as TrustBand so the
// two read as one system, not two card recipes.
// ─────────────────────────────────────────────────────────────────────────────
// TODO(real-data): every number here is an ILLUSTRATIVE PLACEHOLDER and the
// section says so visibly (badge under the heading). Replace values + remove
// the badge once measured platform data exists. Do not ship real-sounding
// claims from this file.
// ─────────────────────────────────────────────────────────────────────────────
import { DUR, EASE } from "@/lib/motion";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

const STATS = [
  { value: 60, prefix: "<", suffix: "s", label: "from request to a running workstation" },
  { value: 100, suffix: "%", label: "of compute on hardware you own" },
  { value: 0, start: 9, label: "tickets filed to get a GPU" },
  { value: 4, suffix: " steps", label: "from rack to remote engineer" },
];

// Illustrative weekly utilization curve (not real data) → smooth SVG path.
const POINTS = [34, 48, 61, 55, 72, 80, 68, 74, 88, 79, 84, 91];
const W = 640;
const H = 168;
const PAD = 12;

const XY: [number, number][] = POINTS.map((v, i) => [
  (i / (POINTS.length - 1)) * W,
  H - PAD - (v / 100) * (H - PAD * 2),
]);

// Catmull-Rom → cubic beziers: one continuous engineered line, no bar chart.
function smoothPath(pts: [number, number][]) {
  let d = `M ${pts[0][0].toFixed(1)} ${pts[0][1].toFixed(1)}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[i - 1] ?? pts[i];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[i + 2] ?? p2;
    const c1 = [p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6];
    const c2 = [p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6];
    d += ` C ${c1[0].toFixed(1)} ${c1[1].toFixed(1)} ${c2[0].toFixed(1)} ${c2[1].toFixed(1)} ${p2[0].toFixed(1)} ${p2[1].toFixed(1)}`;
  }
  return d;
}

const LINE_D = smoothPath(XY);
const AREA_D = `${LINE_D} L ${W} ${H} L 0 ${H} Z`;
const [END_X, END_Y] = XY[XY.length - 1];

export function Metrics() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    reveal(gsap, root.querySelectorAll("[data-reveal]"), { trigger: root });

    // Count-ups: tween a counter object and write snapped values into the DOM.
    root.querySelectorAll<HTMLElement>("[data-count]").forEach((el) => {
      const end = Number(el.dataset.count);
      const start = Number(el.dataset.countStart ?? 0);
      const counter = { n: start };
      gsap.fromTo(
        el,
        { autoAlpha: 0, y: 18 },
        {
          autoAlpha: 1,
          y: 0,
          duration: DUR.slow,
          ease: EASE.expressive,
          scrollTrigger: { trigger: el, start: "top 85%", once: true },
          onStart: () => {
            gsap.to(counter, {
              n: end,
              duration: 1.6,
              ease: "power2.out",
              onUpdate: () => {
                el.textContent = String(Math.round(counter.n));
              },
            });
          },
        },
      );
    });

    // Utilization curve draws itself; the area and endpoint follow it in.
    // Markup is the final state — under reduced motion the chart is simply there.
    const line = root.querySelector<SVGPathElement>("[data-chart-line]");
    if (line) {
      const len = line.getTotalLength();
      const tl = gsap.timeline({
        scrollTrigger: { trigger: root.querySelector("[data-chart]"), start: "top 80%", once: true },
      });
      tl.fromTo(
        line,
        { strokeDasharray: len, strokeDashoffset: len },
        { strokeDashoffset: 0, duration: 1.7, ease: EASE.expressive },
      );
      tl.fromTo(
        root.querySelector("[data-chart-area]"),
        { autoAlpha: 0 },
        { autoAlpha: 1, duration: 0.9, ease: "none" },
        0.5,
      );
      tl.fromTo(
        root.querySelector("[data-chart-end]"),
        { autoAlpha: 0, scale: 0.4, transformOrigin: "center" },
        { autoAlpha: 1, scale: 1, duration: DUR.base, ease: EASE.snappy },
        ">-0.15",
      );
    }
  });

  return (
    <section ref={root} id="metrics" className="relative scroll-mt-24 py-24 sm:py-28">
      <div className="mx-auto max-w-6xl px-6">
        <SectionHeading
          kicker="What rollout looks like"
          title="Self-service speed, with the meter running"
          lede="GPU access stops being a queue and starts being a utility — measured, attributed, and visible."
        />
        <p data-reveal className="mt-5 text-center">
          {/* Visible marker required while values are placeholders. */}
          <span className="inline-flex items-center rounded-full border border-line px-3 py-1 text-[11px] uppercase tracking-wide text-ink-muted">
            Illustrative figures — real platform data lands here
          </span>
        </p>

        {/* One instrument panel: stat ledger over the utilization curve. */}
        <div data-reveal className="glass relative mt-14 overflow-hidden rounded-2xl">
          {/* Faint brand wash from the top-left — same light source as TrustBand. */}
          <div
            aria-hidden
            className="pointer-events-none absolute inset-0"
            style={{
              background:
                "radial-gradient(46rem 22rem at 6% -24%, rgb(var(--grad-a) / 0.09), rgb(var(--grad-b) / 0.03) 45%, transparent 70%)",
            }}
          />
          <dl className="relative grid sm:grid-cols-2 lg:grid-cols-4">
            {STATS.map((s, i) => (
              <div
                key={s.label}
                className={`border-white/[0.06] px-7 py-7 ${i > 0 ? "border-t" : ""} ${
                  i % 2 === 1 ? "sm:border-t-0 sm:border-l" : ""
                } ${i === 2 ? "sm:border-t" : ""} ${i === 3 ? "sm:border-t" : ""} lg:border-t-0 ${
                  i > 0 ? "lg:border-l" : ""
                }`}
              >
                <dd className="font-display text-4xl font-semibold tracking-tight text-ink">
                  {s.prefix}
                  <span
                    data-count={s.value}
                    data-count-start={"start" in s ? s.start : undefined}
                    className="tnum"
                  >
                    {s.value}
                  </span>
                  {s.suffix}
                </dd>
                <dt className="mt-2 max-w-[26ch] text-sm leading-relaxed text-ink-muted">
                  {s.label}
                </dt>
              </div>
            ))}
          </dl>

          <figure data-chart className="relative border-t border-white/[0.06]">
            <div className="flex items-baseline justify-between px-7 pt-5">
              <p className="font-display text-xs font-semibold uppercase tracking-[0.18em] text-ink-muted">
                Fleet utilization
              </p>
              <p className="tnum text-xs text-ink-muted" aria-hidden>
                rollout, week 1 → 12
              </p>
            </div>
            <svg
              viewBox={`0 0 ${W} ${H}`}
              className="mt-2 block h-40 w-full"
              role="img"
              aria-label="Illustrative chart of GPU utilization rising over a rollout"
              preserveAspectRatio="none"
            >
              <defs>
                <linearGradient id="nh-util-stroke" x1="0" y1="0" x2="1" y2="0">
                  <stop offset="0%" stopColor="rgb(var(--grad-a))" />
                  <stop offset="50%" stopColor="rgb(var(--grad-b))" />
                  <stop offset="100%" stopColor="rgb(var(--grad-c))" />
                </linearGradient>
                <linearGradient id="nh-util-fill" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="rgb(var(--grad-b))" stopOpacity="0.22" />
                  <stop offset="100%" stopColor="rgb(var(--grad-b))" stopOpacity="0" />
                </linearGradient>
              </defs>
              {/* Hairline reference lines — quiet, like the section grid. */}
              {[0.25, 0.5, 0.75].map((f) => (
                <line
                  key={f}
                  x1="0"
                  x2={W}
                  y1={PAD + f * (H - PAD * 2)}
                  y2={PAD + f * (H - PAD * 2)}
                  stroke="rgb(255 255 255 / 0.05)"
                  strokeWidth="1"
                  vectorEffect="non-scaling-stroke"
                />
              ))}
              <path data-chart-area d={AREA_D} fill="url(#nh-util-fill)" />
              <path
                data-chart-line
                d={LINE_D}
                fill="none"
                stroke="url(#nh-util-stroke)"
                strokeWidth="2"
                strokeLinecap="round"
                vectorEffect="non-scaling-stroke"
              />
              <g data-chart-end>
                <circle cx={END_X} cy={END_Y} r="7" fill="rgb(var(--grad-c) / 0.18)" />
                <circle cx={END_X} cy={END_Y} r="3" fill="rgb(var(--grad-c))" />
              </g>
            </svg>
            <figcaption className="border-t border-white/[0.06] px-7 py-3 text-xs text-ink-muted">
              Fleet utilization over a rollout — illustrative sketch, not customer data.
            </figcaption>
          </figure>
        </div>
      </div>
    </section>
  );
}
