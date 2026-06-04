"use client";
import { useMemo } from "react";
import Link from "next/link";
import { useGPUs, useNodes, useFleetSummary, useUtilization, useWorkloads } from "@/lib/queries";
import {
  PageHeader, StatCard, Card, CardHeader, Table, Row, Cell, Meter, UtilizationChart, EmptyState,
} from "@/components/ui";
import { Activity } from "lucide-react";

export default function CapacityPage() {
  const { data: gpus } = useGPUs();
  const { data: nodes } = useNodes();
  const { data: workloads } = useWorkloads();
  const { data: summary } = useFleetSummary();

  const { from, to } = useMemo(() => {
    const now = Math.floor(Date.now() / 60_000) * 60_000;
    return { from: new Date(now - 24 * 3600_000).toISOString(), to: new Date(now).toISOString() };
  }, []);
  const { data: util } = useUtilization("fleet", undefined, from, to);

  const total = gpus?.length ?? 0;
  const allocated = gpus?.filter((g) => g.status === "in_use").length ?? 0;
  const available = gpus?.filter((g) => g.status === "idle").length ?? 0;
  const allocPct = total > 0 ? Math.round((allocated / total) * 100) : 0;

  const byModel = useMemo(() => {
    const m: Record<string, { total: number; allocated: number }> = {};
    for (const g of gpus ?? []) {
      m[g.model] ??= { total: 0, allocated: 0 };
      m[g.model].total++;
      if (g.status === "in_use") m[g.model].allocated++;
    }
    return Object.entries(m).sort((a, b) => b[1].total - a[1].total);
  }, [gpus]);

  const byNode = useMemo(() => {
    return (nodes ?? []).map((n) => {
      const ng = (gpus ?? []).filter((g) => g.node_id === n.id);
      const alloc = ng.filter((g) => g.status === "in_use").length;
      const running = (workloads ?? []).filter((w) => w.node_id === n.id && w.status === "running").length;
      return { ...n, gpuTotal: ng.length || n.gpu_count, allocated: alloc, running };
    });
  }, [nodes, gpus, workloads]);

  const chartData = util?.map((p) => ({
    time: new Date(p.ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
    util: +p.util_pct.toFixed(1),
  })) ?? [];

  return (
    <div className="space-y-6">
      <PageHeader title="Capacity" description="GPU allocation and utilization across the fleet." />

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard label="Total GPUs" value={total} hint="across all nodes" />
        <StatCard label="Allocated" value={allocated} hint="attached to workloads" />
        <StatCard label="Available" value={available} hint="idle, ready to run" />
        <Card className="px-5 py-4">
          <div className="text-xs font-medium text-ink-muted">Allocation</div>
          <div className="mt-2 text-2xl font-semibold text-ink tnum">{allocPct}%</div>
          <Meter className="mt-3" value={allocPct} tone={allocPct > 85 ? "red" : allocPct > 60 ? "amber" : "green"} />
        </Card>
      </div>

      <Card>
        <CardHeader title="Utilization trend" meta="last 24 hours" actions={<span className="text-xs text-ink-muted tnum">avg {(summary?.avg_util_pct ?? 0).toFixed(1)}%</span>} />
        <div className="p-5">
          {chartData.length === 0 ? (
            <div className="flex h-44 items-center justify-center text-sm text-ink-muted">No telemetry data yet.</div>
          ) : (
            <UtilizationChart data={chartData} height={220} series={[{ key: "util", name: "Utilization", tone: "blue" }]} />
          )}
        </div>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card className="overflow-hidden">
          <CardHeader title="By GPU model" />
          {!byModel.length ? (
            <EmptyState icon={<Activity size={20} />} title="No GPUs in inventory" />
          ) : (
            <Table columns={[{ key: "m", label: "Model" }, { key: "a", label: "Allocated", align: "right" }, { key: "t", label: "Total", align: "right" }, { key: "u", label: "Allocation" }]}>
              {byModel.map(([model, v]) => {
                const pct = v.total > 0 ? Math.round((v.allocated / v.total) * 100) : 0;
                return (
                  <Row key={model}>
                    <Cell className="font-medium text-ink">{model}</Cell>
                    <Cell align="right" className="tnum">{v.allocated}</Cell>
                    <Cell align="right" className="tnum">{v.total}</Cell>
                    <Cell><div className="flex items-center gap-2"><Meter className="w-24" value={pct} tone={pct > 85 ? "red" : "blue"} /><span className="w-9 text-right text-xs text-ink-muted tnum">{pct}%</span></div></Cell>
                  </Row>
                );
              })}
            </Table>
          )}
        </Card>

        <Card className="overflow-hidden">
          <CardHeader title="By node" />
          {!byNode.length ? (
            <EmptyState icon={<Activity size={20} />} title="No nodes enrolled" />
          ) : (
            <Table columns={[{ key: "n", label: "Node" }, { key: "r", label: "Running", align: "right" }, { key: "g", label: "GPUs", align: "right" }, { key: "a", label: "Allocation" }]}>
              {byNode.map((n) => {
                const pct = n.gpuTotal > 0 ? Math.round((n.allocated / n.gpuTotal) * 100) : 0;
                return (
                  <Row key={n.id}>
                    <Cell><Link href={`/nodes/${n.id}`} className="font-mono text-sm font-medium text-ink hover:underline">{n.hostname}</Link></Cell>
                    <Cell align="right" className="tnum">{n.running}</Cell>
                    <Cell align="right" className="tnum">{n.allocated}/{n.gpuTotal}</Cell>
                    <Cell><div className="flex items-center gap-2"><Meter className="w-24" value={pct} tone={pct > 85 ? "red" : "blue"} /><span className="w-9 text-right text-xs text-ink-muted tnum">{pct}%</span></div></Cell>
                  </Row>
                );
              })}
            </Table>
          )}
        </Card>
      </div>
    </div>
  );
}
