"use client";
import { useState, useMemo } from "react";
import Link from "next/link";
import { useChargeback, useFleetSummary, useCredits, useLedger, useTopUp, useDeploymentConfig, type LedgerEntry } from "@/lib/queries";
import { ApiError } from "@/lib/api-client";
import { formatRupees } from "@/lib/currency";
import { PageHeader, Card, CardHeader, StatCard, Table, Row, Cell, Button, Meter, Badge } from "@/components/ui";
import { Wallet, Plus, Check, ArrowDownRight, ArrowUpRight, Lock, AlertCircle } from "lucide-react";

const PACKS = [5000, 10000, 25000, 50000];
// Mirrors the server's bound in topupCredits (the ledger column is numeric(14,4)).
// Client-side validation is for fast feedback only — the API re-checks everything.
const MAX_TOPUP = 1_000_000_000;

// parseAmount rejects what the old parseInt quietly accepted: "12abc" became 12 and
// "12.5" became 12, so the user was charged an amount they never typed.
function parseAmount(raw: string): { value: number } | { error: string } {
  const trimmed = raw.trim();
  if (!trimmed) return { error: "Enter an amount." };
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return { error: "Enter a whole number, or an amount with up to 2 decimal places." };
  const value = Number(trimmed);
  if (!Number.isFinite(value) || value <= 0) return { error: "Amount must be greater than zero." };
  if (value > MAX_TOPUP) return { error: `Amount cannot exceed ${formatRupees(MAX_TOPUP)}.` };
  return { value };
}

// Same shape as the auth pages: the control plane answers errors as
// {"error":{"code","message"}} and ApiError carries that body verbatim.
function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    try {
      const parsed = JSON.parse(err.message);
      if (parsed?.error?.message) return parsed.error.message as string;
    } catch { /* not JSON */ }
    if (err.status === 403) return "You do not have permission to add credits to this organization.";
    return "Could not add credits. Please try again.";
  }
  if (err instanceof TypeError) return "Cannot reach the server. Confirm the control plane is running.";
  return "Could not add credits. Please try again.";
}

function kindMeta(kind: LedgerEntry["kind"]) {
  switch (kind) {
    case "charge": return { tone: "neutral" as const, label: "Usage", icon: ArrowDownRight };
    case "topup": return { tone: "green" as const, label: "Top-up", icon: ArrowUpRight };
    case "grant": return { tone: "blue" as const, label: "Grant", icon: ArrowUpRight };
    default: return { tone: "amber" as const, label: "Adjustment", icon: ArrowUpRight };
  }
}

