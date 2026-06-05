"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { Button, Input, FormField } from "@/components/ui";

// Pre-onboarding landing for a freshly-signed-in Google user with no organization yet.
// Phase 1 supports "Create organization"; "Join with an invite" arrives in Phase 3.
export default function OnboardingPage() {
  const { user, ready, createOrg } = useAuth();
  const router = useRouter();
  const [orgName, setOrgName] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!ready) return;
    if (!user) router.replace("/login");
    else if (user.onboarded) router.replace("/overview");
  }, [ready, user, router]);

  if (!ready || !user || user.onboarded) return null;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(""); setLoading(true);
    try {
      await createOrg(orgName.trim());
      router.replace("/overview");
    } catch {
      setError("Could not create the organization. Please try again.");
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
        <p className="mt-1 text-sm text-ink-muted">Create your organization to start running GPU workloads. You'll be its admin.</p>

        <form onSubmit={submit} className="mt-6 space-y-4">
          <FormField label="Organization name" required>
            <Input value={orgName} onChange={(e) => setOrgName(e.target.value)} placeholder="Acme AI" required autoFocus />
          </FormField>
          {error && <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</p>}
          <Button type="submit" variant="primary" className="w-full justify-center" disabled={loading || !orgName.trim()}>
            {loading ? "Creating…" : "Create organization"}
          </Button>
        </form>

        <p className="mt-6 text-center text-xs text-ink-subtle">
          Have an invite link? Joining an existing organization is coming soon.
        </p>
      </div>
    </div>
  );
}
