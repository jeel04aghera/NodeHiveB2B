// Marketing shell — full footer (Phase 1). Server-safe (no hooks). Legal pages do
// not exist yet; those links are intentionally inert placeholders (flagged at the
// phase gate) so the information architecture is in place for real pages later.
import Link from "next/link";
import { ShieldCheck } from "lucide-react";

const COLUMNS: { heading: string; links: { label: string; href: string }[] }[] = [
  {
    heading: "Product",
    links: [
      { label: "How it works", href: "/#how-it-works" },
      { label: "Plans", href: "/#plans" },
      { label: "Use cases", href: "/#use-cases" },
      { label: "Security", href: "/#security" },
    ],
  },
  {
    heading: "For teams",
    links: [
      { label: "Start a trial", href: "/signup" },
      { label: "Book a demo", href: "mailto:demo@nodehive.cloud?subject=NodeHive%20demo" },
      { label: "Sign in", href: "/login" },
    ],
  },
  {
    heading: "Developers",
    links: [
      { label: "API access", href: "/#developers" },
      { label: "Agent install", href: "/#how-it-works" },
      { label: "Status", href: "/#" }, // TODO: real status page
    ],
  },
  {
    heading: "Company",
    links: [
      { label: "Contact", href: "mailto:demo@nodehive.cloud" },
      { label: "Privacy", href: "/#" }, // TODO: legal pages
      { label: "Terms", href: "/#" },
    ],
  },
];

export function MarketingFooter() {
  return (
    <footer className="border-t border-line bg-canvas">
      <div className="mx-auto max-w-6xl px-6 py-14">
        <div className="grid gap-10 sm:grid-cols-2 lg:grid-cols-6">
          <div className="lg:col-span-2">
            <div className="flex items-center gap-2.5">
              <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-brand text-[14px] font-bold text-white">
                N
              </div>
              <span className="font-display text-[17px] font-semibold tracking-tight text-ink">NodeHive</span>
            </div>
            <p className="mt-4 max-w-xs text-sm leading-relaxed text-ink-muted">
              Secure GPU workstations for your team, on hardware you control — wherever your people work.
            </p>
            <p className="mt-5 inline-flex items-center gap-2 rounded-full border border-line px-3 py-1.5 text-xs text-ink-muted">
              <ShieldCheck size={13} className="text-accent" aria-hidden />
              mTLS · hashed credentials · audit logged · role-based access
            </p>
          </div>

          {COLUMNS.map((col) => (
            <nav key={col.heading} aria-label={col.heading}>
              <h3 className="text-xs font-semibold uppercase tracking-wider text-ink-subtle">{col.heading}</h3>
              <ul className="mt-4 space-y-2.5">
                {col.links.map((l) => (
                  <li key={l.label}>
                    <Link
                      href={l.href}
                      className="rounded-sm text-sm text-ink-muted transition-colors duration-150 hover:text-ink focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
                    >
                      {l.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </nav>
          ))}
        </div>

        <div className="mt-12 flex flex-col items-start justify-between gap-3 border-t border-line pt-6 text-xs text-ink-subtle sm:flex-row sm:items-center">
          <span>© {new Date().getFullYear()} NodeHive. All rights reserved.</span>
          <span>Built for IT &amp; engineering teams that own their GPUs.</span>
        </div>
      </div>
    </footer>
  );
}