export default function BillingPage() {
  const { data: credits } = useCredits();
  const { data: ledger } = useLedger(20);
  const topUp = useTopUp();
  const [custom, setCustom] = useState("");
  const [added, setAdded] = useState<{ amount: number; balance: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { data: summary } = useFleetSummary();
  const { data: cfg } = useDeploymentConfig();
  // Undefined while /config is in flight — treat as "not yet known" rather than
  // "disabled" so the panel doesn't flash the disabled state on every load.
  const topUpEnabled = cfg?.self_topup_enabled;

  const { from, to, daysElapsed, daysInMonth } = useMemo(() => {
    const now = new Date();
    const first = new Date(now.getFullYear(), now.getMonth(), 1);
    const dim = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate();
    return { from: first.toISOString(), to: now.toISOString(), daysElapsed: now.getDate(), daysInMonth: dim };
  }, []);
  const { data: byProject } = useChargeback(from, to, "project");

  const balance = credits?.balance ?? 0;
  const monthSpend = credits?.month_spent ?? 0;
  const forecast = daysElapsed > 0 ? (monthSpend / daysElapsed) * daysInMonth : 0;
  const topProjects = useMemo(() => (byProject?.rows ?? []).slice().sort((a, b) => b.amount - a.amount).slice(0, 5), [byProject]);
  const projectTotalInr = (byProject?.total ?? 0) * 83;

  function doTopUp(amount: number) {
    if (!Number.isFinite(amount) || amount <= 0 || topUp.isPending) return;
    setError(null);
    topUp.mutate({ amount, description: "Credit top-up" }, {
      // The mutation resolves with the authoritative post-write balance, so the
      // confirmation reports what the ledger actually holds rather than a local sum.
      // useTopUp also invalidates the credits + ledger queries, which refreshes the
      // balance card and the recent-activity table.
      onSuccess: (res) => { setAdded({ amount, balance: res.balance }); setCustom(""); },
      // Without this the rejected mutation was swallowed and the click looked like a no-op.
      onError: (e) => { setAdded(null); setError(errorMessage(e)); },
    });
  }

  // Submit the custom field, surfacing the same validation the API enforces.
  function submitCustom() {
    const parsed = parseAmount(custom);
    if ("error" in parsed) { setAdded(null); setError(parsed.error); return; }
    doTopUp(parsed.value);
  }

  return (
    <div className="space-y-6">
      <PageHeader title="Billing" description="Prepaid credits and usage spend for your organization, in INR." />

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card className="px-5 py-4">
          <div className="flex items-center justify-between"><span className="text-xs font-medium text-ink-muted">Credit Balance</span><Wallet size={15} className="text-ink-subtle" /></div>
          <div className="mt-2 text-2xl font-semibold text-ink tnum">{formatRupees(balance)}</div>
          <div className="mt-1 text-xs text-ink-subtle">available to spend</div>
        </Card>
        <StatCard label="This Month's Usage" value={formatRupees(monthSpend)} hint="charged to date" />
        <StatCard label="Forecast" value={formatRupees(forecast)} hint="projected full month" />
        <StatCard label="Active Workloads" value={summary?.workloads_active ?? 0} hint="currently running" />
      </div>

      <Card>
        <CardHeader title="Top up credits" meta={topUpEnabled === false ? "unavailable" : "instant"} />
        <div className="p-5">
          {topUpEnabled === false ? (
            // No payment provider is wired up on this deployment, so the endpoint
            // answers 403 by design. Say so plainly rather than offering buttons
            // that cannot work.
            <div className="flex items-start gap-2.5">
              <Lock size={15} className="mt-0.5 shrink-0 text-ink-subtle" />
              <div>
                <p className="text-sm text-ink">Self-serve top-ups are not available on this deployment.</p>
                <p className="mt-1 text-xs text-ink-subtle">No payment provider is configured. Contact your provider to have credits added to your organization.</p>
              </div>
            </div>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2.5">
                {PACKS.map((p) => (
                  <button key={p} onClick={() => doTopUp(p)} disabled={topUp.isPending} className="rounded-md border border-line bg-surface px-4 py-2.5 text-sm font-medium text-ink transition-colors hover:border-line-strong hover:bg-subtle disabled:opacity-50">{formatRupees(p)}</button>
                ))}
                <form
                  className="flex items-center gap-2"
                  // noValidate: with min={1} the browser silently refuses to submit an
                  // out-of-range value, so submitCustom never runs and the user gets no
                  // feedback at all. Our own check owns validation and always renders a
                  // message; min/max/step stay for the spinner and mobile keypad.
                  noValidate
                  onSubmit={(e) => { e.preventDefault(); submitCustom(); }}
                >
                  <span className="text-sm text-ink-subtle">₹</span>
                  <input
                    type="number" min={1} max={MAX_TOPUP} step="0.01" inputMode="decimal"
                    value={custom}
                    onChange={(e) => { setCustom(e.target.value); setError(null); setAdded(null); }}
                    placeholder="Custom"
                    disabled={topUp.isPending}
                    aria-label="Custom credit amount"
                    aria-invalid={!!error}
                    className="h-10 w-28 rounded-md border border-line bg-surface px-3 text-sm text-ink placeholder:text-ink-subtle focus:border-line-strong focus:outline-none focus:ring-2 focus:ring-ink/10 disabled:opacity-50"
                  />
                  <Button type="submit" variant="primary" disabled={!custom.trim() || topUp.isPending}>
                    <Plus size={14} /> {topUp.isPending ? "Adding…" : "Add"}
                  </Button>
                </form>
                {topUp.isPending && <span className="text-sm text-ink-subtle">Adding credits…</span>}
                {added && !topUp.isPending && (
                  <span className="inline-flex items-center gap-1.5 text-sm font-medium text-emerald-400">
                    <Check size={15} /> Added {formatRupees(added.amount)} · new balance {formatRupees(added.balance)}
                  </span>
                )}
              </div>
              {error && (
                <p role="alert" className="mt-3 inline-flex items-start gap-1.5 text-xs text-red-400">
                  <AlertCircle size={14} className="mt-px shrink-0" /> {error}
                </p>
              )}
              <p className="mt-3 text-xs text-ink-subtle">Credits are consumed as workloads run — you’re charged for actual GPU-hours used.</p>
            </>
          )}
        </div>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card className="overflow-hidden">
          <CardHeader title="Spend by Project" meta="this month" actions={<Link href="/projects" className="text-xs text-ink-muted hover:text-ink">All projects →</Link>} />
          {!topProjects.length ? (
            <div className="px-5 py-10 text-center text-sm text-ink-muted">No project spend yet.</div>
          ) : (
            <Table columns={[{ key: "p", label: "Project" }, { key: "h", label: "GPU-hrs", align: "right" }, { key: "a", label: "Amount", align: "right" }, { key: "s", label: "Share" }]}>
              {topProjects.map((r) => {
                const inr = r.amount * 83;
                const share = projectTotalInr > 0 ? (inr / projectTotalInr) * 100 : 0;
                return (
                  <Row key={r.group_key}>
                    <Cell className="font-medium text-ink">{r.group_key}</Cell>
                    <Cell align="right" className="tnum">{r.gpu_hours.toFixed(2)}</Cell>
                    <Cell align="right" className="tnum font-medium text-ink">{formatRupees(inr)}</Cell>
                    <Cell><div className="flex items-center gap-2"><Meter className="w-20" value={share} /><span className="w-9 text-right text-xs text-ink-muted tnum">{share.toFixed(0)}%</span></div></Cell>
                  </Row>
                );
              })}
            </Table>
          )}
        </Card>

        <Card className="overflow-hidden">
          <CardHeader title="Credit Activity" meta="recent" />
          {!ledger?.length ? (
            <div className="px-5 py-10 text-center text-sm text-ink-muted">No activity yet.</div>
          ) : (
            <ul className="divide-y divide-line">
              {ledger.map((e) => {
                const m = kindMeta(e.kind);
                const Icon = m.icon;
                const positive = e.delta >= 0;
                return (
                  <li key={e.id} className="flex items-center justify-between gap-3 px-5 py-2.5">
                    <span className="flex min-w-0 items-center gap-2.5">
                      <Icon size={14} className={positive ? "text-emerald-400" : "text-ink-subtle"} />
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-medium text-ink">{e.description}</span>
                        <span className="text-xs text-ink-subtle">{new Date(e.created_at).toLocaleDateString()}</span>
                      </span>
                    </span>
                    <span className="flex shrink-0 items-center gap-3">
                      <Badge tone={m.tone}>{m.label}</Badge>
                      <span className={`w-24 text-right text-sm font-medium tnum ${positive ? "text-emerald-400" : "text-ink"}`}>{positive ? "+" : "−"}{formatRupees(Math.abs(e.delta))}</span>
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}
