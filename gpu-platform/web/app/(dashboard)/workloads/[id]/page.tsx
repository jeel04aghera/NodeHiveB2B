"use client";
import { useParams } from "next/navigation";
import Link from "next/link";
import { useWorkloadDetail, useStopWorkload, useWorkloadLogs, useWorkloadEvents, type LifecycleEvent } from "@/lib/queries";
import { ArrowLeft, Square, Cpu, Clock, DollarSign, Server, Check, X as XIcon, Loader2 } from "lucide-react";
import {
  Card, CardHeader, StatCard, Badge, Button, CopyButton, toneFor, WORKLOAD_TONE,
} from "@/components/ui";

function fmtDuration(sec?: number) {
  if (!sec || sec <= 0) return "—";
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return [h ? `${h}h` : "", m ? `${m}m` : "", `${s}s`].filter(Boolean).join(" ");
}

// F2 — canonical happy-path deployment stages, in order, with human labels.
const DEPLOY_STAGES: { key: string; label: string }[] = [
  { key: "queued", label: "Queued" },
  { key: "scheduling", label: "Scheduling" },
  { key: "node_selected", label: "Node selected" },
  { key: "preparing", label: "Preparing" },
  { key: "pulling_image", label: "Pulling image" },
  { key: "building_env", label: "Building env" },
  { key: "configuring", label: "Configuring" },
  { key: "starting_container", label: "Starting" },
  { key: "ready", label: "Ready" },
];

const STAGE_LABEL: Record<string, string> = {
  ...Object.fromEntries(DEPLOY_STAGES.map((s) => [s.key, s.label])),
  ssh_enabled: "SSH enabled",
  jupyter_enabled: "Jupyter enabled",
  stopping: "Stopping",
  stopped: "Stopped",
  failed: "Failed",
};

// DeploymentProgress (F2) renders a horizontal stepper for the launch lifecycle,
// deriving completed/active steps from the workload's current stage + status.
function DeploymentProgress({ stage, status }: { stage?: string; status: string }) {
  const failed = status === "failed";
  const terminalDone = status === "running" || status === "stopped";
  // Index of the current stage within the happy path. ready/terminal → all done.
  let activeIdx = DEPLOY_STAGES.findIndex((s) => s.key === stage);
  if (terminalDone || stage === "ready" || stage === "ssh_enabled" || stage === "jupyter_enabled") {
    activeIdx = DEPLOY_STAGES.length - 1;
  }
  if (activeIdx < 0) activeIdx = 0;

  return (
    <Card>
      <CardHeader title="Deployment progress" />
      <div className="flex flex-wrap items-center gap-x-1 gap-y-3 p-5">
        {DEPLOY_STAGES.map((s, i) => {
          const done = i < activeIdx || (terminalDone && i <= activeIdx);
          const active = i === activeIdx && !terminalDone && !failed;
          const isFailedHere = failed && i === activeIdx;
          return (
            <div key={s.key} className="flex items-center">
              <div className="flex flex-col items-center gap-1.5">
                <div
                  className={
                    "flex h-7 w-7 items-center justify-center rounded-full border text-[11px] font-medium " +
                    (isFailedHere
                      ? "border-red-500/40 bg-red-500/10 text-red-400"
                      : done
                      ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-400"
                      : active
                      ? "border-blue-500/40 bg-blue-500/10 text-blue-400"
                      : "border-line bg-subtle text-ink-subtle")
                  }
                >
                  {isFailedHere ? <XIcon size={13} /> : done ? <Check size={13} /> : active ? <Loader2 size={13} className="animate-spin" /> : i + 1}
                </div>
                <span className={"text-[10px] " + (done || active || isFailedHere ? "text-ink-muted" : "text-ink-subtle")}>{s.label}</span>
              </div>
              {i < DEPLOY_STAGES.length - 1 && (
                <div className={"mx-1 h-px w-6 " + (i < activeIdx || terminalDone ? "bg-emerald-500/40" : "bg-line")} />
              )}
            </div>
          );
        })}
      </div>
    </Card>
  );
}

function stageTone(stage: string): string {
  if (stage === "failed") return "border-red-500/40 bg-red-500/10 text-red-400";
  if (stage === "ready" || stage === "ssh_enabled" || stage === "jupyter_enabled" || stage === "stopped") return "border-emerald-500/40 bg-emerald-500/10 text-emerald-400";
  return "border-blue-500/30 bg-blue-500/10 text-blue-400";
}

// Timeline (F1) — chronological lifecycle events with timestamps + relative spacing.
function Timeline({ events }: { events: LifecycleEvent[] }) {
  if (events.length === 0) return <p className="p-5 text-sm text-ink-muted">No events recorded yet.</p>;
  return (
    <ol className="space-y-0 p-5">
      {events.map((e, i) => (
        <li key={e.id} className="flex gap-3">
          <div className="flex flex-col items-center">
            <span className={"flex h-6 w-6 items-center justify-center rounded-full border text-[10px] " + stageTone(e.stage)}>●</span>
            {i < events.length - 1 && <span className="my-0.5 w-px flex-1 bg-line" />}
          </div>
          <div className="pb-4">
            <div className="text-sm font-medium text-ink">{STAGE_LABEL[e.stage] ?? e.stage}</div>
            {e.message && <div className="text-xs text-ink-muted">{e.message}</div>}
            <div className="mt-0.5 text-[11px] text-ink-subtle tnum">{new Date(e.ts).toLocaleString()}</div>
          </div>
        </li>
      ))}
    </ol>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="mb-1 text-xs font-medium text-ink-muted">{label}</p>
      <div className="text-sm text-ink">{children}</div>
    </div>
  );
}

