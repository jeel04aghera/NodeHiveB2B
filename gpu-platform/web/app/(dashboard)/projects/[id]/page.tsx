"use client";
import { useMemo, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import {
  useProjects, useWorkloads, useChargeback,
  useProjectDetailSettings, useUpdateProject, useAddProjectMember, useRemoveProjectMember, useUsers,
} from "@/lib/queries";
import { useAuth, isAdminRole } from "@/lib/auth";
import { useEnrichedWorkloads } from "@/lib/enrich";
import { formatMoney } from "@/lib/currency";
import {
  Card, CardHeader, StatCard, Table, Row, Cell, Badge, Button, EmptyState, Tabs, Meter,
  Select, FormField, toneFor, WORKLOAD_TONE,
} from "@/components/ui";
import { ArrowLeft, Layers, Play, User, Lock } from "lucide-react";

function ago(iso?: string) {
  if (!iso) return "—";
  const d = Date.now() - new Date(iso).getTime();
  if (d < 60_000) return "just now";
  if (d < 3600_000) return `${Math.floor(d / 60_000)}m ago`;
  if (d < 86400_000) return `${Math.floor(d / 3600_000)}h ago`;
  return `${Math.floor(d / 86400_000)}d ago`;
}

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { user } = useAuth();
  const isAdmin = isAdminRole(user?.role);
  const { data: projects } = useProjects();
  const { data: workloads } = useWorkloads();
  const enriched = useEnrichedWorkloads((workloads ?? []).map((w) => w.id));
  const [tab, setTab] = useState("workloads");

  const project = projects?.find((p) => p.id === id);
  const wls = useMemo(() => (workloads ?? []).filter((w) => w.project_id === id).sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at)), [workloads, id]);
  const running = wls.filter((w) => w.status === "running").length;

  const { from, to } = useMemo(() => {
    const now = new Date();
    return { from: new Date(now.getFullYear(), now.getMonth(), 1).toISOString(), to: now.toISOString() };
  }, []);
  const { data: spend } = useChargeback(from, to, "project");
  const projectSpend = spend?.rows?.find((r) => r.group_key === project?.name)?.amount ?? 0;

  const members = useMemo(() => {
    const m: Record<string, { count: number; cost: number }> = {};
    for (const w of wls) {
      const o = enriched[w.id]?.owner;
      if (!o) continue;
      m[o] ??= { count: 0, cost: 0 };
      m[o].count++;
      m[o].cost += enriched[w.id]?.runtime_cost ?? 0;
    }
    return Object.entries(m).sort((a, b) => b[1].cost - a[1].cost);
  }, [wls, enriched]);

  const launchHref = `/workloads?launch=1&project=${id}`;

  return (
    <div className="space-y-6">
      <Link href="/projects" className="inline-flex items-center gap-1.5 text-sm text-ink-muted hover:text-ink"><ArrowLeft size={14} /> Projects</Link>

      <div className="flex items-center justify-between">
        <h1 className="inline-flex items-center gap-2.5 text-xl font-semibold tracking-tight text-ink">
          <Layers size={20} className="text-ink-subtle" />{project?.name ?? "Project"}
          {project?.visibility === "restricted" && (
            <Badge tone="amber"><Lock size={11} /> Restricted</Badge>
          )}
          {project?.archived_at && <Badge tone="neutral">Archived</Badge>}
        </h1>
        <Link href={launchHref}><Button variant="primary" size="md"><Play size={14} /> New workload</Button></Link>
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard label="Spend (month)" value={formatMoney(projectSpend)} hint="attributed to this project" />
        <StatCard label="Workloads" value={wls.length} hint="all time" />
        <StatCard label="Running" value={running} hint="active now" />
        <StatCard label="Members" value={members.length} hint="contributing owners" />
      </div>

      <Tabs
        items={[
          { key: "workloads", label: "Workloads", count: wls.length },
          { key: "members", label: "Members", count: members.length },
          { key: "activity", label: "Activity" },
          ...(isAdmin ? [{ key: "access", label: "Access" }] : []),
        ]}
        value={tab}
        onChange={setTab}
      />

      {tab === "workloads" && (
        <Card className="overflow-hidden">
          {!wls.length ? (
            <EmptyState icon={<Layers size={20} />} title="No workloads in this project" description="Launch a workload and assign it to this project." action={<Link href={launchHref}><Button size="sm" variant="primary"><Play size={13} /> New workload</Button></Link>} />
          ) : (
            <Table columns={[{ key: "n", label: "Name" }, { key: "s", label: "Status" }, { key: "g", label: "GPUs", align: "right" }, { key: "o", label: "Owner" }, { key: "c", label: "Cost", align: "right" }]}>
              {wls.map((w) => (
                <Row key={w.id}>
                  <Cell><Link href={`/workloads/${w.id}`} className="font-medium text-ink hover:underline">{w.name}</Link></Cell>
                  <Cell><Badge tone={toneFor(WORKLOAD_TONE, w.status)} dot>{w.status}</Badge></Cell>
                  <Cell align="right" className="tnum">{w.requested_gpu_count}</Cell>
                  <Cell className="text-xs">{enriched[w.id]?.owner ?? <span className="text-ink-subtle">—</span>}</Cell>
                  <Cell align="right" className="tnum">{enriched[w.id]?.runtime_cost != null ? formatMoney(enriched[w.id]!.runtime_cost!) : <span className="text-ink-subtle">—</span>}</Cell>
                </Row>
              ))}
            </Table>
          )}
        </Card>
      )}

      {tab === "members" && (
        <Card className="overflow-hidden">
          {!members.length ? (
            <EmptyState icon={<User size={20} />} title="No members yet" description="Members appear here once they launch workloads in this project." />
          ) : (
            <Table columns={[{ key: "m", label: "Member" }, { key: "w", label: "Workloads", align: "right" }, { key: "c", label: "Spend", align: "right" }, { key: "s", label: "Share" }]}>
              {members.map(([owner, v]) => {
                const share = projectSpend > 0 ? (v.cost / projectSpend) * 100 : 0;
                return (
                  <Row key={owner}>
                    <Cell><span className="inline-flex items-center gap-2 font-medium text-ink"><span className="flex h-6 w-6 items-center justify-center rounded-full bg-subtle text-[10px] font-semibold text-ink-muted">{owner.slice(0, 1).toUpperCase()}</span>{owner}</span></Cell>
                    <Cell align="right" className="tnum">{v.count}</Cell>
                    <Cell align="right" className="tnum font-medium text-ink">{formatMoney(v.cost)}</Cell>
                    <Cell><div className="flex items-center gap-2"><Meter className="w-24" value={share} /><span className="w-9 text-right text-xs text-ink-muted tnum">{share.toFixed(0)}%</span></div></Cell>
                  </Row>
                );
              })}
            </Table>
          )}
        </Card>
      )}

      {tab === "access" && isAdmin && <ProjectAccess id={id} />}

      {tab === "activity" && (
        <Card>
          {!wls.length ? (
            <div className="px-5 py-10 text-center text-sm text-ink-muted">No activity yet.</div>
          ) : (
            <ul className="divide-y divide-line">
              {wls.slice(0, 12).map((w) => (
                <li key={w.id} className="flex items-center justify-between gap-3 px-5 py-3">
                  <div className="flex items-center gap-2.5">
                    <Badge tone={toneFor(WORKLOAD_TONE, w.status)} dot>{w.status}</Badge>
                    <Link href={`/workloads/${w.id}`} className="text-sm font-medium text-ink hover:underline">{w.name}</Link>
                    <span className="text-xs text-ink-subtle">{enriched[w.id]?.owner ?? ""}</span>
                  </div>
                  <span className="text-xs text-ink-subtle">{ago(w.created_at)}</span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}
    </div>
  );
}

// ProjectAccess (Phase 6, admin-only): visibility, archive and the explicit member
// list that gates a restricted project.
function ProjectAccess({ id }: { id: string }) {
  const { data: detail } = useProjectDetailSettings(id);
  const { data: users } = useUsers();
  const update = useUpdateProject();
  const addMember = useAddProjectMember();
  const removeMember = useRemoveProjectMember();
  const [pick, setPick] = useState("");

  const memberIds = new Set((detail?.members ?? []).map((m) => m.user_id));
  const candidates = (users ?? []).filter((u) => !memberIds.has(u.id));

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader
          title="Access control"
          description="Restricted projects are only visible and usable by the members below (plus org admins). Open projects admit every org member."
        />
        <div className="flex flex-wrap items-end gap-4 px-5 pb-5">
          <FormField label="Visibility">
            <Select
              value={detail?.visibility ?? "open"}
              onChange={(e) => update.mutate({ id, visibility: e.target.value })}
              className="w-56"
            >
              <option value="open">Open — all org members</option>
              <option value="restricted">Restricted — members only</option>
            </Select>
          </FormField>
          <Button
            size="sm"
            variant={detail?.archived_at ? "secondary" : "danger"}
            onClick={() => update.mutate({ id, archived: !detail?.archived_at })}
          >
            {detail?.archived_at ? "Restore project" : "Archive project"}
          </Button>
          {detail?.archived_at && (
            <span className="pb-2 text-xs text-ink-subtle">Archived projects refuse new launches; running workloads continue.</span>
          )}
        </div>
      </Card>

      <Card>
        <CardHeader title="Project members" description="Who can use this project when it is restricted." />
        <div className="flex items-end gap-3 px-5 pb-4">
          <FormField label="Add member">
            <Select value={pick} onChange={(e) => setPick(e.target.value)} className="w-64">
              <option value="">Select a user…</option>
              {candidates.map((u) => (
                <option key={u.id} value={u.id}>{u.name ? `${u.name} <${u.email}>` : u.email}</option>
              ))}
            </Select>
          </FormField>
          <Button size="sm" disabled={!pick || addMember.isPending}
            onClick={async () => { await addMember.mutateAsync({ id, user_id: pick }); setPick(""); }}>
            Add
          </Button>
        </div>
        {!detail?.members?.length ? (
          <div className="px-5 pb-6 text-sm text-ink-muted">No explicit members. If the project is restricted, only org admins can use it.</div>
        ) : (
          <Table columns={[{ key: "m", label: "Member" }, { key: "since", label: "Added" }, { key: "x", label: "" }]}>
            {detail.members.map((m) => (
              <Row key={m.user_id}>
                <Cell className="font-medium text-ink">{m.name || m.email}<span className="ml-2 text-xs text-ink-subtle">{m.email}</span></Cell>
                <Cell className="text-xs text-ink-subtle">{new Date(m.created_at).toLocaleDateString()}</Cell>
                <Cell>
                  <Button size="sm" variant="ghost" onClick={() => removeMember.mutate({ id, user_id: m.user_id })}>Remove</Button>
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>
    </div>
  );
}
