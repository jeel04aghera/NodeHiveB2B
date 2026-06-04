"use client";
import { useState } from "react";
import {
  useReservations, useCreateReservation, useCancelReservation, useProjects, useGPUs,
  type Reservation,
} from "@/lib/queries";
import {
  PageHeader, Card, Table, Row, Cell, EmptyState, Button, Badge, Modal,
  FormField, Input, Select, toneFor, type Tone, useToast,
} from "@/components/ui";
import { CalendarClock, Plus } from "lucide-react";

const RES_TONE: Record<string, Tone> = {
  active: "green",
  upcoming: "blue",
  expired: "neutral",
  cancelled: "neutral",
};

function toLocalInput(d: Date) {
  // datetime-local wants YYYY-MM-DDTHH:mm in local time.
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function CreateModal({ onClose }: { onClose: () => void }) {
  const create = useCreateReservation();
  const { data: projects } = useProjects();
  const { data: gpus } = useGPUs();
  const toast = useToast();
  const models = Array.from(new Set((gpus ?? []).map((g) => g.model)));
  const now = new Date();
  const in2h = new Date(now.getTime() + 2 * 3600_000);

  const [gpuModel, setGpuModel] = useState("any");
  const [gpuCount, setGpuCount] = useState(1);
  const [start, setStart] = useState(toLocalInput(now));
  const [end, setEnd] = useState(toLocalInput(in2h));
  const [projectId, setProjectId] = useState("");
  const [err, setErr] = useState("");

  function submit() {
    setErr("");
    create.mutate(
      {
        gpu_model: gpuModel,
        gpu_count: gpuCount,
        start_at: new Date(start).toISOString(),
        end_at: new Date(end).toISOString(),
        project_id: projectId || undefined,
      },
      {
        onSuccess: () => { toast.success("Reservation created"); onClose(); },
        onError: (e: unknown) => setErr(e instanceof Error ? e.message : "Could not create reservation"),
      },
    );
  }

  return (
    <Modal title="Reserve GPU capacity" onClose={onClose}>
      <div className="space-y-4 p-5">
        <div className="grid grid-cols-2 gap-4">
          <FormField label="GPU model">
            <Select value={gpuModel} onChange={(e) => setGpuModel(e.target.value)}>
              <option value="any">Any model</option>
              {models.map((m) => <option key={m} value={m}>{m}</option>)}
            </Select>
          </FormField>
          <FormField label="GPU count" required>
            <Input type="number" min={1} value={gpuCount} onChange={(e) => setGpuCount(Math.max(1, Number(e.target.value)))} />
          </FormField>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <FormField label="Start" required>
            <Input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
          </FormField>
          <FormField label="End" required>
            <Input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
          </FormField>
        </div>
        <FormField label="Project" hint="Optional — ties the reservation to a project for planning.">
          <Select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
            <option value="">No project</option>
            {(projects ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </Select>
        </FormField>
        {err && <p className="text-xs text-red-400">{err}</p>}
      </div>
      <div className="flex justify-end gap-2 border-t border-line px-5 py-3">
        <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
        <Button variant="primary" size="sm" onClick={submit} disabled={create.isPending}>
          {create.isPending ? "Reserving…" : "Reserve"}
        </Button>
      </div>
    </Modal>
  );
}

export default function ReservationsPage() {
  const { data, isLoading } = useReservations();
  const cancel = useCancelReservation();
  const [showCreate, setShowCreate] = useState(false);
  const rows: Reservation[] = data ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Reservations"
        description="Hold GPU capacity for a future window so planned training runs are guaranteed hardware. Overlapping reservations can't exceed fleet size."
        actions={<Button variant="primary" size="sm" onClick={() => setShowCreate(true)}><Plus size={14} /> New reservation</Button>}
      />

      <Card>
        {isLoading ? (
          <div className="p-5 text-sm text-ink-muted">Loading reservations…</div>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<CalendarClock size={18} />}
            title="No reservations yet"
            description="Reserve GPUs ahead of time to guarantee capacity for scheduled jobs."
            action={<Button variant="primary" size="sm" onClick={() => setShowCreate(true)}><Plus size={14} /> New reservation</Button>}
          />
        ) : (
          <Table
            columns={[
              { key: "status", label: "Status" },
              { key: "gpu", label: "GPUs" },
              { key: "window", label: "Window" },
              { key: "project", label: "Project" },
              { key: "owner", label: "Reserved by" },
              { key: "action", label: "", align: "right" },
            ]}
          >
            {rows.map((r) => (
              <Row key={r.id}>
                <Cell><Badge tone={toneFor(RES_TONE, r.status)} dot>{r.status}</Badge></Cell>
                <Cell className="text-ink">{r.gpu_count}× {r.gpu_model === "any" ? "any model" : r.gpu_model}</Cell>
                <Cell className="tnum text-xs">
                  {new Date(r.start_at).toLocaleString()} → {new Date(r.end_at).toLocaleString()}
                </Cell>
                <Cell>{r.project_name || <span className="text-ink-subtle">—</span>}</Cell>
                <Cell>{r.owner_email || "—"}</Cell>
                <Cell align="right">
                  {(r.status === "upcoming" || r.status === "active") && (
                    <Button variant="ghost" size="sm" onClick={() => cancel.mutate(r.id)} disabled={cancel.isPending}>
                      Cancel
                    </Button>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      {showCreate && <CreateModal onClose={() => setShowCreate(false)} />}
    </div>
  );
}
