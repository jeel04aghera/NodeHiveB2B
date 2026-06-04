"use client";
import Link from "next/link";
import { useAuth } from "@/lib/auth";
import {
  Boxes, LayoutTemplate, Receipt, Activity, Terminal, BrainCircuit, FlaskConical,
  Users, Share2, Check, ArrowRight, ShieldCheck,
} from "lucide-react";

const WHY = [
  { icon: Boxes, title: "Self-Service Compute", desc: "Launch GPU environments in seconds — no tickets, no waiting on ops." },
  { icon: LayoutTemplate, title: "Template Catalog", desc: "PyTorch, TensorFlow, CUDA, and Jupyter, ready to deploy." },
  { icon: Receipt, title: "Department Chargeback", desc: "Know exactly where GPU spend goes, per team and per project." },
  { icon: Activity, title: "Capacity Management", desc: "Track utilization and available capacity across the fleet." },
];

const USE_CASES = [
  { icon: BrainCircuit, title: "AI Training", desc: "Multi-GPU training jobs with the frameworks your team already uses." },
  { icon: Terminal, title: "Inference", desc: "Stand up serving environments with SSH and GPU passthrough." },
  { icon: FlaskConical, title: "Data Science", desc: "JupyterLab notebooks backed by real GPUs in one click." },
  { icon: Users, title: "Research Teams", desc: "Give researchers self-service access without losing control." },
  { icon: Share2, title: "Enterprise GPU Sharing", desc: "Pool owned hardware across departments with fair chargeback." },
];

const PRICING = [
  { name: "Starter", price: "₹0", note: "for evaluation", features: ["Up to 2 GPUs", "1 project", "SSH & Jupyter access", "Community support"], cta: "Start Free", highlight: false },
  { name: "Growth", price: "Custom", note: "for growing teams", features: ["Unlimited GPUs", "Projects & departments", "Chargeback & capacity", "Priority support"], cta: "Start Free", highlight: true },
  { name: "Enterprise", price: "Talk to us", note: "for large fleets", features: ["Multi-node fleets", "SSO & audit logs", "mTLS & RBAC", "Dedicated support"], cta: "Book Demo", highlight: false },
];

function Nav({ authed }: { authed: boolean }) {
  return (
    <header className="sticky top-0 z-20 border-b border-line bg-surface/85 backdrop-blur">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-6">
        <Link href="/" className="flex items-center gap-2.5">
          <div className="flex h-6 w-6 items-center justify-center rounded-md bg-neutral-900 text-[13px] font-bold text-white">N</div>
          <span className="text-[15px] font-semibold tracking-tight text-ink">NodeHive</span>
        </Link>
        <nav className="hidden items-center gap-7 text-sm text-ink-muted md:flex">
          <a href="#why" className="hover:text-ink">Product</a>
          <a href="#use-cases" className="hover:text-ink">Use cases</a>
          <a href="#pricing" className="hover:text-ink">Pricing</a>
        </nav>
        <div className="flex items-center gap-2.5">
          {authed ? (
            <Link href="/overview" className="inline-flex h-9 items-center gap-1.5 rounded-md bg-neutral-900 px-4 text-sm font-medium text-white hover:bg-neutral-800">Go to Dashboard <ArrowRight size={14} /></Link>
          ) : (
            <>
              <Link href="/login" className="hidden text-sm font-medium text-ink-muted hover:text-ink sm:block">Sign in</Link>
              <Link href="/signup" className="inline-flex h-9 items-center rounded-md bg-neutral-900 px-4 text-sm font-medium text-white hover:bg-neutral-800">Start Free</Link>
            </>
          )}
        </div>
      </div>
    </header>
  );
}

