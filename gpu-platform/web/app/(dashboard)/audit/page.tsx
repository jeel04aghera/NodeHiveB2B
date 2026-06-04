"use client";
import { useState } from "react";
import { useAuditLogs } from "@/lib/queries";
import { ScrollText, RefreshCw } from "lucide-react";
import {
  PageHeader,
  Card,
  Table,
  Row,
  Cell,
  Badge,
  Button,
  EmptyState,
  FormField,
  Input,
  toneFor,
  AUDIT_TONE,
} from "@/components/ui";

function isoDate(d: Date) {
  return d.toISOString().slice(0, 10);
}

export default function AuditPage() {
  const now = new Date();
  const weekAgo = new Date(now.getTime() - 7 * 86400_000);
  const [from, setFrom] = useState(isoDate(weekAgo));
  const [to, setTo] = useState(isoDate(now));

  const fromISO = new Date(from).toISOString();
  const toISO = new Date(to + "T23:59:59Z").toISOString();
  const { data: logs, isLoading, error, refetch } = useAuditLogs(fromISO, toISO);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Audit Log"
        description="A record of who did what, and when."
        actions={
          <Button variant="ghost" size="icon" onClick={() => refetch()} title="Refresh">
            <RefreshCw size={15} />
          </Button>
        }
      />

      <Card className="flex flex-wrap items-end gap-4 p-4">
        <FormField label="From">
          <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="w-auto" />
        </FormField>
        <FormField label="To">
          <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} className="w-auto" />
        </FormField>
      </Card>

      {error ? (
        <Card><EmptyState icon={<ScrollText size={20} />} title="Failed to load audit log" action={<Button size="sm" variant="secondary" onClick={() => refetch()}>Retry</Button>} /></Card>
      ) : isLoading ? (
        <Card className="p-10 text-center text-sm text-ink-muted">Loading audit log…</Card>
      ) : (
        <Card className="overflow-hidden">
          {!logs?.length ? (
            <EmptyState
              icon={<ScrollText size={20} />}
              title="No audit events in this range"
              description="Launch a workload or create a user to generate events, or widen the date range."
            />
          ) : (
            <Table
              columns={[
                { key: "time", label: "Time" },
                { key: "actor", label: "Actor" },
                { key: "action", label: "Action" },
                { key: "target", label: "Target" },
                { key: "details", label: "Details" },
              ]}
            >
              {logs.map((e) => (
                <Row key={e.id}>
                  <Cell className="whitespace-nowrap text-xs">{new Date(e.ts).toLocaleString()}</Cell>
                  <Cell>
                    <span className="text-ink">{e.actor_type}</span>
                    <span className="ml-1.5 font-mono text-xs text-ink-subtle">{e.actor_id.slice(0, 8)}</span>
                  </Cell>
                  <Cell>
                    <Badge tone={toneFor(AUDIT_TONE, e.action)} className="font-mono">
                      {e.action}
                    </Badge>
                  </Cell>
                  <Cell className="font-mono text-xs text-ink-subtle">{e.target_id ? e.target_id.slice(0, 12) : "—"}</Cell>
                  <Cell className="max-w-[20rem] truncate font-mono text-xs text-ink-subtle">
                    {e.metadata && Object.keys(e.metadata).length ? JSON.stringify(e.metadata) : "—"}
                  </Cell>
                </Row>
              ))}
            </Table>
          )}
        </Card>
      )}
    </div>
  );
}
