"use client";
import { useState } from "react";
import {
  useAlerts, useAckAlert, useAlertRules, useCreateAlertRule, useToggleAlertRule, useDeleteAlertRule,
  useProjects, useDepartments, type Alert, type AlertRule, type AlertType,
} from "@/lib/queries";
import { useAuth, isAdminRole } from "@/lib/auth";
import {
  PageHeader, Card, CardHeader, Table, Row, Cell, EmptyState, Button, Badge,
  Modal, FormField, Input, Select, type Tone, useToast,
} from "@/components/ui";
import { BellRing, Plus, Trash2, Check } from "lucide-react";

const SEV_TONE: Record<string, Tone> = { info: "blue", warning: "amber", critical: "red" };

const RULE_META: Record<AlertType, { label: string; unit: string; scope: "project" | "department" | "none" }> = {
  project_spend:      { label: "Project monthly spend", unit: "₹ this month", scope: "project" },
  department_spend:   { label: "Department monthly spend", unit: "₹ this month", scope: "department" },
  budget_utilization: { label: "Budget utilization", unit: "% of any budget", scope: "none" },
  workload_runtime:   { label: "Long-running workload", unit: "hours running", scope: "none" },
  idle_workload:      { label: "Idle workload", unit: "hours idle (<5% util)", scope: "none" },
};