export default function LandingPage() {
  const { user, ready } = useAuth();
  const authed = ready && !!user;

  return (
    <div className="nh-light min-h-screen bg-canvas">
      <Nav authed={authed} />

      {/* Hero */}
      <section className="mx-auto max-w-6xl px-6 pb-10 pt-16 text-center sm:pt-24">
        <span className="inline-flex items-center gap-1.5 rounded-full border border-line bg-surface px-3 py-1 text-xs font-medium text-ink-muted">
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" /> Private GPU Cloud for Enterprises
        </span>
        <h1 className="mx-auto mt-6 max-w-3xl text-4xl font-semibold leading-[1.1] tracking-tight text-ink sm:text-6xl">
          Private GPU Cloud<br />Without Cloud Complexity
        </h1>
        <p className="mx-auto mt-5 max-w-2xl text-base leading-relaxed text-ink-muted sm:text-lg">
          Deploy AI workloads, manage GPU capacity, track costs, and provide self-service compute for engineering teams — on hardware you own.
        </p>
        <div className="mt-8 flex items-center justify-center gap-3">
          <Link href="/signup" className="inline-flex h-11 items-center gap-2 rounded-md bg-neutral-900 px-6 text-sm font-medium text-white hover:bg-neutral-800">Start Free <ArrowRight size={16} /></Link>
          <a href="mailto:demo@nodehive.cloud?subject=NodeHive%20demo" className="inline-flex h-11 items-center rounded-md border border-neutral-300 bg-surface px-6 text-sm font-medium text-ink hover:bg-neutral-50">Book Demo</a>
        </div>

        {/* Hero product shot (actual UI) */}
        <div className="mx-auto mt-14 max-w-5xl overflow-hidden rounded-xl border border-line bg-surface shadow-pop">
          <img src="/preview/overview.png" alt="NodeHive overview dashboard" className="w-full" />
        </div>
      </section>

      {/* Why */}
      <section id="why" className="border-t border-line bg-surface py-20">
        <div className="mx-auto max-w-6xl px-6">
          <div className="mx-auto max-w-2xl text-center">
            <h2 className="text-3xl font-semibold tracking-tight text-ink">Why NodeHive</h2>
            <p className="mt-3 text-ink-muted">Everything an operations team needs to run GPUs like a cloud — without the cloud bill.</p>
          </div>
          <div className="mt-12 grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
            {WHY.map(({ icon: Icon, title, desc }) => (
              <div key={title} className="rounded-xl border border-line bg-canvas p-6">
                <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-line bg-surface text-ink"><Icon size={19} /></div>
                <h3 className="mt-4 text-base font-semibold text-ink">{title}</h3>
                <p className="mt-1.5 text-sm leading-relaxed text-ink-muted">{desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Product preview */}
      <section className="py-20">
        <div className="mx-auto max-w-6xl px-6">
          <div className="mx-auto max-w-2xl text-center">
            <h2 className="text-3xl font-semibold tracking-tight text-ink">An operational console, not a spreadsheet</h2>
            <p className="mt-3 text-ink-muted">Real-time workloads, capacity, and chargeback in one place.</p>
          </div>
          <div className="mt-12 grid gap-6 lg:grid-cols-2">
            {[
              { src: "/preview/workloads.png", cap: "Workloads — runtime, owner, department, and cost on every row." },
              { src: "/preview/capacity.png", cap: "Capacity — allocation and utilization across the fleet." },
            ].map((s) => (
              <figure key={s.src} className="overflow-hidden rounded-xl border border-line bg-surface shadow-card">
                <img src={s.src} alt={s.cap} className="w-full border-b border-line" />
                <figcaption className="px-5 py-3 text-sm text-ink-muted">{s.cap}</figcaption>
              </figure>
            ))}
          </div>
        </div>
      </section>

      {/* Use cases */}
      <section id="use-cases" className="border-t border-line bg-surface py-20">
        <div className="mx-auto max-w-6xl px-6">
          <h2 className="text-center text-3xl font-semibold tracking-tight text-ink">Built for how teams use GPUs</h2>
          <div className="mt-12 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
            {USE_CASES.map(({ icon: Icon, title, desc }) => (
              <div key={title} className="flex items-start gap-4 rounded-xl border border-line bg-canvas p-6">
                <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-neutral-900 text-white"><Icon size={18} /></div>
                <div><h3 className="text-base font-semibold text-ink">{title}</h3><p className="mt-1 text-sm leading-relaxed text-ink-muted">{desc}</p></div>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Pricing */}
      <section id="pricing" className="py-20">
        <div className="mx-auto max-w-6xl px-6">
          <div className="mx-auto max-w-2xl text-center">
            <h2 className="text-3xl font-semibold tracking-tight text-ink">Simple, transparent pricing</h2>
            <p className="mt-3 text-ink-muted">Start free on your own hardware. Scale when you’re ready.</p>
          </div>
          <div className="mt-12 grid gap-6 lg:grid-cols-3">
            {PRICING.map((p) => (
              <div key={p.name} className={`flex flex-col rounded-xl border bg-surface p-7 ${p.highlight ? "border-neutral-900 shadow-pop" : "border-line shadow-card"}`}>
                <div className="flex items-center justify-between">
                  <h3 className="text-lg font-semibold text-ink">{p.name}</h3>
                  {p.highlight && <span className="rounded-full bg-neutral-900 px-2.5 py-0.5 text-xs font-medium text-white">Popular</span>}
                </div>
                <div className="mt-4 flex items-baseline gap-1.5"><span className="text-3xl font-semibold text-ink">{p.price}</span><span className="text-sm text-ink-subtle">{p.note}</span></div>
                <ul className="mt-6 flex-1 space-y-2.5">
                  {p.features.map((f) => (<li key={f} className="flex items-center gap-2.5 text-sm text-ink-muted"><Check size={15} className="text-emerald-600" /> {f}</li>))}
                </ul>
                <Link href={p.cta === "Book Demo" ? "mailto:demo@nodehive.cloud" : "/signup"} className={`mt-7 inline-flex h-10 items-center justify-center rounded-md text-sm font-medium ${p.highlight ? "bg-neutral-900 text-white hover:bg-neutral-800" : "border border-neutral-300 bg-surface text-ink hover:bg-neutral-50"}`}>{p.cta}</Link>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* CTA */}
      <section className="px-6 pb-20">
        <div className="mx-auto max-w-5xl overflow-hidden rounded-2xl bg-neutral-950 px-8 py-16 text-center">
          <h2 className="mx-auto max-w-2xl text-3xl font-semibold tracking-tight text-white sm:text-4xl">Ready to deploy GPU infrastructure?</h2>
          <p className="mx-auto mt-3 max-w-xl text-neutral-400">Turn the GPUs you own into a self-service private cloud in minutes.</p>
          <div className="mt-8 flex items-center justify-center gap-3">
            <Link href="/signup" className="inline-flex h-11 items-center gap-2 rounded-md bg-white px-6 text-sm font-medium text-neutral-900 hover:bg-neutral-200">Start Free <ArrowRight size={16} /></Link>
            <a href="mailto:demo@nodehive.cloud?subject=NodeHive%20demo" className="inline-flex h-11 items-center rounded-md border border-neutral-700 px-6 text-sm font-medium text-white hover:bg-neutral-900">Book Demo</a>
          </div>
        </div>
      </section>

      <footer className="border-t border-line py-8">
        <div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-3 px-6 text-sm text-ink-subtle sm:flex-row">
          <div className="flex items-center gap-2"><div className="flex h-5 w-5 items-center justify-center rounded bg-neutral-900 text-[10px] font-bold text-white">N</div> © NodeHive</div>
          <div className="flex items-center gap-2"><ShieldCheck size={14} /> mTLS · audit logged · role-based access</div>
        </div>
      </footer>
    </div>
  );
}
