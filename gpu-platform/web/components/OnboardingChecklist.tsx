"use client";
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Check, Circle, X, ArrowRight, Layers, Server, Boxes, Wallet } from "lucide-react";
import { Card, Meter } from "@/components/ui";

const STARTED_KEY = "nh_onboarding_started";
const DISMISSED_KEY = "nh_onboarding_dismissed";
const BILLING_KEY = "nh_onboard_billing";

/** Mark a step complete from elsewhere (e.g. the Billing page on mount). */
export function markOnboardingBillingReviewed() {
  try { localStorage.setItem(BILLING_KEY, "1"); } catch {}
}

export function OnboardingChecklist({
  projects, nodes, workloads,
}: { projects: number; nodes: number; workloads: number }) {
  const [billingReviewed, setBillingReviewed] = useState(false);
  const [started, setStarted] = useState(false);
  const [dismissed, setDismissed] = useState(true); // hidden until we read storage

  useEffect(() => {
    setBillingReviewed(localStorage.getItem(BILLING_KEY) === "1");
    setDismissed(localStorage.getItem(DISMISSED_KEY) === "1");
    const firstRun = projects === 0 && nodes === 0 && workloads === 0;
    const wasStarted = localStorage.getItem(STARTED_KEY) === "1";
    if (firstRun && !wasStarted) { localStorage.setItem(STARTED_KEY, "1"); setStarted(true); }
    else setStarted(wasStarted || firstRun);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projects, nodes, workloads]);

  const steps = useMemo(() => [
    { key: "project", label: "Create a project", desc: "Organize workloads, spend, and ownership.", done: projects > 0, href: "/projects", icon: Layers },
    { key: "node", label: "Connect your first node", desc: "Install the agent on a GPU server.", done: nodes > 0, href: "/settings?tab=enrollment", icon: Server },
    { key: "workload", label: "Launch a workload", desc: "Deploy from a template in seconds.", done: workloads > 0, href: "/workloads?launch=1", icon: Boxes },
    { key: "billing", label: "Review billing", desc: "See spend, forecast, and credits.", done: billingReviewed, href: "/billing", icon: Wallet },
  ], [projects, nodes, workloads, billingReviewed]);

  const doneCount = steps.filter((s) => s.done).length;
  const allDone = doneCount === steps.length;

  if (dismissed || allDone || !started) return null;

  function dismiss() { try { localStorage.setItem(DISMISSED_KEY, "1"); } catch {}; setDismissed(true); }

  return (
    <Card className="overflow-hidden">
      <div className="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
        <div>
          <h2 className="text-sm font-semibold text-ink">Finish setting up NodeHive</h2>
          <p className="mt-0.5 text-xs text-ink-muted">A few steps to get your private GPU cloud running.</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="hidden items-center gap-2 sm:flex">
            <Meter className="w-24" value={(doneCount / steps.length) * 100} tone="green" />
            <span className="text-xs text-ink-muted tnum">{doneCount}/{steps.length}</span>
          </div>
          <button onClick={dismiss} title="Dismiss" className="rounded p-1 text-ink-subtle transition-colors hover:bg-subtle hover:text-ink"><X size={15} /></button>
        </div>
      </div>
      <ul className="divide-y divide-line">
        {steps.map((s) => {
          const Icon = s.icon;
          return (
            <li key={s.key}>
              <Link href={s.href} className="flex items-center gap-3 px-5 py-3 transition-colors hover:bg-subtle/70">
                <span className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full ${s.done ? "bg-emerald-500 text-white" : "border border-line bg-surface text-ink-subtle"}`}>
                  {s.done ? <Check size={13} /> : <Circle size={9} className="fill-current" />}
                </span>
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-line bg-subtle text-ink-muted"><Icon size={15} /></span>
                <span className="min-w-0 flex-1">
                  <span className={`block text-sm font-medium ${s.done ? "text-ink-muted line-through" : "text-ink"}`}>{s.label}</span>
                  <span className="block text-xs text-ink-subtle">{s.desc}</span>
                </span>
                {!s.done && <ArrowRight size={15} className="shrink-0 text-ink-subtle" />}
              </Link>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}
