"use client";
import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { useAuth, roleLabel } from "@/lib/auth";
import { api, ApiError } from "@/lib/api-client";
import { Button, Input, FormField } from "@/components/ui";

interface InvitePreview { org_name: string; email: string; role: string; status: string }

function InviteContent() {
  const { user, ready, acceptInvitation, registerPending } = useAuth();
  const router = useRouter();
  const search = useSearchParams();
  const token = search.get("token") ?? "";

  const [preview, setPreview] = useState<InvitePreview | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [signup, setSignup] = useState({ name: "", password: "" });

  useEffect(() => {
    if (!token) { setLoadErr("This invite link is missing its token."); return; }
    api<InvitePreview>(`/auth/invitations/${encodeURIComponent(token)}`)
      .then(setPreview)
      .catch((e) => setLoadErr(e instanceof ApiError ? "This invitation is invalid or has expired." : "Could not load the invitation."));
  }, [token]);

  async function accept() {
    setError(""); setBusy(true);
    try {
      await acceptInvitation(token);
      router.replace("/overview");
    } catch (e) {
      setError(e instanceof ApiError && e.status === 403
        ? "This invitation was sent to a different email address."
        : e instanceof ApiError && e.status === 409
        ? "You already belong to an organization."
        : "Could not accept the invitation.");
    } finally { setBusy(false); }
  }

  async function signupAndAccept(e: React.FormEvent) {
    e.preventDefault();
    if (!preview) return;
    setError(""); setBusy(true);
    try {
      await registerPending({ email: preview.email, name: signup.name, password: signup.password });
      await acceptInvitation(token);
      router.replace("/overview");
    } catch (e) {
      setError(e instanceof ApiError && e.status === 409
        ? "An account with this email already exists — sign in instead."
        : "Could not create your account.");
    } finally { setBusy(false); }
  }

  const shell = (children: React.ReactNode) => (
    <div className="flex min-h-screen items-center justify-center bg-canvas p-6">
      <div className="w-full max-w-md">
        <div className="mb-6 flex items-center gap-2.5">
          <div className="flex h-8 w-8 items-center justify-center rounded-md bg-ink text-base font-bold text-canvas">N</div>
          <span className="text-lg font-semibold tracking-tight text-ink">NodeHive</span>
        </div>
        {children}
      </div>
    </div>
  );

  if (loadErr) return shell(<p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{loadErr}</p>);
  if (!preview || !ready) return shell(<p className="text-sm text-ink-muted">Loading invitation…</p>);

  if (preview.status !== "pending") {
    return shell(
      <>
        <h1 className="text-xl font-semibold tracking-tight text-ink">Invitation unavailable</h1>
        <p className="mt-1 text-sm text-ink-muted">This invitation is {preview.status}. Ask an admin of {preview.org_name} to send a new one.</p>
        <Link href="/login" className="mt-5 inline-block text-sm font-medium text-ink hover:underline">Go to sign in →</Link>
      </>
    );
  }

  const header = (
    <>
      <h1 className="text-xl font-semibold tracking-tight text-ink">Join {preview.org_name}</h1>
      <p className="mt-1 text-sm text-ink-muted">
        You've been invited as <span className="font-medium text-ink">{roleLabel(preview.role)}</span> ({preview.email}).
      </p>
    </>
  );

  // Logged in already.
  if (user) {
    if (user.onboarded) {
      return shell(
        <>
          {header}
          <p className="mt-4 rounded-md border border-line bg-subtle px-3 py-2 text-sm text-ink-muted">
            You're already a member of an organization. Leave it first to join {preview.org_name}.
          </p>
        </>
      );
    }
    return shell(
      <>
        {header}
        {error && <p className="mt-4 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</p>}
        <Button variant="primary" className="mt-5 w-full justify-center" onClick={accept} disabled={busy}>
          {busy ? "Joining…" : `Accept & join ${preview.org_name}`}
        </Button>
      </>
    );
  }

  // Not logged in → create an account for the invited email, then accept.
  return shell(
    <>
      {header}
      <form onSubmit={signupAndAccept} className="mt-5 space-y-4">
        <FormField label="Email"><Input value={preview.email} disabled /></FormField>
        <FormField label="Your name"><Input value={signup.name} onChange={(e) => setSignup({ ...signup, name: e.target.value })} placeholder="Jane Smith" autoFocus /></FormField>
        <FormField label="Password" required>
          <Input type="password" value={signup.password} onChange={(e) => setSignup({ ...signup, password: e.target.value })} placeholder="At least 8 characters" required minLength={8} />
        </FormField>
        {error && <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</p>}
        <Button type="submit" variant="primary" className="w-full justify-center" disabled={busy || signup.password.length < 8}>
          {busy ? "Joining…" : "Create account & join"}
        </Button>
      </form>
      <p className="mt-4 text-center text-xs text-ink-subtle">
        Already have an account? <Link href="/login" className="font-medium text-ink hover:underline">Sign in</Link>, then open this link again.
      </p>
    </>
  );
}

export default function InvitePage() {
  return (
    <Suspense fallback={null}>
      <InviteContent />
    </Suspense>
  );
}
