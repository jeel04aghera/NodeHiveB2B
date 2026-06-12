"use client";
// Developer / API — terminal-inspired panel for the IT/devops buyer. The
// commands are an illustrative example of the real REST surface (API keys +
// service accounts exist in Settings → API access; workloads are provisioned
// via POST /api/v1/workloads) — generic $NODEHIVE_API host, no invented
// production domains. Typing is a manual char tween (no TextPlugin), final
// text lives in the markup so no-JS / reduced-motion users read it instantly.
import { KeyRound, Repeat, TerminalSquare, Workflow } from "lucide-react";
import { CopyButton } from "@/components/ui/CopyButton";
import { DUR, EASE } from "@/lib/motion";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

type Line = { kind: "comment" | "cmd" | "out"; text: string };

const LINES: Line[] = [
  { kind: "comment", text: "# Mint a key in Settings → API access" },
  { kind: "cmd", text: 'export NODEHIVE_KEY="nh_********"' },
  { kind: "comment", text: "# Provision a GPU workstation for an engineer" },
  {
    kind: "cmd",
    text: `curl -X POST "$NODEHIVE_API/api/v1/workloads" -H "Authorization: Bearer $NODEHIVE_KEY" -d '{"name":"ml-dev","template":"pytorch-jupyter","gpu_count":1}'`,
  },
  { kind: "out", text: '{"id":"wl_8c2e","status":"pending"}' },
  { kind: "comment", text: "# Seconds later" },
  { kind: "cmd", text: 'curl "$NODEHIVE_API/api/v1/workloads/wl_8c2e"' },
  { kind: "out", text: '{"status":"running","ssh":"ssh dev@ws-8c2e"}' },
];

const COPY_TEXT = LINES.filter((l) => l.kind !== "out")
  .map((l) => l.text)
  .join("\n");

const POINTS = [
  {
    icon: KeyRound,
    title: "Keys & service accounts",
    desc: "Personal keys for engineers, service accounts for automation — both with TTLs and revocation.",
  },
  {
    icon: Workflow,
    title: "Provision from CI",
    desc: "Everything the console does is a REST call away: create, stop, and inspect workloads from your pipelines.",
  },
  {
    icon: Repeat,
    title: "One-line agent install",
    desc: "New GPU machine? One command enrolls it into the fleet over mTLS.",
  },
];

export function DeveloperApi() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    reveal(gsap, root.querySelectorAll("[data-reveal]"), { trigger: root });
    reveal(gsap, root.querySelectorAll("[data-point]"), {
      trigger: root.querySelector("[data-points]"),
    });

    // Terminal "session replay": hide all lines, then type commands char by
    // char and snap outputs in — once, when the panel scrolls into view.
    const lineEls = Array.from(root.querySelectorAll<HTMLElement>("[data-line]"));
    const tl = gsap.timeline({
      scrollTrigger: { trigger: root.querySelector("[data-terminal]"), start: "top 75%", once: true },
    });
    lineEls.forEach((el) => {
      const kind = el.dataset.line as Line["kind"];
      if (kind === "cmd") {
        const target = el.querySelector<HTMLElement>("[data-typed]")!;
        const full = target.textContent ?? "";
        const counter = { n: 0 };
        tl.call(() => {
          target.textContent = "";
        });
        tl.set(el, { autoAlpha: 1 });
        tl.to(counter, {
          n: full.length,
          duration: Math.min(full.length * 0.014, 1.6),
          ease: "none",
          onUpdate: () => {
            target.textContent = full.slice(0, Math.round(counter.n));
          },
        });
      } else {
        tl.fromTo(
          el,
          { autoAlpha: 0 },
          { autoAlpha: 1, duration: DUR.fast, ease: EASE.snappy },
          kind === "out" ? "+=0.25" : "+=0.1",
        );
      }
    });
    // Hide everything up front (after measuring) — only when motion is allowed.
    gsap.set(lineEls, { autoAlpha: 0 });
  });

  return (
    <section ref={root} id="developers" className="relative scroll-mt-24 py-24 sm:py-28">
      {/* Section light field — ties the terminal side back to the hero's brand
          light instead of floating in flat black. Multi-stop falloff, no haze. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0"
        style={{
          background:
            "radial-gradient(52rem 30rem at 78% 32%, rgb(var(--grad-b) / 0.07), rgb(var(--grad-b) / 0.025) 45%, transparent 70%), radial-gradient(34rem 22rem at 8% 88%, rgb(var(--grad-a) / 0.05), transparent 70%)",
        }}
      />
      <div className="relative mx-auto max-w-6xl px-6">
        <div className="grid items-center gap-14 lg:grid-cols-2">
          <div>
            <SectionHeading
              align="left"
              kicker="For platform teams"
              title="An API your devops team will actually use"
              lede="The console is optional. Every provision, stop, and usage query is a REST call — automate rollout the way you automate everything else."
            />
            <ul data-points className="mt-10 space-y-6">
              {POINTS.map(({ icon: Icon, title, desc }) => (
                <li key={title} data-point className="flex gap-4">
                  <div className="glass flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-brand-cyan">
                    <Icon size={18} aria-hidden />
                  </div>
                  <div>
                    <h3 className="font-display text-base font-semibold text-ink">{title}</h3>
                    <p className="mt-1 text-sm leading-relaxed text-ink-muted">{desc}</p>
                  </div>
                </li>
              ))}
            </ul>
          </div>

          <div data-reveal className="relative">
            {/* Ambient glow behind the terminal (decorative, motion-safe drift). */}
            <div
              aria-hidden
              className="absolute -inset-8 -z-10 rounded-[2rem] opacity-60 motion-safe:animate-nh-float"
              style={{
                background:
                  "radial-gradient(24rem 16rem at 60% 40%, rgb(var(--grad-b) / 0.22), transparent 70%)",
              }}
            />
            <div data-terminal className="glass-strong glass overflow-hidden rounded-xl shadow-pop">
              <div className="flex items-center justify-between border-b border-white/10 px-4 py-2.5">
                <div className="flex items-center gap-2">
                  <span className="h-2.5 w-2.5 rounded-full bg-line-strong" aria-hidden />
                  <span className="h-2.5 w-2.5 rounded-full bg-line-strong" aria-hidden />
                  <span className="h-2.5 w-2.5 rounded-full bg-line-strong" aria-hidden />
                  <span className="ml-2 flex items-center gap-1.5 text-xs text-ink-muted">
                    <TerminalSquare size={13} aria-hidden /> rollout.sh
                  </span>
                </div>
                <CopyButton value={COPY_TEXT} label="Copy" />
              </div>
              <div className="space-y-1 px-5 py-5 font-mono text-[12.5px] leading-6">
                {LINES.map((l, i) => (
                  <p
                    key={i}
                    data-line={l.kind}
                    className={
                      l.kind === "comment"
                        ? "text-ink-muted"
                        : l.kind === "out"
                          ? "break-all text-brand-cyan/90"
                          : "break-all text-ink"
                    }
                  >
                    {l.kind === "cmd" ? (
                      <>
                        <span className="select-none text-brand-violet">$ </span>
                        <span data-typed>{l.text}</span>
                      </>
                    ) : (
                      l.text
                    )}
                  </p>
                ))}
                <p aria-hidden className="pt-1">
                  <span className="inline-block h-4 w-2 translate-y-0.5 bg-brand-cyan/80 motion-safe:animate-nh-pulse" />
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
