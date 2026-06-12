// Landing page (Phase 2 rebuild) — dark premium marketing surface on the
// Phase 1 design system. This stays a server component (metadata + instant
// headline paint); every section is a client island that lazy-loads its own
// motion (GSAP) and 3D (R3F) behind first paint.
import { MarketingNav } from "@/components/marketing/Nav";
import { MarketingFooter } from "@/components/marketing/Footer";
import { Hero } from "@/components/marketing/sections/Hero";
import { TrustBand } from "@/components/marketing/sections/TrustBand";
import { HowItWorks } from "@/components/marketing/sections/HowItWorks";
import { UseCases } from "@/components/marketing/sections/UseCases";
import { DeveloperApi } from "@/components/marketing/sections/DeveloperApi";
import { Metrics } from "@/components/marketing/sections/Metrics";
import { Testimonials } from "@/components/marketing/sections/Testimonials";
import { Faq } from "@/components/marketing/sections/Faq";
import { FinalCta } from "@/components/marketing/sections/FinalCta";

export const metadata = {
  title: "NodeHive — Secure GPU workstations for your team, anywhere",
  description:
    "Turn the GPUs you own into a private cloud. Employees get secure, on-demand GPU workstations from wherever they work; IT keeps control, visibility, and cost.",
};

export default function LandingPage() {
  return (
    <div className="nh-marketing bg-canvas font-body text-ink">
      {/* Film grain over every section — kills the flat-vector look (texture pass). */}
      <div aria-hidden className="grain" />
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-surface px-4 py-2 text-sm text-ink focus:not-sr-only focus:fixed focus:left-4 focus:top-4"
      >
        Skip to content
      </a>
      <MarketingNav />
      <main id="main">
        <Hero />
        <TrustBand />
        <HowItWorks />
        <UseCases />
        <DeveloperApi />
        <Metrics />
        <Testimonials />
        <Faq />
        <FinalCta />
      </main>
      <MarketingFooter />
    </div>
  );
}
