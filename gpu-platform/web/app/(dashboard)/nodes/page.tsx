"use client";
import Link from "next/link";
import { useState, useMemo, useRef, useEffect } from "react";
import { useNodes, useGPUs, useWorkloads, useIssueEnrollmentToken } from "@/lib/queries";
import { API_BASE_ORIGIN } from "@/lib/api-client";
import { Server, RefreshCw, Loader2, CheckCircle2 } from "lucide-react";
import {
  PageHeader, Card, Table, Row, Cell, Badge, Button, EmptyState, CopyButton, Meter,
  toneFor, HEALTH_TONE, useToast,
} from "@/components/ui";

function deriveHealth(status: string, lastSeen: string | null): string {
  if (status !== "online") return "offline";
  if (!lastSeen) return "stale";
  return Date.now() - new Date(lastSeen).getTime() < 90_000 ? "healthy" : "stale";
}

// Control-plane origin (the /api/v1 suffix stripped) — where install.sh is served.
const CP_ORIGIN = API_BASE_ORIGIN;

function EnrollPanel({ token, enrolledNodeName }: { token: string; enrolledNodeName?: string }) {
  const installCmd = `curl -fsSL ${CP_ORIGIN}/install.sh | sh -s -- --token ${token}`;
  return (
    <Card className="overflow-hidden">
      <div className="border-b border-line px-5 py-3">
        <p className="text-sm font-semibold text-ink">Enroll a GPU server</p>
        <p className="mt-0.5 text-xs text-ink-muted">Run this on the machine you want to add. It downloads the agent and enrolls it into this organization — no manual setup.</p>
      </div>
      {enrolledNodeName ? (
        <div className="flex items-center gap-2.5 border-b border-emerald-500/20 bg-emerald-500/10 px-5 py-3 text-sm">
          <CheckCircle2 size={16} className="shrink-0 text-emerald-400" />
          <span className="text-ink"><span className="font-medium">{enrolledNodeName}</span> enrolled and online.</span>
        </div>
      ) : (
        <div className="flex items-center gap-2.5 border-b border-line bg-subtle/50 px-5 py-3 text-sm text-ink-muted">
          <Loader2 size={15} className="shrink-0 animate-spin" />
          Waiting for the agent to connect… run the command below, then this updates automatically.
        </div>
      )}
      <div className="space-y-4 p-5">
        <div>
          <p className="mb-1.5 text-xs font-medium text-ink-muted">One-line install</p>
          <div className="flex items-start gap-2 rounded-md border border-line bg-subtle px-3 py-2.5">
            <code className="flex-1 whitespace-pre-wrap break-all font-mono text-xs leading-relaxed text-ink">{installCmd}</code>
            <CopyButton value={installCmd} label="Copy" />
          </div>
          <p className="mt-1.5 text-xs text-ink-subtle">Hosts with NVIDIA GPUs enroll real hardware. On macOS / no GPU it runs in dev mode (synthetic GPUs, real Docker). Keep the process running to stay online.</p>
        </div>
        <div>
          <p className="mb-1.5 text-xs font-medium text-ink-muted">Enrollment token <span className="text-ink-subtle">· reusable for 1 year</span></p>
          <div className="flex items-center gap-2 rounded-md border border-line bg-subtle px-3 py-2">
            <code className="flex-1 truncate font-mono text-sm text-emerald-400">{token}</code>
            <CopyButton value={token} label="Copy" />
          </div>
        </div>
      </div>
    </Card>
  );
}

