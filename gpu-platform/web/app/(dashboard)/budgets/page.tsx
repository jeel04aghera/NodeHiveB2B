"use client";
import { useState } from "react";
import {
  useBudgets, useSetBudget, useDeleteBudget, useDepartments, useProjects,
  type Budget,
} from "@/lib/queries";
import { useAuth } from "@/lib/auth";
import {
  PageHeader, Card, Table, Row, Cell, EmptyState, Button, Badge, Modal, Meter,
  FormField, Input, Select, toneFor, type Tone, useToast,
} from "@/components/ui";
import { Target, Plus, Trash2 } from "lucide-react";
import { formatRupees } from "@/lib/currency";

const BUDGET_TONE: Record<string, Tone> = {
  safe: "green",
  at_risk: "amber",
  exceeded: "red",
};

const BUDGET_LABEL: Record<string, string> = {
  safe: "On track",
  at_risk: "At risk",
  exceeded: "Exceeded",
};

function SetModal({ onClose }: { onClose: () => void }) {
  const setBudget = useSetBudget();
  const { data: departments } = useDepartments();
  const { data: projects } = useProjects();
  const toast = useToast();

  const [scopeType, setScopeType] = useState("organization");
  const [scopeId, setScopeId] = useState("");
  const [amount, setAmount] = useState(50000);
  const [err, setErr] = useState("");

  function submit() {
    setErr("");
    if (scopeType !== "organization" && !scopeId) {
      setErr("Pick a " + scopeType);
      return;
    }
    setBudget.mutate(
      { scope_type: scopeType, scope_id: scopeId || undefined, amount },
      {
        onSuccess: () => { toast.success("Budget saved"); onClose(); },
        onError: (e: unknown) => setErr(e instanceof Error ? e.message : "Could not save budget"),
      },
    );
  }

  return (
    <Modal title="Set a budget" onClose={onClose}>
      <div className="space-y-4 p-5">
        <FormField label="Scope" hint="A monthly spending limit for the whole org, a department, or a project.">
          <Select value={scopeType} onChange={(e) => { setScopeType(e.target.value); setScopeId(""); }}>
            <option value="organization">Organization</option>
            <option value="department">Department</option>
            <option value="project">Project</option>
          </Select>
        </FormField>
        {scopeType === "department" && (
          <FormField label="Department" required>
            <Select value={scopeId} onChange={(e) => setScopeId(e.target.value)}>
              <option value="">Select…</option>
              {(departments ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
            </Select>
          </FormField>
        )}
        {scopeType === "project" && (
          <FormField label="Project" required>
            <Select value={scopeId} onChange={(e) => setScopeId(e.target.value)}>
              <option value="">Select…</option>
              {(projects ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          </FormField>
        )}
        <FormField label="Monthly limit (₹)" required>
          <Input type="number" min={0} step={1000} value={amount} onChange={(e) => setAmount(Math.max(0, Number(e.target.value)))} />
        </FormField>
        {err && <p className="text-xs text-red-400">{err}</p>}
      </div>
      <div className="flex justify-end gap-2 border-t border-line px-5 py-3">
        <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
        <Button variant="primary" size="sm" onClick={submit} disabled={setBudget.isPending}>
          {setBudget.isPending ? "Saving…" : "Save budget"}
        </Button>
      </div>
    </Modal>
  );
}

export default function BudgetsPage() {
  const { user } = useAuth();
  const isAdmin = user?.role === "admin";
  const { data, isLoading } = useBudgets();
  const del = useDeleteBudget();
  const [showSet, setShowSet] = useState(false);
  const rows: Budget[] = data ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Budgets"
        description="Set monthly spending limits per organization, department, or project. Spend, forecast, and burn rate update from real workload costs."
        actions={isAdmin && <Button variant="primary" size="sm" onClick={() => setShowSet(true)}><Plus size={14} /> Set budget</Button>}
      />

      <Card>
        {isLoading ? (
          <div className="p-5 text-sm text-ink-muted">Loading budgets…</div>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<Target size={18} />}
            title="No budgets set"
            description="Define a monthly limit to track spend against a target and get alerted before you run over."
            action={isAdmin && <Button variant="primary" size="sm" onClick={() => setShowSet(true)}><Plus size={14} /> Set budget</Button>}
          />
        ) : (
          <Table
            columns={[
              { key: "scope", label: "Scope" },
              { key: "status", label: "Status" },
              { key: "usage", label: "Usage" },
              { key: "spend", label: "Spend / Limit", align: "right" },
              { key: "forecast", label: "Forecast", align: "right" },
              { key: "burn", label: "Burn/day", align: "right" },
              { key: "action", label: "", align: "right" },
            ]}
          >
            {rows.map((b) => (
              <Row key={b.id}>
                <Cell className="text-ink">
                  <span className="font-medium">{b.scope_name}</span>
                  <span className="ml-2 text-[11px] uppercase tracking-wide text-ink-subtle">{b.scope_type}</span>
                </Cell>
                <Cell><Badge tone={toneFor(BUDGET_TONE, b.status)} dot>{BUDGET_LABEL[b.status] ?? b.status}</Badge></Cell>
                <Cell className="w-48">
                  <Meter value={Math.min(b.used_pct, 100)} tone={toneFor(BUDGET_TONE, b.status)} />
                  <span className="mt-1 block text-[11px] text-ink-subtle tnum">{b.used_pct.toFixed(1)}% used</span>
                </Cell>
                <Cell align="right" className="tnum text-ink">{formatRupees(b.spend)} <span className="text-ink-subtle">/ {formatRupees(b.amount)}</span></Cell>
                <Cell align="right" className="tnum">{formatRupees(b.forecast)}</Cell>
                <Cell align="right" className="tnum">{formatRupees(b.burn_per_day)}</Cell>
                <Cell align="right">
                  {isAdmin && (
                    <Button variant="ghost" size="sm" onClick={() => del.mutate(b.id)} disabled={del.isPending} title="Remove budget">
                      <Trash2 size={13} />
                    </Button>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      {showSet && <SetModal onClose={() => setShowSet(false)} />}
    </div>
  );
}
