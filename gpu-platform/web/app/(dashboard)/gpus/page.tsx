"use client";
import { useState } from "react";
import { useGPUs } from "@/lib/queries";
import { Cpu, RefreshCw } from "lucide-react";
import {
  PageHeader,
  Card,
  Table,
  Row,
  Cell,
  Badge,
  Button,
  EmptyState,
  cn,
  toneFor,
  GPU_TONE,
} from "@/components/ui";

function fmtMem(mb: number) {
  if (mb >= 1024) return `${(mb / 1024).toFixed(0)} GB`;
  return `${mb} MB`;
}

type FilterStatus = "all" | "idle" | "in_use";

export default function GPUsPage() {
  const [filter, setFilter] = useState<FilterStatus>("all");
  const { data: gpus, isLoading, error, refetch } = useGPUs(filter === "all" ? undefined : filter);

  const filters: { value: FilterStatus; label: string }[] = [
    { value: "all", label: "All" },
    { value: "idle", label: "Idle" },
    { value: "in_use", label: "In use" },
  ];

  return (
    <div className="space-y-6">
      <PageHeader
        title="GPUs"
        description={gpus ? `${gpus.length} GPU${gpus.length !== 1 ? "s" : ""} in inventory` : "GPU inventory across the fleet"}
        actions={
          <Button variant="ghost" size="icon" onClick={() => refetch()} title="Refresh">
            <RefreshCw size={15} />
          </Button>
        }
      />

      <div className="inline-flex rounded-md border border-line bg-surface p-0.5">
        {filters.map(({ value, label }) => (
          <button
            key={value}
            onClick={() => setFilter(value)}
            className={cn(
              "rounded px-3 py-1.5 text-sm font-medium transition-colors",
              filter === value ? "bg-subtle text-ink" : "text-ink-muted hover:text-ink",
            )}
          >
            {label}
          </button>
        ))}
      </div>

      {error ? (
        <Card><EmptyState icon={<Cpu size={20} />} title="Failed to load GPUs" action={<Button size="sm" variant="secondary" onClick={() => refetch()}>Retry</Button>} /></Card>
      ) : isLoading ? (
        <Card className="p-10 text-center text-sm text-ink-muted">Loading GPUs…</Card>
      ) : (
        <Card className="overflow-hidden">
          {!gpus || gpus.length === 0 ? (
            <EmptyState
              icon={<Cpu size={20} />}
              title="No GPUs found"
              description="GPUs are discovered automatically when an enrolled node sends its inventory."
            />
          ) : (
            <Table
              columns={[
                { key: "i", label: "#", align: "right" },
                { key: "model", label: "Model" },
                { key: "mem", label: "Memory", align: "right" },
                { key: "status", label: "Status" },
                { key: "mig", label: "MIG" },
                { key: "uuid", label: "UUID" },
                { key: "updated", label: "Updated" },
              ]}
            >
              {gpus.map((g) => (
                <Row key={g.id}>
                  <Cell align="right" className="tnum">{g.index}</Cell>
                  <Cell>
                    <span className="inline-flex items-center gap-2 font-medium text-ink">
                      {g.model}
                      {g.synthetic && <Badge tone="amber" className="text-[10px]">synthetic</Badge>}
                    </span>
                  </Cell>
                  <Cell align="right" className="tnum text-ink">{fmtMem(g.memory_mb)}</Cell>
                  <Cell>
                    <Badge tone={toneFor(GPU_TONE, g.status)} dot>
                      {g.status.replace("_", " ")}
                    </Badge>
                  </Cell>
                  <Cell className="text-xs">
                    {g.mig_enabled ? <span className="text-amber-400">enabled</span> : <span className="text-ink-subtle">—</span>}
                  </Cell>
                  <Cell className="max-w-[16rem] truncate font-mono text-xs text-ink-subtle">{g.uuid}</Cell>
                  <Cell className="text-xs text-ink-subtle">{new Date(g.updated_at).toLocaleString()}</Cell>
                </Row>
              ))}
            </Table>
          )}
        </Card>
      )}
    </div>
  );
}
