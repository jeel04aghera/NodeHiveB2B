"use client";
import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
  useRates, useSetRate, useIssueEnrollmentToken,
  useDepartments, useCreateDepartment,
  useEnrollmentTokens, useRevokeToken, useTemplates, useDeploymentConfig,
} from "@/lib/queries";
import { useAuth, isAdminRole } from "@/lib/auth";
import { DISPLAY_CURRENCY } from "@/lib/currency";
import { Plus, Trash2, Building2 } from "lucide-react";
import {
  PageHeader, Card, CardHeader, Table, Row, Cell, Badge, Button,
  Input, Select, FormField, CopyButton, Tabs, toneFor, TOKEN_TONE,
} from "@/components/ui";
import { SecuritySessions } from "@/components/SecuritySessions";
import { OrgMembers } from "@/components/OrgMembers";

// Admin tabs gate behind admin/owner; Security (own sessions) is available to everyone.
const ADMIN_TABS = [
  { key: "general", label: "General" },
  { key: "organization", label: "Organization" },
  { key: "departments", label: "Departments" },
  { key: "billing", label: "Billing" },
  { key: "templates", label: "Templates" },
  { key: "enrollment", label: "Node Enrollment" },
];
const SECURITY_TAB = { key: "security", label: "Security" };

function SettingsContent() {
  const { user } = useAuth();
  const isAdmin = isAdminRole(user?.role);
  const router = useRouter();
  const search = useSearchParams();
  const TABS = isAdmin ? [...ADMIN_TABS, SECURITY_TAB] : [SECURITY_TAB];
  const defaultTab = isAdmin ? "general" : "security";
  const tab = TABS.some((t) => t.key === search.get("tab")) ? (search.get("tab") as string) : defaultTab;
  const setTab = (k: string) => router.push(k === "general" ? "/settings" : `/settings?tab=${k}`);

  const { data: rates, isLoading: ratesLoading } = useRates();
  const setRate = useSetRate();
  const [rateForm, setRateForm] = useState({ gpu_model: "", rate_per_gpu_hour: "", currency: "USD" });
  const [rateError, setRateError] = useState("");

  const { data: departments } = useDepartments();
  const createDept = useCreateDepartment();
  const [deptForm, setDeptForm] = useState({ name: "", description: "" });

  const issueToken = useIssueEnrollmentToken();
  const { data: tokens } = useEnrollmentTokens();
  const revokeToken = useRevokeToken();
  const [tokenForm, setTokenForm] = useState({ description: "", ttl_days: 365, max_uses: 100 });
  const [token, setToken] = useState<string | null>(null);
  const serverUrl = "https://nodehive.company.com";

  const { data: templates } = useTemplates();
  const { data: cfg } = useDeploymentConfig();

  async function handleSetRate(e: React.FormEvent) {
    e.preventDefault(); setRateError("");
    const rate = parseFloat(rateForm.rate_per_gpu_hour);
    if (isNaN(rate) || rate <= 0) { setRateError("Enter a valid positive rate."); return; }
    try { await setRate.mutateAsync({ gpu_model: rateForm.gpu_model, rate_per_gpu_hour: rate, currency: rateForm.currency }); setRateForm({ gpu_model: "", rate_per_gpu_hour: "", currency: "USD" }); }
    catch { setRateError("Failed to save rate card."); }
  }
  async function handleCreateDept(e: React.FormEvent) { e.preventDefault(); if (!deptForm.name) return; await createDept.mutateAsync(deptForm); setDeptForm({ name: "", description: "" }); }
  async function handleIssueToken() { const res = await issueToken.mutateAsync(tokenForm); setToken(res.token); }

  // Non-admins still manage their own active sessions under Security.
  if (!isAdmin) {
    return (
      <div className="space-y-6">
        <PageHeader title="Settings" description="Manage your account security." />
        <Tabs items={TABS} value={tab} onChange={setTab} />
        <SecuritySessions />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader title="Settings" description="Organization configuration and administration." />
      <Tabs items={TABS} value={tab} onChange={setTab} />

      {tab === "security" && <SecuritySessions />}

      {tab === "general" && (
        <div className="grid gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader title="Organization" />
            <dl className="divide-y divide-line">
              {[
                ["Display currency", DISPLAY_CURRENCY],
                ["Environment", cfg?.dev_mode ? "Development (synthetic fleet)" : "Production"],
                ["Total GPUs", String(cfg?.total_gpu_count ?? "—")],
                ["Your role", "Administrator"],
              ].map(([k, v]) => (
                <div key={k} className="flex items-center justify-between px-5 py-3 text-sm">
                  <dt className="text-ink-muted">{k}</dt>
                  <dd className="font-medium text-ink">{v}</dd>
                </div>
              ))}
            </dl>
          </Card>
          <Card>
            <CardHeader title="About" />
            <div className="space-y-2 p-5 text-sm text-ink-muted">
              <p>NodeHive is a private GPU cloud for your organization. Manage workloads, capacity, chargeback, and enrollment from one operational console.</p>
              <p>Billing is shown in {DISPLAY_CURRENCY}. GPU-hour rates are configured under the <button onClick={() => setTab("billing")} className="font-medium text-ink hover:underline">Billing</button> tab.</p>
            </div>
          </Card>
        </div>
      )}

      {tab === "organization" && <OrgMembers />}

      {tab === "departments" && (
        <div className="space-y-4">
          <Card className="overflow-hidden">
            <CardHeader title="Departments" />
            {!departments?.length ? <div className="py-6 text-center text-sm text-ink-muted">No departments yet.</div> : (
              <Table columns={[{ key: "n", label: "Name" }, { key: "d", label: "Description" }, { key: "m", label: "Members", align: "right" }, { key: "w", label: "Active workloads", align: "right" }]}>
                {departments.map((d) => (
                  <Row key={d.id}>
                    <Cell><span className="inline-flex items-center gap-2 font-medium text-ink"><Building2 size={14} className="text-ink-subtle" />{d.name}</span></Cell>
                    <Cell>{d.description || "—"}</Cell>
                    <Cell align="right" className="tnum">{d.user_count}</Cell>
                    <Cell align="right" className="tnum">{d.workload_count}</Cell>
                  </Row>
                ))}
              </Table>
            )}
          </Card>
          <Card className="p-5">
            <h3 className="mb-4 flex items-center gap-2 text-sm font-medium text-ink"><Plus size={14} /> Add department</h3>
            <form onSubmit={handleCreateDept}>
              <div className="grid grid-cols-2 gap-3">
                <Input value={deptForm.name} onChange={(e) => setDeptForm({ ...deptForm, name: e.target.value })} placeholder="Department name (e.g. AI)" required />
                <Input value={deptForm.description} onChange={(e) => setDeptForm({ ...deptForm, description: e.target.value })} placeholder="Description (optional)" />
              </div>
              <Button type="submit" variant="primary" className="mt-4" disabled={createDept.isPending}>{createDept.isPending ? "Saving…" : "Add department"}</Button>
            </form>
          </Card>
        </div>
      )}

      {tab === "billing" && (
        <div className="space-y-4">
          <Card className="overflow-hidden">
            <CardHeader title="Rate Cards" meta="GPU-hour cost per model — used for chargeback" />
            {ratesLoading ? <div className="py-6 text-center text-sm text-ink-muted">Loading rates…</div> : !rates?.length ? <div className="py-6 text-center text-sm text-ink-muted">No rate cards yet.</div> : (
              <Table columns={[{ key: "m", label: "GPU Model" }, { key: "r", label: "Rate / GPU-hour", align: "right" }, { key: "c", label: "Currency" }, { key: "e", label: "Effective from" }]}>
                {rates.map((rc) => (
                  <Row key={rc.id}>
                    <Cell className="font-medium text-ink">{rc.gpu_model}</Cell>
                    <Cell align="right" className="tnum">{rc.rate_per_gpu_hour.toFixed(4)}</Cell>
                    <Cell>{rc.currency}</Cell>
                    <Cell className="text-xs">{new Date(rc.effective_from).toLocaleDateString()}</Cell>
                  </Row>
                ))}
              </Table>
            )}
          </Card>
          <Card className="p-5">
            <h3 className="mb-1 flex items-center gap-2 text-sm font-medium text-ink"><Plus size={14} /> Add / update rate</h3>
            <p className="mb-4 text-xs text-ink-subtle">Rates are stored in their configured currency; the dashboard displays spend in {DISPLAY_CURRENCY}.</p>
            <form onSubmit={handleSetRate}>
              <div className="grid grid-cols-3 gap-3">
                <FormField label="GPU Model"><Input value={rateForm.gpu_model} onChange={(e) => setRateForm({ ...rateForm, gpu_model: e.target.value })} placeholder="NVIDIA A100" required /></FormField>
                <FormField label="Rate / GPU-hour"><Input type="number" step="0.0001" min="0" value={rateForm.rate_per_gpu_hour} onChange={(e) => setRateForm({ ...rateForm, rate_per_gpu_hour: e.target.value })} placeholder="3.50" required /></FormField>
                <FormField label="Currency"><Select value={rateForm.currency} onChange={(e) => setRateForm({ ...rateForm, currency: e.target.value })}><option value="USD">USD</option><option value="INR">INR</option><option value="EUR">EUR</option></Select></FormField>
              </div>
              {rateError && <p className="mt-3 text-sm text-red-400">{rateError}</p>}
              <Button type="submit" variant="primary" className="mt-4" disabled={setRate.isPending}>{setRate.isPending ? "Saving…" : "Save rate"}</Button>
            </form>
          </Card>
        </div>
      )}

      {tab === "templates" && (
        <Card className="overflow-hidden">
          <CardHeader title="Templates" meta={`${templates?.length ?? 0} available`} actions={<a href="/templates" className="text-xs text-ink-muted hover:text-ink">Open catalog →</a>} />
          {!templates?.length ? <div className="py-6 text-center text-sm text-ink-muted">No templates.</div> : (
            <Table columns={[{ key: "n", label: "Name" }, { key: "i", label: "Base image" }, { key: "t", label: "Type" }, { key: "a", label: "Access" }]}>
              {templates.map((t) => (
                <Row key={t.id}>
                  <Cell className="font-medium text-ink">{t.name}</Cell>
                  <Cell className="font-mono text-xs">{t.base_image}</Cell>
                  <Cell><Badge tone={t.built_in ? "neutral" : "blue"}>{t.built_in ? "Built-in" : "Custom"}</Badge></Cell>
                  <Cell className="text-xs text-ink-muted">{[t.default_expose_ssh && "SSH", t.default_expose_jupyter && "Jupyter"].filter(Boolean).join(" · ") || "Compute"}</Cell>
                </Row>
              ))}
            </Table>
          )}
        </Card>
      )}

      {tab === "enrollment" && (
        <div className="space-y-4">
          {token && (
            <Card className="border-emerald-200 p-5">
              <p className="mb-2 text-sm text-ink">New token issued. Copy it now — it won&apos;t be shown again.</p>
              <div className="mb-4 flex items-center gap-2 rounded-md border border-line bg-subtle px-3 py-2"><code className="flex-1 truncate font-mono text-sm text-emerald-400">{token}</code><CopyButton value={token} label="Copy" /></div>
              <p className="mb-1.5 text-xs text-ink-muted">Run this on the GPU server:</p>
              <pre className="overflow-x-auto rounded-md border border-line bg-neutral-950 px-3 py-2 font-mono text-xs text-neutral-200">{`gpu-agent \\
  --server=${serverUrl} \\
  --token=${token}`}</pre>
            </Card>
          )}
          <Card className="p-5">
            <h3 className="mb-4 flex items-center gap-2 text-sm font-medium text-ink"><Plus size={14} /> Generate token</h3>
            <div className="grid grid-cols-3 gap-3">
              <FormField label="Description"><Input value={tokenForm.description} onChange={(e) => setTokenForm({ ...tokenForm, description: e.target.value })} placeholder="rack-3 servers" /></FormField>
              <FormField label="Expires in (days)"><Input type="number" min={1} value={tokenForm.ttl_days} onChange={(e) => setTokenForm({ ...tokenForm, ttl_days: parseInt(e.target.value) || 365 })} /></FormField>
              <FormField label="Max uses"><Input type="number" min={1} value={tokenForm.max_uses} onChange={(e) => setTokenForm({ ...tokenForm, max_uses: parseInt(e.target.value) || 100 })} /></FormField>
            </div>
            <Button variant="primary" className="mt-4" onClick={handleIssueToken} disabled={issueToken.isPending}>{issueToken.isPending ? "Generating…" : "Generate token"}</Button>
          </Card>
          <Card className="overflow-hidden">
            <CardHeader title="Active tokens" />
            {!tokens?.length ? <div className="py-6 text-center text-sm text-ink-muted">No enrollment tokens yet.</div> : (
              <Table columns={[{ key: "d", label: "Description" }, { key: "s", label: "Status" }, { key: "u", label: "Uses", align: "right" }, { key: "e", label: "Expires" }, { key: "a", label: "" }]}>
                {tokens.map((t) => (
                  <Row key={t.id}>
                    <Cell className="text-ink">{t.description || <span className="text-ink-subtle">—</span>}</Cell>
                    <Cell><Badge tone={toneFor(TOKEN_TONE, t.status)} dot>{t.status}</Badge></Cell>
                    <Cell align="right" className="tnum">{t.uses} / {t.max_uses}</Cell>
                    <Cell className="text-xs">{new Date(t.expires_at).toLocaleDateString()}</Cell>
                    <Cell align="right">{t.status === "active" && <Button variant="danger" size="sm" onClick={() => revokeToken.mutate(t.id)} disabled={revokeToken.isPending}><Trash2 size={12} /> Revoke</Button>}</Cell>
                  </Row>
                ))}
              </Table>
            )}
          </Card>
        </div>
      )}
    </div>
  );
}

export default function SettingsPage() {
  return (
    <Suspense fallback={null}>
      <SettingsContent />
    </Suspense>
  );
}
