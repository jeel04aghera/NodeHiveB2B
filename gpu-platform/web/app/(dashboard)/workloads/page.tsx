"use client";
import { useState, useEffect, useMemo } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  useWorkloads, useLaunchWorkload, useStopWorkload,
  useWorkloadLogs, useTemplates, useDepartments, useProjects, useRates,
  type Workload, type Template,
} from "@/lib/queries";
import { useEnrichedWorkloads } from "@/lib/enrich";
import { formatMoney } from "@/lib/currency";
import { Play, Square, RefreshCw, ChevronRight, Terminal, ExternalLink, Boxes, X } from "lucide-react";
import {
  PageHeader, Card, Table, Row, Cell, Badge, Button, EmptyState, Modal, Tabs,
  SearchInput, FormField, Input, Select, CopyButton, SkeletonTable, useToast, cn, toneFor, WORKLOAD_TONE,
} from "@/components/ui";

interface LaunchForm {
  name: string; template_id: string; project_id: string; department_id: string; gpu_type: string;
  gpu_count: number; idle_timeout_sec: number; expose_ssh: boolean; expose_jupyter: boolean;
}

function fmtRuntime(w: Workload): string {
  const start = w.started_at ? new Date(w.started_at).getTime() : 0;
  if (!start) return "—";
  const end = w.stopped_at ? new Date(w.stopped_at).getTime() : (w.status === "running" || w.status === "pending" ? Date.now() : 0);
  if (!end) return "—";
  const s = Math.max(0, Math.floor((end - start) / 1000));
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}

function AccessChip({ on, ready, label }: { on: boolean; ready: boolean; label: string }) {
  if (!on) return null;
  return <Badge tone={ready ? "green" : "neutral"} className="text-[11px]">{label}</Badge>;
}

// Numbered section in the deploy flow — makes the launch feel like a deployment, not a form.
function Step({ n, title, desc, children }: { n: number; title: string; desc?: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-3">
      <div className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-ink text-xs font-semibold text-canvas">{n}</div>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium text-ink">{title}</div>
        {desc && <div className="mb-2 text-xs text-ink-subtle">{desc}</div>}
        {children}
      </div>
    </div>
  );
}

