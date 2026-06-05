"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useAuth } from "@/lib/auth";
import { ApiError } from "@/lib/api-client";
import { saveOrgProfile } from "@/lib/org";
import { Button, Input, FormField, Select } from "@/components/ui";
import { GoogleButton } from "@/components/GoogleButton";
import { Check, ArrowRight, Boxes, Activity, Receipt } from "lucide-react";

const STEPS = ["Create account", "Create organization", "Enter dashboard"];
const SIZES = ["1–10", "11–50", "51–200", "201–1000", "1000+"];
const USE_CASES = ["AI Training", "Inference", "Data Science", "Research", "Enterprise GPU Sharing"];

function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    try {
      const parsed = JSON.parse(err.message);
      if (parsed?.error?.message) return parsed.error.message as string;
    } catch { /* not JSON */ }
    if (err.status === 409) return "An account with that email already exists. Try signing in instead.";
    return "Sign-up failed. Please check your details and try again.";
  }
  if (err instanceof TypeError) return "Cannot reach the server. Confirm the control plane is running.";
  return "Sign-up failed. Please try again.";
}

export default function SignupPage() {
  const { register, user } = useAuth();
  const router = useRouter();
  const [step, setStep] = useState(1);
  const [account, setAccount] = useState({ name: "", email: "", password: "" });
  const [org, setOrg] = useState({ name: "", size: SIZES[1], useCase: USE_CASES[0] });
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => { if (user) router.replace("/overview"); }, [user, router]);

  function next1(e: React.FormEvent) {
    e.preventDefault();
    if (!account.name || !account.email || !account.password) { setError("Fill in all fields."); return; }
    if (account.password.length < 8) { setError("Password must be at least 8 characters."); return; }
    setError(""); setStep(2);
  }

  async function createOrg(e: React.FormEvent) {
    e.preventDefault();
    if (!org.name) { setError("Organization name is required."); return; }
    setError(""); setLoading(true);
    try {
      await register({ orgName: org.name, name: account.name, email: account.email, password: account.password });
      saveOrgProfile({ name: org.name, size: org.size, useCase: org.useCase });
      setStep(3);
    } catch (err) {
      setError(errorMessage(err));
    } finally { setLoading(false); }
  }

  return (
    <div className="grid min-h-screen lg:grid-cols-[420px_1fr]">
      {/* Left rail */}
      <div className="hidden flex-col justify-between bg-neutral-950 p-10 text-neutral-300 lg:flex">
        <Link href="/" className="flex items-center gap-2.5">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-white text-sm font-bold text-neutral-900">N</div>
          <span className="text-base font-semibold tracking-tight text-white">NodeHive</span>
        </Link>

        <div>
          <h1 className="text-2xl font-semibold leading-tight tracking-tight text-white">Set up your private GPU cloud</h1>
          <ol className="mt-8 space-y-4">
            {STEPS.map((label, i) => {
              const n = i + 1, done = step > n, active = step === n;
              return (
                <li key={label} className="flex items-center gap-3">
                  <span className={`flex h-7 w-7 items-center justify-center rounded-full border text-xs font-semibold ${done ? "border-white bg-white text-neutral-900" : active ? "border-white text-white" : "border-neutral-700 text-neutral-500"}`}>
                    {done ? <Check size={14} /> : n}
                  </span>
                  <span className={done || active ? "text-sm font-medium text-white" : "text-sm text-neutral-500"}>{label}</span>
                </li>
              );
            })}
          </ol>
        </div>

        <div className="space-y-2.5 text-xs text-neutral-500">
          <div className="flex items-center gap-2"><Boxes size={14} /> Self-service GPU workloads</div>
          <div className="flex items-center gap-2"><Activity size={14} /> Fleet capacity & utilization</div>
          <div className="flex items-center gap-2"><Receipt size={14} /> Department chargeback</div>
        </div>
      </div>

      {/* Right — step content */}
      <div className="flex items-center justify-center bg-canvas p-6">
        <div className="w-full max-w-sm">
          <p className="text-xs font-medium uppercase tracking-wider text-ink-subtle">Step {step} of 3</p>

          {step === 1 && (
            <form onSubmit={next1} className="mt-2 space-y-4">
              <div>
                <h2 className="text-xl font-semibold tracking-tight text-ink">Create your account</h2>
                <p className="mt-1 text-sm text-ink-muted">Start running GPU workloads in minutes.</p>
              </div>
              <GoogleButton label="Sign up with Google" />
              <div className="flex items-center gap-3 text-xs text-ink-subtle">
                <div className="h-px flex-1 bg-line" />or<div className="h-px flex-1 bg-line" />
              </div>
              <FormField label="Full name"><Input value={account.name} onChange={(e) => setAccount({ ...account, name: e.target.value })} placeholder="Jane Smith" required autoFocus /></FormField>
              <FormField label="Work email"><Input type="email" value={account.email} onChange={(e) => setAccount({ ...account, email: e.target.value })} placeholder="you@company.com" required /></FormField>
              <FormField label="Password"><Input type="password" value={account.password} onChange={(e) => setAccount({ ...account, password: e.target.value })} placeholder="••••••••" required /></FormField>
              {error && <p className="text-sm text-red-400">{error}</p>}
              <Button type="submit" variant="primary" className="w-full justify-center">Continue <ArrowRight size={15} /></Button>
              <p className="text-center text-xs text-ink-subtle">Already have an account? <Link href="/login" className="font-medium text-ink hover:underline">Sign in</Link></p>
            </form>
          )}

          {step === 2 && (
            <form onSubmit={createOrg} className="mt-2 space-y-4">
              <div>
                <h2 className="text-xl font-semibold tracking-tight text-ink">Create your organization</h2>
                <p className="mt-1 text-sm text-ink-muted">This is where your team’s GPUs, projects, and spend live.</p>
              </div>
              <FormField label="Organization name"><Input value={org.name} onChange={(e) => setOrg({ ...org, name: e.target.value })} placeholder="Acme AI" required autoFocus /></FormField>
              <FormField label="Company size"><Select value={org.size} onChange={(e) => setOrg({ ...org, size: e.target.value })}>{SIZES.map((s) => <option key={s} value={s}>{s} employees</option>)}</Select></FormField>
              <FormField label="Primary use case"><Select value={org.useCase} onChange={(e) => setOrg({ ...org, useCase: e.target.value })}>{USE_CASES.map((u) => <option key={u} value={u}>{u}</option>)}</Select></FormField>
              {error && <p className="text-sm text-red-400">{error}</p>}
              <div className="flex gap-3 pt-1">
                <Button type="button" variant="secondary" className="justify-center" onClick={() => { setError(""); setStep(1); }}>Back</Button>
                <Button type="submit" variant="primary" className="flex-1 justify-center" disabled={loading}>{loading ? "Creating…" : "Create organization"}</Button>
              </div>
            </form>
          )}

          {step === 3 && (
            <div className="mt-2 space-y-5 text-center">
              <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full border border-emerald-500/20 bg-emerald-500/10 text-emerald-400"><Check size={24} /></div>
              <div>
                <h2 className="text-xl font-semibold tracking-tight text-ink">You’re all set</h2>
                <p className="mt-1 text-sm text-ink-muted"><span className="font-medium text-ink">{org.name}</span> is ready. Launch your first GPU workload now.</p>
              </div>
              <Button variant="primary" className="w-full justify-center" onClick={() => router.replace("/overview")}>Enter Dashboard <ArrowRight size={15} /></Button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
