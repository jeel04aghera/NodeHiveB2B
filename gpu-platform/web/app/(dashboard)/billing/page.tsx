"use client";
import { useState, useMemo } from "react";
import Link from "next/link";
import { useChargeback, useFleetSummary, useCredits, useLedger, useTopUp, type LedgerEntry } from "@/lib/queries";
import { formatRupees } from "@/lib/currency";
import { PageHeader, Card, CardHeader, StatCard, Table, Row, Cell, Button, Meter, Badge } from "@/components/ui";
import { Wallet, Plus, Check, ArrowDownRight, ArrowUpRight } from "lucide-react";

const PACKS = [5000, 10000, 25000, 50000];

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
  const [added, setAdded] = useState<number | null>(null);
  const { data: summary } = useFleetSummary();

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
    if (amount <= 0 || topUp.isPending) return;
    topUp.mutate({ amount, description: "Credit top-up" }, {
      onSuccess: () => { setAdded(amount); setCustom(""); setTimeout(() => setAdded(null), 2500); },
    });
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
        <CardHeader title="Top up credits" meta="instant" />
        <div className="p-5">
          <div className="flex flex-wrap items-center gap-2.5">
            {PACKS.map((p) => (
              <button key={p} onClick={() => doTopUp(p)} disabled={topUp.isPending} className="rounded-md border border-line bg-surface px-4 py-2.5 text-sm font-medium text-ink transition-colors hover:border-line-strong hover:bg-subtle disabled:opacity-50">{formatRupees(p)}</button>
            ))}
            <div className="flex items-center gap-2">
              <span className="text-sm text-ink-subtle">₹</span>
              <input type="number" min={1} value={custom} onChange={(e) => setCustom(e.target.value)} placeholder="Custom" className="h-10 w-28 rounded-md border border-line bg-surface px-3 text-sm text-ink placeholder:text-ink-subtle focus:border-line-strong focus:outline-none focus:ring-2 focus:ring-ink/10" />
              <Button variant="primary" onClick={() => doTopUp(parseInt(custom))} disabled={!custom || parseInt(custom) <= 0 || topUp.isPending}><Plus size={14} /> Add</Button>
            </div>
            {added != null && <span className="inline-flex items-center gap-1.5 text-sm font-medium text-emerald-400"><Check size={15} /> Added {formatRupees(added)}</span>}
          </div>
          <p className="mt-3 text-xs text-ink-subtle">Credits are consumed as workloads run — you’re charged for actual GPU-hours used.</p>
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
