// Shared marketing section header: gradient kicker, display heading, muted lede.
// Children of the section's GSAP context — tag with data-reveal so the section
// orchestrator staggers them in (markup is the final state; see useGsap.ts).
import { cn } from "@/components/ui/cn";

export function SectionHeading({
  kicker,
  title,
  lede,
  align = "center",
}: {
  kicker: string;
  title: React.ReactNode;
  lede?: React.ReactNode;
  align?: "center" | "left";
}) {
  const center = align === "center";
  return (
    <div className={cn("max-w-2xl", center && "mx-auto text-center")}>
      {/* Solid brand tint, not .text-gradient — gradient text is rationed to one
          phrase per viewport region (hero + final CTA own it). */}
      <p
        data-reveal
        className="font-display text-xs font-semibold uppercase tracking-[0.18em] text-brand-cyan"
      >
        {kicker}
      </p>
      <h2 data-reveal className="mt-3 font-display text-display-md text-ink">
        {title}
      </h2>
      {lede && (
        <p data-reveal className="mt-4 text-base leading-relaxed text-ink-muted">
          {lede}
        </p>
      )}
    </div>
  );
}
