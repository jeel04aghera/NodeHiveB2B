"use client";
import { useState, useMemo } from "react";
import { useChargeback, useWorkloads } from "@/lib/queries";
import { useEnrichedWorkloads } from "@/lib/enrich";
import { formatMoney } from "@/lib/currency";
import {
  PageHeader, Card, StatCard, Table, Row, Cell, Button, EmptyState, Segmented, FormField, Input, Meter,
} from "@/components/ui";
import { Receipt, Download } from "lucide-react";

function isoDate(d: Date) { return d.toISOString().slice(0, 10); }

export default function ChargebackPage() {
  const now = new Date();
  const firstOfMonth = new Date(now.getFullYear(), now.getMonth(), 1);
  const [from, setFrom] = useState(isoDate(firstOfMonth));
  const [to, setTo] = useState(isoDate(now));
  const GROUPS = ["department", "user", "project", "gpu_type"] as const;
  const [groupBy, setGroupBy] = useState<(typeof GROUPS)[number]>("department");

  const fromISO = new Date(from).toISOString();
  const toISO = new Date(to + "T23:59:59Z").toISOString();

  const { data: report, isLoading, error } = useChargeback(fromISO, toISO, groupBy);
  const { data: byDept } = useChargeback(fromISO, toISO, "department");
  const { data: byUser } = useChargeback(fromISO, toISO, "user");

  const { data: workloads } = useWorkloads();
  const enriched = useEnrichedWorkloads((workloads ?? []).map((w) => w.id));

  const label = groupBy === "gpu_type" ? "GPU Type" : groupBy.charAt(0).toUpperCase() + groupBy.slice(1);
  const total = report?.total ?? 0;

  const rangeDays = Math.max(1, Math.round((+new Date(toISO) - +new Date(fromISO)) / 86400_000));
  const forecast = (total / rangeDays) * 30;

  const topDept = useMemo(() => (byDept?.rows ?? []).slice().sort((a, b) => b.amount - a.amount)[0], [byDept]);
  const topUser = useMemo(() => (byUser?.rows ?? []).slice().sort((a, b) => b.amount - a.amount)[0], [byUser]);
  const topWorkload = useMemo(() => {
    let best: { name: string; cost: number } | null = null;
    for (const w of workloads ?? []) {
      const c = enriched[w.id]?.runtime_cost;
      if (c != null && (!best || c > best.cost)) best = { name: w.name, cost: c };
    }
    return best;
  }, [workloads, enriched]);

  function downloadCSV() {
    if (!report?.rows?.length) return;
    const header = `${label},GPU Hours,Utilization %,Amount (INR),Currency`;
    const rows = report.rows.map((r) => `"${r.group_key}",${r.gpu_hours.toFixed(2)},${r.util_pct.toFixed(1)},${(r.amount * 83).toFixed(2)},INR`);
    const csv = [header, ...rows, `"TOTAL",,,${(total * 83).toFixed(2)},INR`].join("\n");
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url; a.download = `chargeback-${groupBy}-${from}-${to}.csv`; a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Chargeback"
        description="Attribute GPU cost to departments, users, projects, and workloads."
        actions={
          <div className="flex items-end gap-3">
            <FormField label="From"><Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="h-9 w-auto" /></FormField>
            <FormField label="To"><Input type="date" value={to} onChange={(e) => setTo(e.target.value)} className="h-9 w-auto" /></FormField>
            {report?.rows?.length ? <Button variant="secondary" onClick={downloadCSV}><Download size={14} /> Export</Button> : null}
          </div>
        }
      />

      {/* Executive summary */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
        <StatCard label="Current Spend" value={formatMoney(total)} hint={`${rangeDays}-day range`} />
        <StatCard label="Forecast" value={formatMoney(forecast)} hint="projected 30 days" />
        <StatCard label="Top Department" value={topDept ? topDept.group_key : "—"} hint={topDept ? formatMoney(topDept.amount) : "no data"} />
        <StatCard label="Top User" value={topUser ? topUser.group_key : "—"} hint={topUser ? formatMoney(topUser.amount) : "no data"} />
        <StatCard label="Most Expensive Workload" value={topWorkload ? topWorkload.name : "—"} hint={topWorkload ? formatMoney(topWorkload.cost) : "no data"} />
      </div>

      <div className="flex items-center justify-between">
        <Segmented
          value={groupBy}
          onChange={setGroupBy}
          options={GROUPS.map((g) => ({ value: g, label: g === "gpu_type" ? "GPU Type" : g.charAt(0).toUpperCase() + g.slice(1) }))}
        />
      </div>

      {error ? (
        <Card><EmptyState icon={<Receipt size={20} />} title="Failed to load chargeback data" /></Card>
      ) : isLoading ? (
        <Card className="p-10 text-center text-sm text-ink-muted">Generating report…</Card>
      ) : (
        <Card className="overflow-hidden">
          {!report?.rows?.length ? (
            <EmptyState icon={<Receipt size={20} />} title="No usage data" description="Usage records are created when workloads stop. Run and stop a workload to populate chargeback." />
          ) : (
            <Table columns={[
              { key: "g", label: label }, { key: "h", label: "GPU Hours", align: "right" },
              { key: "u", label: "Utilization", align: "right" }, { key: "a", label: "Amount", align: "right" }, { key: "s", label: "Share", align: "right" },
            ]}>
              {report.rows.map((row) => {
                const share = total > 0 ? (row.amount / total) * 100 : 0;
                return (
                  <Row key={row.group_key}>
                    <Cell className="font-medium text-ink">{row.group_key}</Cell>
                    <Cell align="right" className="tnum">{row.gpu_hours.toFixed(2)} h</Cell>
                    <Cell align="right" className="tnum">{row.util_pct > 0 ? `${row.util_pct.toFixed(0)}%` : "—"}</Cell>
                    <Cell align="right" className="tnum font-medium text-ink">{formatMoney(row.amount)}</Cell>
                    <Cell align="right">
                      <div className="flex items-center justify-end gap-2"><Meter className="w-16" value={share} /><span className="w-8 text-right text-xs text-ink-muted tnum">{share.toFixed(0)}%</span></div>
                    </Cell>
                  </Row>
                );
              })}
              <tr className="border-t border-line-strong bg-subtle/60">
                <Cell className="font-semibold text-ink">Total</Cell>
                <Cell align="right" /><Cell align="right" />
                <Cell align="right" className="tnum font-semibold text-ink">{formatMoney(total)}</Cell>
                <Cell align="right" />
              </tr>
            </Table>
          )}
        </Card>
      )}
    </div>
  );
}