export default function NodesPage() {
  const { data: nodes, isLoading, error, refetch } = useNodes();
  const { data: gpus } = useGPUs();
  const { data: workloads } = useWorkloads();
  const issueToken = useIssueEnrollmentToken();
  const toast = useToast();
  const [token, setToken] = useState<string | null>(null);
  // Node IDs present when the token was generated — anything new is a fresh enrollment.
  const baselineIds = useRef<Set<string> | null>(null);
  const [enrolledNode, setEnrolledNode] = useState<string | null>(null);

  async function handleIssueToken() {
    baselineIds.current = new Set((nodes ?? []).map((n) => n.id));
    setEnrolledNode(null);
    const res = await issueToken.mutateAsync({});
    setToken(res.token);
  }

  // While the enroll panel is open, watch for a node that wasn't there before and confirm it.
  useEffect(() => {
    if (!token || !baselineIds.current || enrolledNode) return;
    const fresh = (nodes ?? []).find((n) => !baselineIds.current!.has(n.id));
    if (fresh) {
      setEnrolledNode(fresh.hostname);
      toast.success(`Node "${fresh.hostname}" enrolled and online`);
    }
  }, [nodes, token, enrolledNode, toast]);

  const rows = useMemo(() => (nodes ?? []).map((n) => {
    const ng = (gpus ?? []).filter((g) => g.node_id === n.id);
    const total = ng.length || n.gpu_count;
    const available = ng.filter((g) => g.status === "idle").length;
    const allocated = total - available;
    const running = (workloads ?? []).filter((w) => w.node_id === n.id && w.status === "running").length;
    return { node: n, health: deriveHealth(n.status, n.last_seen_at), total, available, allocated, running };
  }), [nodes, gpus, workloads]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Nodes"
        description="Operational status of GPU servers in your fleet."
        actions={
          <>
            <Button variant="ghost" size="icon" onClick={() => refetch()} title="Refresh"><RefreshCw size={15} /></Button>
            <Button variant="primary" size="md" onClick={handleIssueToken} disabled={issueToken.isPending}>Add node</Button>
          </>
        }
      />

      {token && <EnrollPanel token={token} enrolledNodeName={enrolledNode ?? undefined} />}

      {error ? (
        <Card><EmptyState icon={<Server size={20} />} title="Failed to load nodes" action={<Button size="sm" variant="secondary" onClick={() => refetch()}>Retry</Button>} /></Card>
      ) : isLoading ? (
        <Card className="p-10 text-center text-sm text-ink-muted">Loading nodes…</Card>
      ) : (
        <Card className="overflow-hidden">
          {!rows.length ? (
            <EmptyState
              icon={<Server size={20} />}
              title="No nodes enrolled"
              description="Enroll your first GPU server to start collecting inventory and metrics."
              steps={[<>Click <span className="font-medium text-ink">Generate install command</span></>, <>Copy the one-line installer it shows</>, <>Run it on your GPU server (or this Mac) — the node appears here</>]}
              action={<Button variant="primary" size="sm" onClick={handleIssueToken} disabled={issueToken.isPending}>Generate install command</Button>}
            />
          ) : (
            <Table columns={[
              { key: "host", label: "Hostname" }, { key: "health", label: "Health" }, { key: "gpus", label: "GPUs", align: "right" },
              { key: "avail", label: "Available", align: "right" }, { key: "run", label: "Running", align: "right" }, { key: "alloc", label: "Allocation" },
            ]}>
              {rows.map(({ node: n, health, total, available, allocated, running }) => {
                const pct = total > 0 ? Math.round((allocated / total) * 100) : 0;
                return (
                  <Row key={n.id}>
                    <Cell><Link href={`/nodes/${n.id}`} className="font-mono text-sm font-medium text-ink hover:underline">{n.hostname}</Link></Cell>
                    <Cell><Badge tone={toneFor(HEALTH_TONE, health)} dot>{health}</Badge></Cell>
                    <Cell align="right" className="tnum text-ink">{total}</Cell>
                    <Cell align="right" className="tnum">{available}</Cell>
                    <Cell align="right" className="tnum">{running}</Cell>
                    <Cell>
                      <div className="flex items-center gap-2">
                        <Meter className="w-24" value={pct} tone={pct > 85 ? "red" : pct > 0 ? "blue" : "neutral"} />
                        <span className="w-9 text-right text-xs text-ink-muted tnum">{pct}%</span>
                      </div>
                    </Cell>
                  </Row>
                );
              })}
            </Table>
          )}
        </Card>
      )}
    </div>
  );
}
