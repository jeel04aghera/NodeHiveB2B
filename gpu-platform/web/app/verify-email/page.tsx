"use client";
// Phase 6 — email verification landing page (link target from the verify email).
import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { Button } from "@/components/ui";
import { MailCheck, MailWarning } from "lucide-react";

function VerifyEmailInner() {
  const token = useSearchParams().get("token") ?? "";
  const [state, setState] = useState<"working" | "ok" | "error">("working");
  const [message, setMessage] = useState("");

  useEffect(() => {
    if (!token) {
      setState("error");
      setMessage("This link is missing its verification token.");
      return;
    }
    api<{ verified: boolean }>("/auth/verify-email/confirm", {
      method: "POST",
      body: JSON.stringify({ token }),
    })
      .then(() => setState("ok"))
      .catch((e: Error) => {
        setState("error");
        setMessage(e.message || "This verification link is invalid or has expired.");
      });
  }, [token]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-canvas px-4">
      <div className="w-full max-w-sm rounded-xl border border-line bg-surface p-8 text-center">
        {state === "working" && <p className="text-sm text-ink-muted">Verifying your email…</p>}
        {state === "ok" && (
          <>
            <MailCheck className="mx-auto mb-3 text-emerald-500" size={28} />
            <h1 className="text-lg font-semibold text-ink">Email verified</h1>
            <p className="mt-2 text-sm text-ink-muted">Your email address is confirmed. You're all set.</p>
            <Link href="/overview"><Button className="mt-6 w-full justify-center">Go to dashboard</Button></Link>
          </>
        )}
        {state === "error" && (
          <>
            <MailWarning className="mx-auto mb-3 text-amber-500" size={28} />
            <h1 className="text-lg font-semibold text-ink">Verification failed</h1>
            <p className="mt-2 text-sm text-ink-muted">{message}</p>
            <p className="mt-2 text-xs text-ink-subtle">Sign in and use “Resend verification” to get a fresh link.</p>
            <Link href="/login"><Button variant="secondary" className="mt-6 w-full justify-center">Back to sign in</Button></Link>
          </>
        )}
      </div>
    </div>
  );
}

export default function VerifyEmailPage() {
  return (
    <Suspense fallback={<div className="min-h-screen bg-canvas" />}>
      <VerifyEmailInner />
    </Suspense>
  );
}
