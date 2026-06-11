"use client";
// Marketing shell — sticky glass nav (Phase 1). B2B voice: the conversion action is
// a demo with IT/engineering leadership; "Start trial" is the self-serve path.
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { useAuth } from "@/lib/auth";

const LINKS = [
  { href: "/#product", label: "Product" },
  { href: "/#how-it-works", label: "How it works" },
  { href: "/#developers", label: "Developers" },
  { href: "/#faq", label: "FAQ" },
];

export function MarketingNav() {
  const { user, ready } = useAuth();
  const authed = ready && !!user;

  return (
    <header className="sticky top-0 z-30">
      <div className="glass border-x-0 border-t-0">
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between px-6">
          <Link href="/" className="flex items-center gap-2.5" aria-label="NodeHive home">
            <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-brand text-[14px] font-bold text-white shadow-glow-brand">
              N
            </div>
            <span className="font-display text-[17px] font-semibold tracking-tight text-ink">
              NodeHive
            </span>
          </Link>

          <nav aria-label="Main" className="hidden items-center gap-8 text-sm text-ink-muted md:flex">
            {LINKS.map((l) => (
              <Link
                key={l.href}
                href={l.href}
                className="rounded-sm transition-colors duration-150 hover:text-ink focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
              >
                {l.label}
              </Link>
            ))}
          </nav>

          <div className="flex items-center gap-3">
            {authed ? (
              <Link
                href="/overview"
                className="inline-flex h-9 items-center gap-1.5 rounded-md bg-gradient-brand px-4 text-sm font-medium text-white shadow-glow-brand transition-[box-shadow,filter] duration-150 hover:brightness-110 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
              >
                Dashboard <ArrowRight size={14} aria-hidden />
              </Link>
            ) : (
              <>
                <Link
                  href="/login"
                  className="hidden rounded-sm text-sm font-medium text-ink-muted transition-colors duration-150 hover:text-ink focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60 sm:block"
                >
                  Sign in
                </Link>
                <Link
                  href="/signup"
                  className="inline-flex h-9 items-center gap-1.5 rounded-md bg-gradient-brand px-4 text-sm font-medium text-white shadow-glow-brand transition-[box-shadow,filter] duration-150 hover:shadow-glow-brand-lg hover:brightness-110 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
                >
                  Start trial
                </Link>
              </>
            )}
          </div>
        </div>
      </div>
    </header>
  );
}
