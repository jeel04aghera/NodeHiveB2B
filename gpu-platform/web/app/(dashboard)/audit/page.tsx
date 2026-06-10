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
  Select,
  toneFor,
  AUDIT_TONE,
} from "@/components/ui";

function isoDate(d: Date) {
  return d.toISOString().slice(0, 10);
}

// Action-prefix categories (server does prefix matching on `action`).
const CATEGORIES = [
  { value: "", label: "All events" },
  { value: "auth", label: "Authentication" },
  { value: "session", label: "Sessions" },
  { value: "org", label: "Organization" },
  { value: "member", label: "Members & roles" },
  { value: "invitation", label: "Invitations" },
  { value: "join_code", label: "Join codes" },
  { value: "node", label: "Nodes" },
  { value: "enrollment_token", label: "Enrollment tokens" },
  { value: "workload", label: "Workloads" },
  { value: "billing", label: "Billing" },
  { value: "user", label: "Users" },
  { value: "template", label: "Templates" },
  { value: "department", label: "Departments" },
  { value: "project", label: "Projects" },
];

const PAGE_SIZE = 50;

export default function AuditPage() {
  const now = new Date();
  const monthAgo = new Date(now.getTime() - 30 * 86400_000);
  const [from, setFrom] = useState(isoDate(monthAgo));
  const [to, setTo] = useState(isoDate(now));
  const [q, setQ] = useState("");
  const [action, setAction] = useState("");
  const [offset, setOffset] = useState(0);

  const fromISO = new Date(from).toISOString();
  const toISO = new Date(to + "T23:59:59Z").toISOString();
  const { data, isLoading, error, refetch } = useAuditLogs({
    from: fromISO,
    to: toISO,
    q,
    action,
    limit: PAGE_SIZE,
    offset,
  });
  const logs = data?.items;
  const total = data?.total ?? 0;
  const page = Math.floor(offset / PAGE_SIZE) + 1;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  // Any filter change resets paging so results never start beyond the match count.
  const setFilter = (fn: () => void) => {
    fn();
    setOffset(0);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Audit Log"
        description="A searchable, tamper-evident record of who did what, and when."
        actions={
          <Button variant="ghost" size="icon" onClick={() => refetch()} title="Refresh">
            <RefreshCw size={15} />
          </Button>
        }
      />

      <Card className="flex flex-wrap items-end gap-4 p-4">
        <FormField label="Search">
          <Input
            placeholder="Actor, target, action, metadata…"
            value={q}
            onChange={(e) => setFilter(() => setQ(e.target.value))}
            className="w-64"
          />
        </FormField>
        <FormField label="Category">
          <Select
            value={action}
            onChange={(e) => setFilter(() => setAction(e.target.value))}
            className="w-44"
          >
            {CATEGORIES.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </Select>
        </FormField>
        <FormField label="From">
          <Input type="date" value={from} onChange={(e) => setFilter(() => setFrom(e.target.value))} className="w-auto" />
        </FormField>
        <FormField label="To">
          <Input type="date" value={to} onChange={(e) => setFilter(() => setTo(e.target.value))} className="w-auto" />
        </FormField>
        <div className="ml-auto pb-2 text-xs text-ink-subtle tnum">
          {total} event{total === 1 ? "" : "s"}
        </div>
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
              title="No audit events match"
              description="Adjust the search, category or date range — or perform an action to generate events."
            />
          ) : (
            <>
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
                      <span className="ml-1.5 font-mono text-xs text-ink-subtle">{e.actor_id ? e.actor_id.slice(0, 8) : "—"}</span>
                    </Cell>
                    <Cell>
                      <Badge tone={toneFor(AUDIT_TONE, e.action)} className="font-mono">
                        {e.action}
                      </Badge>
                    </Cell>
                    <Cell className="font-mono text-xs text-ink-subtle">
                      {e.target_id ? `${e.target_type ? e.target_type + " · " : ""}${e.target_id.slice(0, 12)}` : "—"}
                    </Cell>
                    <Cell className="max-w-[20rem] truncate font-mono text-xs text-ink-subtle">
                      {e.metadata && Object.keys(e.metadata).length ? JSON.stringify(e.metadata) : "—"}
                    </Cell>
                  </Row>
                ))}
              </Table>
              {pages > 1 && (
                <div className="flex items-center justify-between border-t border-line px-4 py-3 text-xs text-ink-muted">
                  <span className="tnum">
                    Page {page} of {pages}
                  </span>
                  <div className="flex gap-2">
                    <Button size="sm" variant="secondary" disabled={offset === 0}
                      onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>
                      Previous
                    </Button>
                    <Button size="sm" variant="secondary" disabled={offset + PAGE_SIZE >= total}
                      onClick={() => setOffset(offset + PAGE_SIZE)}>
                      Next
                    </Button>
                  </div>
                </div>
              )}
            </>
          )}
        </Card>
      )}
    </div>
  );
}
