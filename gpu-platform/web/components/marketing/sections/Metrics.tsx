"use client";
// Metrics — count-up stats + an animated utilization sketch.
// ─────────────────────────────────────────────────────────────────────────────
// TODO(real-data): every number here is an ILLUSTRATIVE PLACEHOLDER and the
// section says so visibly (badge under the heading). Replace values + remove
// the badge once measured platform data exists. Do not ship real-sounding
// claims from this file.
// ─────────────────────────────────────────────────────────────────────────────
import { DUR, EASE, STAGGER } from "@/lib/motion";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

const STATS = [
  { value: 60, prefix: "<", suffix: "s", label: "from request to a running workstation" },
  { value: 100, suffix: "%", label: "of compute on hardware you own" },
  { value: 0, start: 9, label: "tickets filed to get a GPU" },
  { value: 4, suffix: " steps", label: "from rack to remote engineer" },
];

// Illustrative weekly utilization curve for the bar sketch (not real data).
const BARS = [34, 48, 61, 55, 72, 80, 68, 74, 88, 79, 84, 91];

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

    // Utilization sketch: bars grow in with a tight stagger.
    gsap.fromTo(
      root.querySelectorAll("[data-bar]"),
      { scaleY: 0.06 },
      {
        scaleY: 1,
        transformOrigin: "bottom",
        duration: DUR.slow,
        ease: EASE.expressive,
        stagger: STAGGER.tight,
        scrollTrigger: { trigger: root.querySelector("[data-bars]"), start: "top 85%", once: true },
      },
    );
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
          <span className="inline-flex items-center rounded-full border border-line px-3 py-1 text-[11px] uppercase tracking-wide text-ink-subtle">
            Illustrative figures — real platform data lands here
          </span>
        </p>

        <dl className="mt-14 grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
          {STATS.map((s) => (
            <div key={s.label} className="glass rounded-xl p-6 text-center">
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
              <dt className="mt-2 text-sm text-ink-muted">{s.label}</dt>
            </div>
          ))}
        </dl>

        <figure data-reveal className="mx-auto mt-14 max-w-3xl">
          <div
            data-bars
            className="glass flex h-36 items-end justify-between gap-2 rounded-xl px-6 pb-0 pt-6"
            role="img"
            aria-label="Illustrative chart of GPU utilization rising over a rollout"
          >
            {BARS.map((h, i) => (
              <div
                key={i}
                data-bar
                className="w-full rounded-t-sm bg-gradient-to-t from-brand-indigo/70 to-brand-cyan/80"
                style={{ height: `${h}%` }}
              />
            ))}
          </div>
          <figcaption className="mt-3 text-center text-xs text-ink-subtle">
            Fleet utilization over a rollout — illustrative sketch, not customer data.
          </figcaption>
        </figure>
      </div>
    </section>
  );
}
