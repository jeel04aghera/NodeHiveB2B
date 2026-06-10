"use client";
import { useMemo } from "react";
import Link from "next/link";
import {
  useFleetSummary, useWorkloads, useNodes, useChargeback, useAuditLogs, useDepartments, useProjects,
} from "@/lib/queries";
import { formatMoney } from "@/lib/currency";
import {
  PageHeader, StatCard, Card, CardHeader, Button, Badge, Meter, EmptyState,
  SkeletonStats, toneFor, WORKLOAD_TONE, AUDIT_TONE,
} from "@/components/ui";
import { OnboardingChecklist } from "@/components/OnboardingChecklist";
import { Zap, Boxes, AlertTriangle, ArrowRight, Server } from "lucide-react";

function ago(iso?: string | null) {
  if (!iso) return "—";
  const d = Date.now() - new Date(iso).getTime();
  if (d < 60_000) return "just now";
  if (d < 3600_000) return `${Math.floor(d / 60_000)}m ago`;
  if (d < 86400_000) return `${Math.floor(d / 3600_000)}h ago`;
  return `${Math.floor(d / 86400_000)}d ago`;
}

export default function OverviewPage() {
  const { data: summary } = useFleetSummary();
  const { data: workloads } = useWorkloads();
  const { data: nodes } = useNodes();
  const { data: departments } = useDepartments();
  const { data: projects } = useProjects();
  const firstLoad = summary === undefined && workloads === undefined && nodes === undefined;

  const { from, to } = useMemo(() => {
    const now = new Date();
    return {
      from: new Date(now.getFullYear(), now.getMonth(), 1).toISOString(),
      to: now.toISOString(),
    };
  }, []);
  const { data: spend } = useChargeback(from, to, "department");
  const { data: auditPage } = useAuditLogs({
    from: new Date(Date.now() - 7 * 86400_000).toISOString(),
    to: new Date().toISOString(),
    limit: 10,
  });
  const activity = auditPage?.items;

  const total = summary?.gpu_total ?? 0;
  const idle = summary?.gpus_idle ?? 0;
  const availablePct = total > 0 ? Math.round((idle / total) * 100) : 0;
  const running = workloads?.filter((w) => w.status === "running") ?? [];
  const failed = workloads?.filter((w) => w.status === "failed") ?? [];
  const onlineNodes = nodes?.filter((n) => n.status === "online").length ?? 0;
  const recent = [...(workloads ?? [])].sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)).slice(0, 5);
  const deptName = (id?: string) => departments?.find((d) => d.id === id)?.name;

  return (
    <div className="space-y-7">
      <PageHeader
        title="Overview"
        description="Operational status of your GPU fleet."
        actions={
          <Link href="/workloads?launch=1">
            <Button variant="primary" size="md"><Zap size={14} /> Quick Launch</Button>
          </Link>
        }
      />

      <OnboardingChecklist projects={projects?.length ?? 0} nodes={nodes?.length ?? 0} workloads={workloads?.length ?? 0} />

      {/* Action-oriented top row */}
      {firstLoad ? <SkeletonStats /> : (
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card className="px-5 py-4">
          <div className="text-xs font-medium text-ink-muted">Current Capacity</div>
          <div className="mt-2 text-2xl font-semibold text-ink tnum">{idle} <span className="text-base font-normal text-ink-muted">/ {total} free</span></div>
          <Meter className="mt-3" value={availablePct} tone={availablePct > 25 ? "green" : availablePct > 0 ? "amber" : "red"} />
          <div className="mt-1.5 text-xs text-ink-subtle">{availablePct}% available to run workloads</div>
        </Card>
        <StatCard label="Current Spend" value={spend ? formatMoney(spend.total) : "—"} hint="month to date" />
        <StatCard label="Running Workloads" value={running.length} hint={`${onlineNodes} node${onlineNodes === 1 ? "" : "s"} online`} />
        <Card className="px-5 py-4">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-ink-muted">Failed Workloads</span>
            {failed.length > 0 && <AlertTriangle size={15} className="text-amber-500" />}
          </div>
          <div className="mt-2 text-2xl font-semibold text-ink tnum">{failed.length}</div>
          <div className="mt-1 text-xs text-ink-subtle">{failed.length > 0 ? "needs attention" : "no failures"}</div>
        </Card>
      </div>
      )}

      {/* Fleet health + recent activity */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader title="Fleet Health" actions={<Link href="/nodes" className="text-xs text-ink-muted hover:text-ink">View nodes →</Link>} />
          <div className="space-y-4 p-5">
            <div className="flex items-center justify-between">
              <span className="inline-flex items-center gap-2 text-sm text-ink"><Server size={15} className="text-ink-subtle" /> {onlineNodes} of {nodes?.length ?? 0} nodes online</span>
              <Badge tone={onlineNodes > 0 ? "green" : "neutral"} dot>{onlineNodes > 0 ? "healthy" : "no nodes"}</Badge>
            </div>
            <div>
              <div className="mb-1.5 flex items-center justify-between text-xs text-ink-muted">
                <span>Capacity available</span><span className="tnum">{idle}/{total} GPUs</span>
              </div>
              <Meter value={availablePct} tone={availablePct > 25 ? "green" : "amber"} />
            </div>
            <div className="flex items-center justify-between border-t border-line pt-3 text-sm">
              <span className="text-ink-muted">Avg utilization</span>
              <span className="font-medium text-ink tnum">{(summary?.avg_util_pct ?? 0).toFixed(1)}%</span>
            </div>
          </div>
        </Card>

        <Card>
          <CardHeader title="Recent Activity" actions={<Link href="/audit" className="text-xs text-ink-muted hover:text-ink">Audit log →</Link>} />
          {!activity?.length ? (
            <div className="px-5 py-8 text-center text-sm text-ink-muted">No recent activity.</div>
          ) : (
            <ul className="divide-y divide-line">
              {activity.slice(0, 6).map((e) => (
                <li key={e.id} className="flex items-center justify-between gap-3 px-5 py-2.5 text-sm">
                  <span className="flex min-w-0 items-center gap-2.5">
                    <Badge tone={toneFor(AUDIT_TONE, e.action)} className="shrink-0 font-mono text-[11px]">{e.action}</Badge>
                    <span className="truncate text-ink-muted">{e.actor_type} {e.actor_id.slice(0, 8)}</span>
                  </span>
                  <span className="shrink-0 text-xs text-ink-subtle">{ago(e.ts)}</span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

      {/* Recent workloads + failures */}
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader title="Recent Workloads" actions={<Link href="/workloads" className="inline-flex items-center gap-1 text-xs text-ink-muted hover:text-ink">All <ArrowRight size={11} /></Link>} />
          {!recent.length ? (
            <EmptyState icon={<Boxes size={20} />} title="No workloads yet" action={<Link href="/workloads?launch=1"><Button size="sm" variant="primary"><Zap size={13} /> Launch</Button></Link>} className="py-10" />
          ) : (
            <ul className="divide-y divide-line">
              {recent.map((w) => (
                <li key={w.id} className="flex items-center justify-between gap-3 px-5 py-2.5">
                  <Link href={`/workloads/${w.id}`} className="min-w-0 truncate text-sm font-medium text-ink hover:underline">{w.name}</Link>
                  <span className="flex shrink-0 items-center gap-3">
                    <span className="text-xs text-ink-subtle tnum">{w.requested_gpu_count} GPU{w.requested_gpu_count === 1 ? "" : "s"}</span>
                    {deptName(w.department_id) && <span className="hidden text-xs text-ink-subtle sm:inline">{deptName(w.department_id)}</span>}
                    <Badge tone={toneFor(WORKLOAD_TONE, w.status)} dot>{w.status}</Badge>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card>
          <CardHeader title="Recent Failures" actions={<Link href="/workloads?status=failed" className="inline-flex items-center gap-1 text-xs text-ink-muted hover:text-ink">All <ArrowRight size={11} /></Link>} />
          {!failed.length ? (
            <div className="flex flex-col items-center justify-center px-5 py-10 text-center">
              <div className="mb-3 flex h-11 w-11 items-center justify-center rounded-lg border border-emerald-500/20 bg-emerald-500/10 text-emerald-400"><Server size={20} /></div>
              <p className="text-sm font-medium text-ink">No failures</p>
              <p className="mt-1 text-sm text-ink-muted">All workloads are healthy.</p>
            </div>
          ) : (
            <ul className="divide-y divide-line">
              {failed.slice(0, 5).map((w) => (
                <li key={w.id} className="flex items-center justify-between gap-3 px-5 py-2.5">
                  <Link href={`/workloads/${w.id}`} className="min-w-0 truncate text-sm font-medium text-ink hover:underline">{w.name}</Link>
                  <span className="flex shrink-0 items-center gap-3">
                    <span className="hidden max-w-[14rem] truncate font-mono text-xs text-ink-subtle md:inline">{w.image}</span>
                    <Badge tone="red" dot>failed</Badge>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}