export default function WorkloadDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data: wl, isLoading, error } = useWorkloadDetail(id);
  const stop = useStopWorkload();
  const { data: logs } = useWorkloadLogs(id, true);
  const { data: events } = useWorkloadEvents(id);
  const logText = logs?.logs ?? wl?.logs ?? "";

  if (isLoading) return <div className="py-8 text-sm text-ink-muted">Loading workload…</div>;
  if (error || !wl) return <div className="py-8 text-sm text-red-400">Workload not found.</div>;

  const sshCmd = wl.ssh_endpoint
    ? `ssh root@${wl.ssh_endpoint.split(":")[0]} -p ${wl.ssh_endpoint.split(":")[1] ?? "22"}`
    : "";

  return (
    <div className="space-y-6">
      <Link href="/workloads" className="inline-flex items-center gap-1.5 text-sm text-ink-muted hover:text-ink">
        <ArrowLeft size={14} /> Workloads
      </Link>

      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <h1 className="text-xl font-semibold tracking-tight text-ink">{wl.name}</h1>
          <Badge tone={toneFor(WORKLOAD_TONE, wl.status)} dot>{wl.status}</Badge>
        </div>
        {(wl.status === "running" || wl.status === "pending" || wl.status === "queued") && (
          <Button variant="danger" size="md" onClick={() => stop.mutate(wl.id)} disabled={stop.isPending}>
            <Square size={13} /> {wl.status === "queued" ? "Cancel" : "Stop"}
          </Button>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard label="Runtime" value={fmtDuration(wl.runtime_seconds)} icon={<Clock size={15} />} />
        <StatCard label="Runtime cost" value={`${(wl.runtime_cost ?? 0).toFixed(4)} ${wl.currency ?? "USD"}`} icon={<DollarSign size={15} />} />
        <StatCard label="GPUs" value={String(wl.gpus?.length ?? wl.requested_gpu_count)} icon={<Cpu size={15} />} />
        <StatCard
          label="Node"
          value={wl.node_id ? <Link className="text-ink hover:underline" href={`/nodes/${wl.node_id}`}>view</Link> : "—"}
          icon={<Server size={15} />}
        />
      </div>

      <DeploymentProgress stage={wl.stage} status={wl.status} />

      <div className="grid gap-6 md:grid-cols-2">
        <Card>
          <CardHeader title="Details" />
          <div className="grid grid-cols-2 gap-4 p-5">
            <Field label="Owner">{wl.owner || "—"}</Field>
            <Field label="Department">{wl.department || <span className="text-ink-subtle">unassigned</span>}</Field>
            <Field label="Template">{wl.template ? `${wl.template}${wl.template_version ? ` (${wl.template_version})` : ""}` : <span className="text-ink-subtle">ad-hoc</span>}</Field>
            <Field label="Actual image"><code className="font-mono text-xs text-ink">{wl.image}</code></Field>
            <Field label="Container ID"><code className="font-mono text-xs text-ink">{wl.container_id || "—"}</code></Field>
            <Field label="Created">{new Date(wl.created_at).toLocaleString()}</Field>
          </div>
        </Card>

        <Card>
          <CardHeader title="Access & allocation" />
          <div className="space-y-4 p-5">
            {wl.expose_ssh && (
              <Field label="SSH">
                {wl.ssh_endpoint ? (
                  <div className="space-y-1.5">
                    <div className="flex items-center gap-2 rounded-md border border-line bg-subtle px-2.5 py-1.5">
                      <code className="flex-1 font-mono text-xs text-ink">{sshCmd}</code>
                      <CopyButton value={sshCmd} />
                    </div>
                    {wl.ssh_password && (
                      <div className="text-xs text-ink-muted">password: <code className="font-mono text-ink">{wl.ssh_password}</code></div>
                    )}
                  </div>
                ) : <span className="text-xs text-ink-subtle">starting…</span>}
              </Field>
            )}
            {wl.expose_jupyter && (
              <Field label="Jupyter">
                {wl.jupyter_endpoint ? (
                  <a href={`http://${wl.jupyter_endpoint}`} target="_blank" rel="noreferrer">
                    <Button variant="primary" size="sm">Open Jupyter ↗</Button>
                  </a>
                ) : <span className="text-xs text-ink-subtle">starting…</span>}
              </Field>
            )}
            <Field label="GPU allocation">
              {wl.gpus && wl.gpus.length > 0 ? (
                <ul className="space-y-1">
                  {wl.gpus.map((g) => (
                    <li key={g.uuid} className="text-xs">
                      <span className="text-ink">{g.model}</span>
                      <code className="ml-2 font-mono text-ink-subtle">{g.uuid}</code>
                    </li>
                  ))}
                </ul>
              ) : <span className="text-xs text-ink-subtle">none</span>}
            </Field>
          </div>
        </Card>
      </div>

      <Card>
        <CardHeader title="Lifecycle timeline" />
        <Timeline events={events ?? []} />
      </Card>

      <Card>
        <CardHeader title="Container logs" />
        <div className="p-5">
          {logText ? (
            <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-md border border-line bg-neutral-950 p-3 font-mono text-xs text-neutral-200">{logText}</pre>
          ) : (
            <p className="text-sm text-ink-muted">No logs captured yet.</p>
          )}
        </div>
      </Card>
    </div>
  );
}