function RuleModal({ onClose }: { onClose: () => void }) {
  const create = useCreateAlertRule();
  const { data: projects } = useProjects();
  const { data: departments } = useDepartments();
  const toast = useToast();

  const [type, setType] = useState<AlertType>("budget_utilization");
  const [threshold, setThreshold] = useState(80);
  const [scopeId, setScopeId] = useState("");
  const [severity, setSeverity] = useState("warning");
  const [err, setErr] = useState("");
  const meta = RULE_META[type];

  function submit() {
    setErr("");
    if (meta.scope !== "none" && !scopeId) { setErr(`Select a ${meta.scope}`); return; }
    if (threshold <= 0) { setErr("Threshold must be positive"); return; }
    create.mutate(
      { type, threshold, scope_id: scopeId || undefined, severity },
      {
        onSuccess: () => { toast.success("Alert rule created"); onClose(); },
        onError: (e: unknown) => setErr(e instanceof Error ? e.message : "Could not create rule"),
      },
    );
  }

  return (
    <Modal title="New alert rule" onClose={onClose}>
      <div className="space-y-4 p-5">
        <FormField label="Condition">
          <Select value={type} onChange={(e) => { setType(e.target.value as AlertType); setScopeId(""); }}>
            {(Object.keys(RULE_META) as AlertType[]).map((t) => (
              <option key={t} value={t}>{RULE_META[t].label}</option>
            ))}
          </Select>
        </FormField>
        {meta.scope === "project" && (
          <FormField label="Project" required>
            <Select value={scopeId} onChange={(e) => setScopeId(e.target.value)}>
              <option value="">Select…</option>
              {(projects ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          </FormField>
        )}
        {meta.scope === "department" && (
          <FormField label="Department" required>
            <Select value={scopeId} onChange={(e) => setScopeId(e.target.value)}>
              <option value="">Select…</option>
              {(departments ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
            </Select>
          </FormField>
        )}
        <div className="grid grid-cols-2 gap-4">
          <FormField label="Threshold" hint={meta.unit} required>
            <Input type="number" min={0} value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} />
          </FormField>
          <FormField label="Severity">
            <Select value={severity} onChange={(e) => setSeverity(e.target.value)}>
              <option value="info">Info</option>
              <option value="warning">Warning</option>
              <option value="critical">Critical</option>
            </Select>
          </FormField>
        </div>
        {err && <p className="text-xs text-red-400">{err}</p>}
      </div>
      <div className="flex justify-end gap-2 border-t border-line px-5 py-3">
        <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
        <Button variant="primary" size="sm" onClick={submit} disabled={create.isPending}>
          {create.isPending ? "Creating…" : "Create rule"}
        </Button>
      </div>
    </Modal>
  );
}

export default function AlertsPage() {
  const { user } = useAuth();
  const isAdmin = isAdminRole(user?.role);
  const { data: alerts } = useAlerts(false);
  const { data: rules } = useAlertRules();
  const ack = useAckAlert();
  const toggle = useToggleAlertRule();
  const delRule = useDeleteAlertRule();
  const [showRule, setShowRule] = useState(false);

  const active: Alert[] = alerts ?? [];
  const ruleRows: AlertRule[] = rules ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Cost Alerts"
        description="Rules watch spend, budgets, and runtime continuously. When a threshold is crossed an alert is raised here — automatically and deduplicated."
        actions={isAdmin && <Button variant="primary" size="sm" onClick={() => setShowRule(true)}><Plus size={14} /> New rule</Button>}
      />

      <Card>
        <CardHeader title={`Active alerts${active.length ? ` (${active.length})` : ""}`} />
        {active.length === 0 ? (
          <EmptyState
            icon={<BellRing size={18} />}
            title="No active alerts"
            description="Everything is within thresholds. Alerts will appear here when a rule fires."
          />
        ) : (
          <Table
            columns={[
              { key: "sev", label: "Severity" },
              { key: "title", label: "Alert" },
              { key: "when", label: "Raised", align: "right" },
              { key: "action", label: "", align: "right" },
            ]}
          >
            {active.map((a) => (
              <Row key={a.id}>
                <Cell><Badge tone={SEV_TONE[a.severity] ?? "neutral"} dot>{a.severity}</Badge></Cell>
                <Cell>
                  <div className="font-medium text-ink">{a.title}</div>
                  {a.message && <div className="text-xs text-ink-muted">{a.message}</div>}
                </Cell>
                <Cell align="right" className="tnum text-xs">{new Date(a.created_at).toLocaleString()}</Cell>
                <Cell align="right">
                  <Button variant="ghost" size="sm" onClick={() => ack.mutate(a.id)} disabled={ack.isPending}>
                    <Check size={13} /> Acknowledge
                  </Button>
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      <Card>
        <CardHeader title="Alert rules" meta={isAdmin ? undefined : "Only admins can edit rules."} />
        {ruleRows.length === 0 ? (
          <EmptyState
            icon={<BellRing size={18} />}
            title="No rules yet"
            description="Create a rule to start monitoring spend, budgets, or workload runtime."
            action={isAdmin && <Button variant="primary" size="sm" onClick={() => setShowRule(true)}><Plus size={14} /> New rule</Button>}
          />
        ) : (
          <Table
            columns={[
              { key: "cond", label: "Condition" },
              { key: "scope", label: "Scope" },
              { key: "thresh", label: "Threshold", align: "right" },
              { key: "sev", label: "Severity" },
              { key: "enabled", label: "Status" },
              { key: "action", label: "", align: "right" },
            ]}
          >
            {ruleRows.map((r) => (
              <Row key={r.id}>
                <Cell className="text-ink">{RULE_META[r.type]?.label ?? r.type}</Cell>
                <Cell>{r.scope_name || <span className="text-ink-subtle">—</span>}</Cell>
                <Cell align="right" className="tnum text-ink">
                  {r.threshold} <span className="text-[11px] text-ink-subtle">{RULE_META[r.type]?.unit}</span>
                </Cell>
                <Cell><Badge tone={SEV_TONE[r.severity] ?? "neutral"}>{r.severity}</Badge></Cell>
                <Cell><Badge tone={r.enabled ? "green" : "neutral"} dot>{r.enabled ? "enabled" : "off"}</Badge></Cell>
                <Cell align="right">
                  {isAdmin && (
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" onClick={() => toggle.mutate({ id: r.id, enabled: !r.enabled })} disabled={toggle.isPending}>
                        {r.enabled ? "Disable" : "Enable"}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => delRule.mutate(r.id)} disabled={delRule.isPending} title="Delete rule">
                        <Trash2 size={13} />
                      </Button>
                    </div>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      {showRule && <RuleModal onClose={() => setShowRule(false)} />}
    </div>
  );
}