function WorkloadDetail({ wl, onClose }: { wl: Workload; onClose: () => void }) {
  const hasAccess = wl.expose_ssh || wl.expose_jupyter;
  const [tab, setTab] = useState<"access" | "logs">(hasAccess ? "access" : "logs");
  const { data: logs } = useWorkloadLogs(wl.id, tab === "logs");
  const logText = logs?.logs ?? wl.logs ?? "";
  const sshEndpoint = wl.ssh_endpoint, sshPassword = wl.ssh_password, jupyterEndpoint = wl.jupyter_endpoint;
  const sshCmd = sshEndpoint ? `ssh root@${sshEndpoint.split(":")[0]} -p ${sshEndpoint.split(":")[1] ?? "22"}` : "";
  const tabBtn = (active: boolean) => cn("rounded px-2.5 py-1 text-xs font-medium transition-colors", active ? "bg-subtle text-ink" : "text-ink-muted hover:text-ink");

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-line px-5 py-3">
        <div className="flex items-center gap-2.5">
          <span className="text-sm font-semibold text-ink">{wl.name}</span>
          <Badge tone={toneFor(WORKLOAD_TONE, wl.status)} dot>{wl.status}</Badge>
        </div>
        <div className="flex items-center gap-1">
          {hasAccess && <button onClick={() => setTab("access")} className={tabBtn(tab === "access")}>Access</button>}
          <button onClick={() => setTab("logs")} className={tabBtn(tab === "logs")}>Logs</button>
          <Link href={`/workloads/${wl.id}`} className="ml-1 inline-flex items-center gap-1 rounded px-2.5 py-1 text-xs text-ink-muted transition-colors hover:bg-subtle hover:text-ink">Details <ExternalLink size={11} /></Link>
          <button onClick={onClose} className="ml-1 rounded p-1 text-ink-subtle hover:bg-subtle hover:text-ink"><X size={15} /></button>
        </div>
      </div>
      <div className="p-5">
        {tab === "access" && hasAccess && (
          <div className="space-y-4">
            {wl.expose_ssh && (sshEndpoint ? (
              <>
                <div>
                  <p className="mb-1.5 text-xs font-medium text-ink-muted">SSH command</p>
                  <div className="flex items-center gap-2 rounded-md border border-line bg-subtle px-3 py-2"><code className="flex-1 font-mono text-sm text-ink">{sshCmd}</code><CopyButton value={sshCmd} label="Copy" /></div>
                </div>
                {sshPassword && (
                  <div>
                    <p className="mb-1.5 text-xs font-medium text-ink-muted">Password</p>
                    <div className="flex items-center gap-2 rounded-md border border-line bg-subtle px-3 py-2"><code className="flex-1 font-mono text-sm text-ink">{sshPassword}</code><CopyButton value={sshPassword} label="Copy" /></div>
                  </div>
                )}
              </>
            ) : (
              <div className="flex items-center gap-2.5 text-sm text-ink-muted"><Terminal size={16} />{wl.status === "pending" ? "Waiting for the agent to start the container and report the SSH endpoint…" : "No SSH endpoint reported yet."}</div>
            ))}
            {wl.expose_jupyter && (jupyterEndpoint ? (
              <div>
                <p className="mb-1.5 text-xs font-medium text-ink-muted">JupyterLab</p>
                <div className="flex items-center gap-2">
                  <a href={`http://${jupyterEndpoint}`} target="_blank" rel="noreferrer"><Button variant="primary" size="sm">Open Jupyter <ExternalLink size={12} /></Button></a>
                  <code className="flex-1 truncate font-mono text-xs text-ink-muted">http://{jupyterEndpoint}</code>
                  <CopyButton value={`http://${jupyterEndpoint}`} label="Copy" />
                </div>
              </div>
            ) : (
              <div className="flex items-center gap-2.5 text-sm text-ink-muted"><Terminal size={16} />{wl.status === "pending" ? "Waiting for the agent to start the container and report the Jupyter endpoint…" : "JupyterLab is still starting…"}</div>
            ))}
          </div>
        )}
        {tab === "logs" && (
          <div>
            <p className="mb-2 text-xs font-medium text-ink-muted">Container output — last 100 lines</p>
            {logText ? <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-md border border-line bg-neutral-950 p-3 font-mono text-xs text-neutral-200">{logText}</pre> : <div className="py-6 text-center text-sm text-ink-muted">{wl.status === "running" ? "No logs yet — the container may still be starting." : "No logs captured."}</div>}
          </div>
        )}
      </div>
    </Card>
  );
}

const STATUS_TABS = [
  { key: "all", label: "All" }, { key: "running", label: "Running" }, { key: "pending", label: "Pending" },
  { key: "failed", label: "Failed" }, { key: "stopped", label: "Stopped" },
];

