"use client";
import Link from "next/link";
import { useQueue, useStopWorkload } from "@/lib/queries";
import { PageHeader, Card, Table, Row, Cell, StatCard, EmptyState, Button, Badge } from "@/components/ui";
import { ListOrdered, Clock, Layers, X } from "lucide-react";

function fmtWait(min: number) {
  if (min <= 0) return "next up";
  if (min < 60) return `~${min}m`;
  const h = Math.floor(min / 60), m = min % 60;
  return `~${h}h${m ? ` ${m}m` : ""}`;
}

export default function QueuePage() {
  const { data, isLoading } = useQueue();
  const cancel = useStopWorkload();
  const entries = data?.entries ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="GPU Queue"
        description="Workloads waiting for capacity. When a GPU frees up the next workload starts automatically — no resubmission needed."
      />

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
        <StatCard label="Waiting" value={String(data?.waiting ?? 0)} icon={<ListOrdered size={15} />} />
        <StatCard label="Avg. time per slot" value={fmtWait(data?.avg_wait_min ?? 0)} icon={<Clock size={15} />} />
        <StatCard
          label="Next free"
          value={data?.next_free_at ? new Date(data.next_free_at).toLocaleTimeString() : "—"}
          icon={<Layers size={15} />}
        />
      </div>

      <Card>
        {isLoading ? (
          <div className="p-5 text-sm text-ink-muted">Loading queue…</div>
        ) : entries.length === 0 ? (
          <EmptyState
            icon={<ListOrdered size={18} />}
            title="The queue is empty"
            description="Every workload found capacity. New launches only queue when no matching idle GPU is available."
            action={<Link href="/workloads?launch=1"><Button variant="primary" size="sm">Launch a workload</Button></Link>}
          />
        ) : (
          <Table
            columns={[
              { key: "pos", label: "#", align: "right" },
              { key: "name", label: "Workload" },
              { key: "gpu", label: "GPUs" },
              { key: "owner", label: "Owner" },
              { key: "project", label: "Project" },
              { key: "wait", label: "Est. wait", align: "right" },
              { key: "start", label: "Est. start", align: "right" },
              { key: "action", label: "", align: "right" },
            ]}
          >
            {entries.map((e) => (
              <Row key={e.id}>
                <Cell align="right" className="tnum text-ink">{e.position}</Cell>
                <Cell>
                  <Link href={`/workloads/${e.id}`} className="font-medium text-ink hover:underline">{e.name}</Link>
                </Cell>
                <Cell>
                  <Badge tone="blue">{e.gpu_count}× {e.gpu_type === "any" ? "any" : e.gpu_type}</Badge>
                </Cell>
                <Cell>{e.owner_email || "—"}</Cell>
                <Cell>{e.project_name || <span className="text-ink-subtle">—</span>}</Cell>
                <Cell align="right" className="tnum">{fmtWait(e.est_wait_min)}</Cell>
                <Cell align="right" className="tnum">{new Date(e.est_start).toLocaleTimeString()}</Cell>
                <Cell align="right">
                  <Button variant="ghost" size="sm" onClick={() => cancel.mutate(e.id)} disabled={cancel.isPending} title="Remove from queue">
                    <X size={13} /> Cancel
                  </Button>
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      {entries.length > 0 && (
        <p className="text-xs text-ink-subtle">
          Wait times are estimates derived from the average runtime of currently-running workloads — actual start depends on when a GPU frees up.
        </p>
      )}
    </div>
  );
}
