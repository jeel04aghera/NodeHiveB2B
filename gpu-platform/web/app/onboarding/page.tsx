"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Button, Input, FormField } from "@/components/ui";

// Pre-onboarding landing for a freshly-signed-in user with no organization yet: create a
// new org (become owner) or join an existing one with a code. Email invites use /invite.
export default function OnboardingPage() {
  const { user, ready, createOrg, joinWithCode } = useAuth();
  const router = useRouter();
  const [mode, setMode] = useState<"create" | "join">("create");
  const [orgName, setOrgName] = useState("");
  const [code, setCode] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!ready) return;
    if (!user) router.replace("/login");
    else if (user.onboarded) router.replace("/overview");
  }, [ready, user, router]);

  if (!ready || !user || user.onboarded) return null;

  async function submitCreate(e: React.FormEvent) {
    e.preventDefault();
    setError(""); setLoading(true);
    try {
      await createOrg(orgName.trim());
      router.replace("/overview");
    } catch {
      setError("Could not create the organization. Please try again.");
    } finally { setLoading(false); }
  }
  async function submitJoin(e: React.FormEvent) {
    e.preventDefault();
    setError(""); setLoading(true);
    try {
      await joinWithCode(code.trim());
      router.replace("/overview");
    } catch (err) {
      setError(err instanceof Error && err.message ? "That join code is invalid, expired, or already used." : "Could not join.");
    } finally { setLoading(false); }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-canvas p-6">
      <div className="w-full max-w-md">
        <div className="mb-6 flex items-center gap-2.5">
          <div className="flex h-8 w-8 items-center justify-center rounded-md bg-ink text-base font-bold text-canvas">N</div>
          <span className="text-lg font-semibold tracking-tight text-ink">NodeHive</span>
        </div>
        <h1 className="text-xl font-semibold tracking-tight text-ink">Welcome, {user.name || user.email} 👋</h1>
        <p className="mt-1 text-sm text-ink-muted">Create a new organization, or join an existing one with a code.</p>

        <div className="mt-5 flex gap-1 rounded-lg border border-line bg-subtle p-1 text-sm">
          <button onClick={() => { setMode("create"); setError(""); }}
            className={`flex-1 rounded-md px-3 py-1.5 font-medium ${mode === "create" ? "bg-surface text-ink shadow-sm" : "text-ink-muted"}`}>Create</button>
          <button onClick={() => { setMode("join"); setError(""); }}
            className={`flex-1 rounded-md px-3 py-1.5 font-medium ${mode === "join" ? "bg-surface text-ink shadow-sm" : "text-ink-muted"}`}>Join with code</button>
        </div>

        {mode === "create" ? (
          <form onSubmit={submitCreate} className="mt-5 space-y-4">
            <FormField label="Organization name" required>
              <Input value={orgName} onChange={(e) => setOrgName(e.target.value)} placeholder="Acme AI" required autoFocus />
            </FormField>
            {error && <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</p>}
            <Button type="submit" variant="primary" className="w-full justify-center" disabled={loading || !orgName.trim()}>
              {loading ? "Creating…" : "Create organization"}
            </Button>
            <p className="text-xs text-ink-subtle">You'll be the owner of this organization.</p>
          </form>
        ) : (
          <form onSubmit={submitJoin} className="mt-5 space-y-4">
            <FormField label="Join code" required>
              <Input value={code} onChange={(e) => setCode(e.target.value)} placeholder="Paste your join code" required autoFocus />
            </FormField>
            {error && <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</p>}
            <Button type="submit" variant="primary" className="w-full justify-center" disabled={loading || !code.trim()}>
              {loading ? "Joining…" : "Join organization"}
            </Button>
          </form>
        )}

        <p className="mt-6 text-center text-xs text-ink-subtle">
          Got an email invite link? Open it to accept and join automatically.
        </p>
      </div>
    </div>
  );
}
