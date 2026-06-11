"use client";
// Testimonials — structural only. The hard rule for this page is NO fabricated
// customers, quotes, or logos, so the section renders nothing until real,
// approved quotes exist. The markup below is the finished layout — flip
// HAS_TESTIMONIALS and fill QUOTES to launch it.
// TODO(testimonials): collect real customer quotes (name, role, company,
// written approval) and enable.
import { Quote } from "lucide-react";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

const HAS_TESTIMONIALS = false;

const QUOTES: { quote: string; name: string; role: string }[] = [
  // { quote: "…", name: "…", role: "Head of Platform, …" },
];

export function Testimonials() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    reveal(gsap, root.querySelectorAll("[data-reveal]"), { trigger: root });
    reveal(gsap, root.querySelectorAll("[data-quote]"), {
      trigger: root.querySelector("[data-quotes]"),
      y: 32,
    });
  });

  if (!HAS_TESTIMONIALS || QUOTES.length === 0) return null;

  return (
    <section ref={root} id="testimonials" className="relative scroll-mt-24 py-24 sm:py-28">
      <div className="mx-auto max-w-6xl px-6">
        <SectionHeading kicker="Customers" title="Teams that brought their GPUs home" />
        <div data-quotes className="mt-14 grid gap-5 lg:grid-cols-3">
          {QUOTES.map((q) => (
            <figure key={q.name} data-quote className="glass rounded-xl p-7">
              <Quote size={18} className="text-brand-cyan" aria-hidden />
              <blockquote className="mt-4 text-sm leading-relaxed text-ink">{q.quote}</blockquote>
              <figcaption className="mt-5 text-sm">
                <span className="font-medium text-ink">{q.name}</span>
                <span className="block text-ink-muted">{q.role}</span>
              </figcaption>
            </figure>
          ))}
        </div>
      </div>
    </section>
  );
}
