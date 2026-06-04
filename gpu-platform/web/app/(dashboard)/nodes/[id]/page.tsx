"use client";
import { useMemo } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { useNodeDetail, useUtilization, type GPU } from "@/lib/queries";
import { ArrowLeft, Cpu, FlaskConical } from "lucide-react";
import {
  Card, CardHeader, Table, Row, Cell, Badge, Dot, EmptyState, UtilizationChart,
  toneFor, NODE_TONE, HEALTH_TONE, GPU_TONE, WORKLOAD_TONE,
} from "@/components/ui";

function fmtMem(mb: number) {
  return mb >= 1024 ? `${(mb / 1024).toFixed(0)} GB` : `${mb} MB`;
}

export default function NodeDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data: node, isLoading, error } = useNodeDetail(id ?? "");

  const { from, to } = useMemo(() => {
    const now = Math.floor(Date.now() / 60_000) * 60_000;
    return { from: new Date(now - 6 * 3600_000).toISOString(), to: new Date(now).toISOString() };
  }, []);
  const { data: util } = useUtilization("node", id, from, to);

  const chartData = util?.map((p) => ({
    time: new Date(p.ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
    util: +p.util_pct.toFixed(1),
  })) ?? [];

  if (isLoading) return <div className="py-8 text-sm text-ink-muted">Loading…</div>;
  if (error || !node) return <div className="py-8 text-sm text-red-400">Node not found.</div>;

  return (
    <div className="space-y-6">
      <Link href="/nodes" className="inline-flex items-center gap-1.5 text-sm text-ink-muted hover:text-ink">
        <ArrowLeft size={14} /> Nodes
      </Link>

      <div className="flex flex-wrap items-center gap-3">
        <Dot tone={toneFor(NODE_TONE, node.status)} />
        <h1 className="font-mono text-xl font-semibold tracking-tight text-ink">{node.hostname}</h1>
        <Badge tone={toneFor(HEALTH_TONE, node.health)}>{node.health}</Badge>
        {node.synthetic && (
          <Badge tone="amber"><FlaskConical size={11} /> Synthetic / Dev</Badge>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
        {[
          { label: "CPU", value: node.cpu_model || "—" },
          { label: "CPU cores", value: node.cpu_cores || "—" },
          { label: "Memory", value: node.ram_mb ? fmtMem(node.ram_mb) : "—" },
          { label: "Storage", value: "Not reported" },
          { label: "GPUs", value: node.gpu_count },
          { label: "OS / kernel", value: `${node.os || "—"}${node.kernel ? ` · ${node.kernel}` : ""}` },
          { label: "Driver / CUDA", value: `${node.nvidia_driver || "—"} / ${node.cuda_version || "—"}` },
          { label: "Agent", value: node.agent_version || "—" },
          { label: "Last heartbeat", value: node.last_seen_at ? new Date(node.last_seen_at).toLocaleString() : "—" },
          { label: "Enrolled", value: node.enrolled_at ? new Date(node.enrolled_at).toLocaleDateString() : "—" },
        ].map(({ label, value }) => (
          <div key={label} className="rounded-lg border border-line bg-surface px-4 py-3 shadow-card">
            <div className="text-xs text-ink-muted">{label}</div>
            <div className="mt-0.5 truncate text-sm font-medium text-ink" title={String(value)}>{String(value)}</div>
          </div>
        ))}
      </div>

      <Card>
        <CardHeader title="Utilization" meta="last 6 hours" actions={node.synthetic ? <span className="text-xs text-amber-400">Synthetic — generated, not measured</span> : undefined} />
        <div className="p-5">
          {chartData.length === 0 ? (
            <div className="flex h-36 items-center justify-center text-sm text-ink-muted">No telemetry data yet.</div>
          ) : (
            <UtilizationChart data={chartData} height={180} series={[{ key: "util", name: "Utilization", tone: "blue" }]} />
          )}
        </div>
      </Card>

      <Card className="overflow-hidden">
        <CardHeader title="Running workloads" />
        {!node.running_workloads?.length ? (
          <div className="py-8 text-center text-sm text-ink-muted">No active workloads on this node.</div>
        ) : (
          <Table columns={[{ key: "n", label: "Name" }, { key: "o", label: "Owner" }, { key: "g", label: "GPUs", align: "right" }, { key: "s", label: "Status" }]}>
            {node.running_workloads.map((w) => (
              <Row key={w.id}>
                <Cell><Link href={`/workloads/${w.id}`} className="font-medium text-ink hover:underline">{w.name}</Link></Cell>
                <Cell className="text-xs">{w.user_email || "—"}</Cell>
                <Cell align="right" className="tnum">{w.gpu_count}</Cell>
                <Cell><Badge tone={toneFor(WORKLOAD_TONE, w.status)} dot>{w.status}</Badge></Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      <Card className="overflow-hidden">
        <CardHeader title="GPUs on this node" />
        {!node.gpus?.length ? (
          <EmptyState icon={<Cpu size={20} />} title="No GPUs found" description="The agent sends inventory on connect." />
        ) : (
          <Table columns={[{ key: "i", label: "#", align: "right" }, { key: "m", label: "Model" }, { key: "mem", label: "Memory", align: "right" }, { key: "s", label: "Status" }, { key: "u", label: "UUID" }]}>
            {node.gpus.map((g: GPU & { synthetic?: boolean }) => (
              <Row key={g.id}>
                <Cell align="right" className="tnum">{g.index}</Cell>
                <Cell>
                  <span className="inline-flex items-center gap-2 font-medium text-ink">
                    {g.model}
                    {g.synthetic && <Badge tone="amber" className="text-[10px]">synthetic</Badge>}
                  </span>
                </Cell>
                <Cell align="right" className="tnum text-ink">{fmtMem(g.memory_mb)}</Cell>
                <Cell><Badge tone={toneFor(GPU_TONE, g.status)} dot>{g.status.replace("_", " ")}</Badge></Cell>
                <Cell className="max-w-[16rem] truncate font-mono text-xs text-ink-subtle">{g.uuid}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>
    </div>
  );
}
