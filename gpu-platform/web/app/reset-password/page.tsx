"use client";
// Phase 6 — password reset: request form (no token) or new-password form (?token=).
import { Suspense, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { Button, FormField, Input } from "@/components/ui";
import { KeyRound, MailCheck } from "lucide-react";

function RequestForm() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api("/auth/password-reset/request", {
        method: "POST",
        body: JSON.stringify({ email }),
      });
    } finally {
      // Always show the same outcome — the API never reveals whether the email exists.
      setSent(true);
      setBusy(false);
    }
  };

  if (sent) {
    return (
      <div className="text-center">
        <MailCheck className="mx-auto mb-3 text-emerald-500" size={28} />
        <h1 className="text-lg font-semibold text-ink">Check your email</h1>
        <p className="mt-2 text-sm text-ink-muted">
          If an account exists for <span className="text-ink">{email}</span>, a reset link is on its way.
          It expires in 1 hour.
        </p>
        <Link href="/login"><Button variant="secondary" className="mt-6 w-full justify-center">Back to sign in</Button></Link>
      </div>
    );
  }
  return (
    <>
      <h1 className="text-lg font-semibold text-ink">Reset your password</h1>
      <p className="mt-1 text-sm text-ink-muted">Enter your account email and we'll send a reset link.</p>
      <form onSubmit={submit} className="mt-6 space-y-4">
        <FormField label="Email">
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@company.com" required autoFocus />
        </FormField>
        <Button type="submit" className="w-full justify-center" disabled={busy || !email}>
          {busy ? "Sending…" : "Send reset link"}
        </Button>
        <p className="text-center text-xs text-ink-subtle">
          <Link href="/login" className="hover:text-ink hover:underline">Back to sign in</Link>
        </p>
      </form>
    </>
  );
}

function ConfirmForm({ token }: { token: string }) {
  const router = useRouter();
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (pw.length < 8) {
      setError("Password must be at least 8 characters.");
      return;
    }
    if (pw !== pw2) {
      setError("Passwords don't match.");
      return;
    }
    setBusy(true);
    try {
      await api("/auth/password-reset/confirm", {
        method: "POST",
        body: JSON.stringify({ token, new_password: pw }),
      });
      setDone(true);
      setTimeout(() => router.replace("/login"), 1800);
    } catch (err) {
      setError(err instanceof Error ? err.message : "This reset link is invalid or has expired.");
    } finally {
      setBusy(false);
    }
  };

  if (done) {
    return (
      <div className="text-center">
        <KeyRound className="mx-auto mb-3 text-emerald-500" size={28} />
        <h1 className="text-lg font-semibold text-ink">Password updated</h1>
        <p className="mt-2 text-sm text-ink-muted">
          All other sessions were signed out. Redirecting you to sign in…
        </p>
      </div>
    );
  }
  return (
    <>
      <h1 className="text-lg font-semibold text-ink">Choose a new password</h1>
      <p className="mt-1 text-sm text-ink-muted">This signs out every other device.</p>
      <form onSubmit={submit} className="mt-6 space-y-4">
        <FormField label="New password">
          <Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder="At least 8 characters" required autoFocus />
        </FormField>
        <FormField label="Confirm password">
          <Input type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} required />
        </FormField>
        {error && <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</p>}
        <Button type="submit" className="w-full justify-center" disabled={busy}>
          {busy ? "Saving…" : "Set new password"}
        </Button>
      </form>
    </>
  );
}

function ResetPasswordInner() {
  const token = useSearchParams().get("token");
  return (
    <div className="flex min-h-screen items-center justify-center bg-canvas px-4">
      <div className="w-full max-w-sm rounded-xl border border-line bg-surface p-8">
        {token ? <ConfirmForm token={token} /> : <RequestForm />}
      </div>
    </div>
  );
}

export default function ResetPasswordPage() {
  return (
    <Suspense fallback={<div className="min-h-screen bg-canvas" />}>
      <ResetPasswordInner />
    </Suspense>
  );
}