export default function WorkloadsPage() {
  const search = useSearchParams();
  const { data: workloads, isLoading, error, refetch } = useWorkloads();
  const { data: templates } = useTemplates();
  const { data: departments } = useDepartments();
  const { data: projects } = useProjects();
  const { data: rates } = useRates();
  const launch = useLaunchWorkload();
  const stop = useStopWorkload();

  const [showModal, setShowModal] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [statusTab, setStatusTab] = useState("all");
  const [query, setQuery] = useState("");
  const [deptFilter, setDeptFilter] = useState("all");
  const [ownerFilter, setOwnerFilter] = useState("all");
  const [templateFilter, setTemplateFilter] = useState("all");

  const selected = workloads?.find((w) => w.id === selectedId) ?? null;
  const enriched = useEnrichedWorkloads((workloads ?? []).map((w) => w.id));

  // Deep links: ?launch=1 opens the modal (optionally ?project=<id>); ?status= preselects a tab.
  useEffect(() => {
    if (search.get("launch") === "1") {
      setShowModal(true);
      const proj = search.get("project");
      if (proj) setForm((f) => ({ ...f, project_id: proj }));
    }
    const s = search.get("status");
    if (s && STATUS_TABS.some((t) => t.key === s)) setStatusTab(s);
  }, [search]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { all: workloads?.length ?? 0 };
    for (const t of STATUS_TABS) if (t.key !== "all") c[t.key] = workloads?.filter((w) => w.status === t.key).length ?? 0;
    return c;
  }, [workloads]);

  const deptName = (id?: string) => departments?.find((d) => d.id === id)?.name;
  const tmplName = (id?: string) => templates?.find((t) => t.id === id)?.name;

  const owners = useMemo(() => Array.from(new Set(Object.values(enriched).map((e) => e.owner).filter(Boolean))) as string[], [enriched]);

  const filtered = useMemo(() => {
    let rows = workloads ?? [];
    if (statusTab !== "all") rows = rows.filter((w) => w.status === statusTab);
    if (deptFilter !== "all") rows = rows.filter((w) => (w.department_id ?? "") === (deptFilter === "none" ? "" : deptFilter));
    if (templateFilter !== "all") rows = rows.filter((w) => (w.template_id ?? "") === templateFilter);
    if (ownerFilter !== "all") rows = rows.filter((w) => enriched[w.id]?.owner === ownerFilter);
    if (query.trim()) {
      const q = query.toLowerCase();
      rows = rows.filter((w) => w.name.toLowerCase().includes(q) || w.image.toLowerCase().includes(q));
    }
    return [...rows].sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at));
  }, [workloads, statusTab, deptFilter, templateFilter, ownerFilter, query, enriched]);

  const [form, setForm] = useState<LaunchForm>({ name: "", template_id: "", project_id: "", department_id: "", gpu_type: "any", gpu_count: 1, idle_timeout_sec: 3600, expose_ssh: true, expose_jupyter: false });
  const [launchError, setLaunchError] = useState("");
  const selectedTemplate: Template | undefined = templates?.find((t) => t.id === form.template_id);

  // Cost estimate: resolve a GPU-hour rate from the rate cards (match the chosen GPU
  // type, else fall back to the average configured rate), then project per-hour cost.
  const estRate = (() => {
    if (!rates?.length) return null;
    const q = form.gpu_type.trim().toLowerCase();
    const match = q && q !== "any" ? rates.find((r) => r.gpu_model.toLowerCase().includes(q)) : undefined;
    const usd = match ? match.rate_per_gpu_hour : rates.reduce((s, r) => s + r.rate_per_gpu_hour, 0) / rates.length;
    return usd; // USD per GPU-hour
  })();
  const estPerHour = estRate != null ? estRate * form.gpu_count : null;

  useEffect(() => {
    if (!showModal || !templates?.length) return;
    if (!templates.some((t) => t.id === form.template_id)) {
      const first = templates[0];
      setForm((f) => ({ ...f, template_id: first.id, expose_ssh: first.default_expose_ssh, expose_jupyter: first.default_expose_jupyter }));
    }
  }, [showModal, templates, form.template_id]);

  function openModal() {
    const first = templates?.[0];
    setForm({ name: "", template_id: first?.id ?? "", project_id: search.get("project") ?? "", department_id: "", gpu_type: "any", gpu_count: 1, idle_timeout_sec: 3600, expose_ssh: first?.default_expose_ssh ?? true, expose_jupyter: first?.default_expose_jupyter ?? false });
    setLaunchError(""); setShowModal(true);
  }
  function pickTemplate(id: string) {
    const t = templates?.find((x) => x.id === id);
    setForm((f) => ({ ...f, template_id: id, expose_ssh: t?.default_expose_ssh ?? f.expose_ssh, expose_jupyter: t?.default_expose_jupyter ?? f.expose_jupyter }));
  }
  async function handleLaunch(e: React.FormEvent) {
    e.preventDefault(); setLaunchError("");
    if (!form.template_id) { setLaunchError("Select a template."); return; }
    try {
      const wl = await launch.mutateAsync({
        name: form.name, template_id: form.template_id,
        project_id: form.project_id || undefined, department_id: form.department_id || undefined,
        gpu_type: form.gpu_type === "any" ? "" : form.gpu_type, gpu_count: form.gpu_count,
        idle_timeout_sec: form.idle_timeout_sec, expose_ssh: form.expose_ssh, expose_jupyter: form.expose_jupyter,
      });
      setShowModal(false); setSelectedId((wl as Workload).id);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : "Launch failed";
      setLaunchError(msg.includes("no_capacity") ? "No idle GPUs available." : msg);
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title="Workloads"
        description="GPU containers running across your fleet."
        actions={
          <>
            <Button variant="ghost" size="icon" onClick={() => refetch()} title="Refresh"><RefreshCw size={15} /></Button>
            <Button variant="primary" size="md" onClick={openModal}><Play size={14} /> Launch Workload</Button>
          </>
        }
      />

      <Tabs items={STATUS_TABS.map((t) => ({ ...t, count: counts[t.key] }))} value={statusTab} onChange={setStatusTab} />

      <div className="flex flex-wrap items-center gap-2.5">
        <SearchInput value={query} onChange={setQuery} placeholder="Search workloads…" className="w-64" />
        <Select value={deptFilter} onChange={(e) => setDeptFilter(e.target.value)} className="h-9 w-auto py-0">
          <option value="all">All departments</option>
          <option value="none">Unassigned</option>
          {departments?.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
        </Select>
        <Select value={ownerFilter} onChange={(e) => setOwnerFilter(e.target.value)} className="h-9 w-auto py-0">
          <option value="all">All owners</option>
          {owners.map((o) => <option key={o} value={o}>{o}</option>)}
        </Select>
        <Select value={templateFilter} onChange={(e) => setTemplateFilter(e.target.value)} className="h-9 w-auto py-0">
          <option value="all">All templates</option>
          {templates?.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
        </Select>
      </div>

      {error ? (
        <Card><EmptyState icon={<Boxes size={20} />} title="Failed to load workloads" action={<Button size="sm" variant="secondary" onClick={() => refetch()}>Retry</Button>} /></Card>
      ) : isLoading ? (
        <Card className="p-10 text-center text-sm text-ink-muted">Loading workloads…</Card>
      ) : (
        <Card className="overflow-hidden">
          {!filtered.length ? (
            workloads?.length ? (
              <div className="px-5 py-12 text-center text-sm text-ink-muted">No workloads match these filters.</div>
            ) : (
              <EmptyState icon={<Boxes size={20} />} title="No workloads yet" description="Launch a GPU container from a template to get SSH or JupyterLab access in seconds." action={<Button variant="primary" size="sm" onClick={openModal}><Play size={14} /> Launch Workload</Button>} />
            )
          ) : (
            <Table columns={[
              { key: "name", label: "Name" }, { key: "status", label: "Status" }, { key: "gpus", label: "GPUs", align: "right" },
              { key: "runtime", label: "Runtime", align: "right" }, { key: "owner", label: "Owner" }, { key: "dept", label: "Department" },
              { key: "cost", label: "Cost", align: "right" }, { key: "access", label: "Access" }, { key: "actions", label: "" },
            ]}>
              {filtered.map((wl) => {
                const e = enriched[wl.id];
                return (
                  <Row key={wl.id} onClick={() => setSelectedId(selectedId === wl.id ? null : wl.id)} className="cursor-pointer">
                    <Cell>
                      <div className="flex items-center gap-1.5">
                        <ChevronRight size={13} className={cn("text-ink-subtle transition-transform", selected?.id === wl.id && "rotate-90")} />
                        <span className="font-medium text-ink">{wl.name}</span>
                      </div>
                    </Cell>
                    <Cell><Badge tone={toneFor(WORKLOAD_TONE, wl.status)} dot>{wl.status}</Badge></Cell>
                    <Cell align="right" className="tnum text-ink">{wl.requested_gpu_count}</Cell>
                    <Cell align="right" className="tnum">{fmtRuntime(wl)}</Cell>
                    <Cell className="max-w-[12rem] truncate text-xs">{e?.owner ?? <span className="text-ink-subtle">—</span>}</Cell>
                    <Cell className="text-xs">{deptName(wl.department_id) ?? e?.department ?? <span className="text-ink-subtle">—</span>}</Cell>
                    <Cell align="right" className="tnum">{e?.runtime_cost != null ? formatMoney(e.runtime_cost) : <span className="text-ink-subtle">—</span>}</Cell>
                    <Cell>
                      <div className="flex items-center gap-1.5">
                        <AccessChip on={wl.expose_ssh} ready={!!wl.ssh_endpoint} label="SSH" />
                        <AccessChip on={wl.expose_jupyter} ready={!!wl.jupyter_endpoint} label="Jupyter" />
                        {!wl.expose_ssh && !wl.expose_jupyter && <span className="text-xs text-ink-subtle">—</span>}
                      </div>
                    </Cell>
                    <Cell>
                      {(wl.status === "running" || wl.status === "pending") && (
                        <Button variant="danger" size="sm" onClick={(ev) => { ev.stopPropagation(); stop.mutate(wl.id); }} disabled={stop.isPending}><Square size={11} /> Stop</Button>
                      )}
                    </Cell>
                  </Row>
                );
              })}
            </Table>
          )}
        </Card>
      )}

      {selected && <WorkloadDetail wl={selected} onClose={() => setSelectedId(null)} />}

      {showModal && (
        <Modal title="Deploy workload" onClose={() => setShowModal(false)} maxWidth="max-w-xl">
          <form onSubmit={handleLaunch} className="space-y-5 p-5">
            <FormField label="Workload name" required><Input type="text" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="my-experiment" required autoFocus /></FormField>

            <Step n={1} title="Template" desc="The environment your workload runs in.">
              <Select value={form.template_id} onChange={(e) => pickTemplate(e.target.value)}>
                {!templates?.length && <option value="">Loading…</option>}
                {templates?.map((t) => <option key={t.id} value={t.id}>{t.name}{t.version ? ` (${t.version})` : ""}</option>)}
              </Select>
              {selectedTemplate && (
                <div className="mt-2 flex items-center gap-2 rounded-md border border-line bg-subtle px-3 py-2 text-xs">
                  <span className="text-ink-subtle">Image</span><code className="truncate font-mono text-ink">{selectedTemplate.base_image}</code>
                </div>
              )}
            </Step>

            <Step n={2} title="Project" desc="Where this workload’s cost and activity roll up.">
              <div className="grid grid-cols-2 gap-3">
                <Select value={form.project_id} onChange={(e) => setForm({ ...form, project_id: e.target.value })}>
                  <option value="">No project</option>
                  {projects?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                </Select>
                <Select value={form.department_id} onChange={(e) => setForm({ ...form, department_id: e.target.value })}>
                  <option value="">My department</option>
                  {departments?.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
                </Select>
              </div>
            </Step>

            <Step n={3} title="Resources" desc="GPU type and count for this deployment.">
              <div className="grid grid-cols-2 gap-3">
                <Input type="text" value={form.gpu_type} onChange={(e) => setForm({ ...form, gpu_type: e.target.value })} placeholder="GPU type — e.g. A100, any" />
                <Input type="number" min={1} max={8} value={form.gpu_count} onChange={(e) => setForm({ ...form, gpu_count: parseInt(e.target.value) || 1 })} />
              </div>
            </Step>

            <Step n={4} title="Access" desc="How you’ll connect once it’s running.">
              <div className="flex gap-3">
                {[{ key: "expose_ssh", label: "SSH", desc: "Terminal (port 22)" }, { key: "expose_jupyter", label: "Jupyter", desc: "Notebook (port 8888)" }].map(({ key, label, desc }) => {
                  const checked = form[key as keyof LaunchForm] as boolean;
                  return (
                    <label key={key} className={cn("flex flex-1 cursor-pointer items-start gap-2.5 rounded-md border p-3 transition-colors", checked ? "border-line-strong bg-subtle" : "border-line bg-surface hover:bg-subtle")}>
                      <input type="checkbox" checked={checked} onChange={(e) => setForm({ ...form, [key]: e.target.checked })} className="mt-0.5 accent-neutral-900" />
                      <div><div className="text-sm font-medium text-ink">{label}</div><div className="text-xs text-ink-subtle">{desc}</div></div>
                    </label>
                  );
                })}
              </div>
              <div className="mt-3"><FormField label="Idle auto-stop (seconds)" hint="Frees the GPUs automatically after this much idle time."><Input type="number" min={300} value={form.idle_timeout_sec} onChange={(e) => setForm({ ...form, idle_timeout_sec: parseInt(e.target.value) || 3600 })} /></FormField></div>
            </Step>

            {/* Estimate */}
            <div className="rounded-lg border border-line bg-subtle p-4">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium uppercase tracking-wide text-ink-subtle">Estimated cost</span>
                <span className="text-lg font-semibold text-ink tnum">{estPerHour != null ? `${formatMoney(estPerHour)} / hr` : "—"}</span>
              </div>
              <div className="mt-1.5 flex items-center justify-between text-xs text-ink-muted">
                <span>{form.gpu_count} GPU{form.gpu_count === 1 ? "" : "s"}{estRate != null ? ` · ${formatMoney(estRate)}/GPU-hr` : ""}</span>
                <span>Auto-stops after {Math.round(form.idle_timeout_sec / 60)}m idle</span>
              </div>
              {estRate == null && <p className="mt-2 text-xs text-ink-subtle">Set GPU rate cards under Settings → Billing to see a cost estimate.</p>}
            </div>

            {launchError && <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">{launchError}</p>}
            <div className="flex gap-3">
              <Button type="button" variant="secondary" className="flex-1 justify-center" onClick={() => setShowModal(false)}>Cancel</Button>
              <Button type="submit" variant="primary" className="flex-1 justify-center" disabled={launch.isPending}><Play size={14} /> {launch.isPending ? "Deploying…" : "Deploy workload"}</Button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
