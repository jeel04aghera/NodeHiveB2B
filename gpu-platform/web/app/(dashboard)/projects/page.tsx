"use client";
import { useMemo, useState } from "react";
import Link from "next/link";
import { useProjects, useCreateProject, useWorkloads, useChargeback } from "@/lib/queries";
import { useEnrichedWorkloads } from "@/lib/enrich";
import { formatMoney } from "@/lib/currency";
import {
  PageHeader, Card, Table, Row, Cell, Badge, Button, EmptyState, Modal, FormField, Input,
} from "@/components/ui";
import { Layers, Plus, ArrowRight } from "lucide-react";

function ago(iso?: string) {
  if (!iso) return "—";
  const d = Date.now() - new Date(iso).getTime();
  if (d < 60_000) return "just now";
  if (d < 3600_000) return `${Math.floor(d / 60_000)}m ago`;
  if (d < 86400_000) return `${Math.floor(d / 3600_000)}h ago`;
  return `${Math.floor(d / 86400_000)}d ago`;
}

export default function ProjectsPage() {
  const { data: projects, isLoading, error } = useProjects();
  const { data: workloads } = useWorkloads();
  const enriched = useEnrichedWorkloads((workloads ?? []).map((w) => w.id));
  const createProject = useCreateProject();

  const { from, to } = useMemo(() => {
    const now = new Date();
    return { from: new Date(now.getFullYear(), now.getMonth(), 1).toISOString(), to: now.toISOString() };
  }, []);
  const { data: spend } = useChargeback(from, to, "project");
  const spendByName = useMemo(() => {
    const m: Record<string, number> = {};
    for (const r of spend?.rows ?? []) m[r.group_key] = r.amount;
    return m;
  }, [spend]);

  const rows = useMemo(() => (projects ?? []).map((p) => {
    const wls = (workloads ?? []).filter((w) => w.project_id === p.id);
    const running = wls.filter((w) => w.status === "running").length;
    const members = new Set(wls.map((w) => enriched[w.id]?.owner).filter(Boolean));
    const last = wls.map((w) => w.created_at).sort().reverse()[0];
    return { project: p, workloads: wls.length, running, members: members.size, spend: spendByName[p.name] ?? 0, last };
  }), [projects, workloads, enriched, spendByName]);

  const [showModal, setShowModal] = useState(false);
  const [name, setName] = useState("");
  const [err, setErr] = useState("");

  async function create(e: React.FormEvent) {
    e.preventDefault(); setErr("");
    if (!name.trim()) { setErr("Project name is required."); return; }
    try { await createProject.mutateAsync({ name: name.trim() }); setShowModal(false); setName(""); }
    catch { setErr("Could not create project."); }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Projects"
        description="Group workloads, spend, and members by team or initiative."
        actions={<Button variant="primary" size="md" onClick={() => { setErr(""); setName(""); setShowModal(true); }}><Plus size={14} /> New Project</Button>}
      />

      {error ? (
        <Card><EmptyState icon={<Layers size={20} />} title="Failed to load projects" /></Card>
      ) : isLoading ? (
        <Card className="p-10 text-center text-sm text-ink-muted">Loading projects…</Card>
      ) : (
        <Card className="overflow-hidden">
          {!rows.length ? (
            <EmptyState icon={<Layers size={20} />} title="No projects yet" description="Create a project to organize workloads and attribute GPU spend by team." action={<Button variant="primary" size="sm" onClick={() => setShowModal(true)}><Plus size={14} /> New Project</Button>} />
          ) : (
            <Table columns={[
              { key: "n", label: "Project" }, { key: "w", label: "Workloads", align: "right" }, { key: "r", label: "Running", align: "right" },
              { key: "s", label: "Spend", align: "right" }, { key: "m", label: "Members", align: "right" }, { key: "a", label: "Last activity" }, { key: "x", label: "" },
            ]}>
              {rows.map(({ project, workloads: wc, running, members, spend, last }) => (
                <Row key={project.id} className="cursor-pointer">
                  <Cell>
                    <Link href={`/projects/${project.id}`} className="inline-flex items-center gap-2 font-medium text-ink hover:underline">
                      <Layers size={14} className="text-ink-subtle" />{project.name}
                    </Link>
                  </Cell>
                  <Cell align="right" className="tnum">{wc}</Cell>
                  <Cell align="right">{running > 0 ? <Badge tone="green" dot>{running}</Badge> : <span className="text-ink-subtle">0</span>}</Cell>
                  <Cell align="right" className="tnum font-medium text-ink">{formatMoney(spend)}</Cell>
                  <Cell align="right" className="tnum">{members}</Cell>
                  <Cell className="text-xs">{ago(last)}</Cell>
                  <Cell align="right"><Link href={`/projects/${project.id}`} className="text-ink-subtle hover:text-ink"><ArrowRight size={15} /></Link></Cell>
                </Row>
              ))}
            </Table>
          )}
        </Card>
      )}

      {showModal && (
        <Modal title="New project" onClose={() => setShowModal(false)} maxWidth="max-w-md">
          <form onSubmit={create} className="space-y-4 p-5">
            <FormField label="Project name" required><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. recommender-team" required autoFocus /></FormField>
            {err && <p className="text-sm text-red-400">{err}</p>}
            <div className="flex gap-3 pt-1">
              <Button type="button" variant="secondary" className="flex-1 justify-center" onClick={() => setShowModal(false)}>Cancel</Button>
              <Button type="submit" variant="primary" className="flex-1 justify-center" disabled={createProject.isPending}>{createProject.isPending ? "Creating…" : "Create project"}</Button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
