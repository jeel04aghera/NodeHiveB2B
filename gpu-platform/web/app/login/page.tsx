"use client";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useEffect, useState } from "react";
import { useAuth } from "@/lib/auth";
import { ApiError } from "@/lib/api-client";
import { Button, Input, FormField } from "@/components/ui";
import { GoogleButton } from "@/components/GoogleButton";
import { Boxes, Activity, Receipt, ShieldCheck } from "lucide-react";

const OAUTH_ERRORS: Record<string, string> = {
  invalid_state: "Sign-in expired or was interrupted. Please try again.",
  missing_code: "Google did not return an authorization code. Please try again.",
  exchange_failed: "Could not verify your Google sign-in. Please try again.",
  account_error: "We couldn't sign you in with Google. Your email may be unverified.",
};

const CAPABILITIES = [
  { icon: Boxes, title: "Self-serve GPU workloads", desc: "Launch containers with SSH or JupyterLab in seconds." },
  { icon: Activity, title: "Fleet visibility & capacity", desc: "Real-time utilization, allocation, and node health." },
  { icon: Receipt, title: "Chargeback & cost control", desc: "Per-department accounting on every GPU-hour." },
];

export default function LoginPage() {
  const { login, user } = useAuth();
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => { if (user) router.replace(user.onboarded === false ? "/onboarding" : "/overview"); }, [user, router]);
  useEffect(() => {
    const code = new URLSearchParams(window.location.search).get("error");
    if (code) setError(OAUTH_ERRORS[code] ?? "Sign-in failed. Please try again.");
  }, []);
  if (user) return null;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault(); setError(""); setLoading(true);
    try {
      await login(email, password);
      router.replace("/overview");
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) setError("Invalid email or password.");
      else if (err instanceof TypeError) setError("Cannot reach the server. Confirm the control plane is running, then try again.");
      else setError("Sign-in failed. Please try again.");
    } finally { setLoading(false); }
  }


  return (
    <div className="grid min-h-screen lg:grid-cols-2">
      {/* Left — branding */}
      <div className="relative hidden flex-col justify-between bg-neutral-950 p-12 text-neutral-300 lg:flex">
        <div className="flex items-center gap-2.5">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-white text-sm font-bold text-neutral-900">N</div>
          <span className="text-base font-semibold tracking-tight text-white">NodeHive</span>
        </div>

        <div className="max-w-md">
          <h1 className="text-3xl font-semibold leading-tight tracking-tight text-white">
            The private GPU cloud for teams that run real workloads.
          </h1>
          <p className="mt-4 text-sm leading-relaxed text-neutral-400">
            Operate your own GPU fleet with the control, visibility, and accountability of a managed cloud — on hardware you own.
          </p>

          <div className="mt-10 space-y-5">
            {CAPABILITIES.map(({ icon: Icon, title, desc }) => (
              <div key={title} className="flex items-start gap-3.5">
                <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-neutral-800 bg-neutral-900 text-neutral-200">
                  <Icon size={17} />
                </div>
                <div>
                  <div className="text-sm font-medium text-white">{title}</div>
                  <div className="mt-0.5 text-sm text-neutral-400">{desc}</div>
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="flex items-center gap-2 text-xs text-neutral-500">
          <ShieldCheck size={14} /> mTLS · audit logged · role-based access
        </div>
      </div>

      {/* Right — sign in */}
      <div className="flex items-center justify-center bg-canvas p-6">
        <div className="w-full max-w-sm">
          <div className="mb-6 flex items-center gap-2.5 lg:hidden">
            <div className="flex h-8 w-8 items-center justify-center rounded-md bg-ink text-base font-bold text-canvas">N</div>
            <span className="text-lg font-semibold tracking-tight text-ink">NodeHive</span>
          </div>

          <h2 className="text-xl font-semibold tracking-tight text-ink">Sign in to NodeHive</h2>
          <p className="mt-1 text-sm text-ink-muted">Use your organization account to continue.</p>

          <div className="mt-6"><GoogleButton /></div>
          <div className="my-4 flex items-center gap-3 text-xs text-ink-subtle">
            <div className="h-px flex-1 bg-line" />or<div className="h-px flex-1 bg-line" />
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
            <FormField label="Email"><Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@company.com" required autoFocus /></FormField>
            <FormField label="Password"><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="••••••••" required /></FormField>
            {error && <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs leading-relaxed text-red-400">{error}</p>}
            <Button type="submit" variant="primary" className="w-full justify-center" disabled={loading}>{loading ? "Signing in…" : "Sign in"}</Button>
            <p className="text-right text-xs">
              <Link href="/reset-password" className="text-ink-subtle underline-offset-2 hover:text-ink hover:underline">Forgot password?</Link>
            </p>
          </form>

          <p className="mt-6 text-center text-xs text-ink-subtle">
            New to NodeHive?{" "}
            <Link href="/signup" className="font-medium text-ink-muted underline-offset-2 hover:text-ink hover:underline">Create an account</Link>
          </p>
        </div>
      </div>
    </div>
  );
}
