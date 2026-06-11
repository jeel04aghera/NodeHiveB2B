"use client";
// Phase 6 — programmatic access: personal API keys (any member) and
// service accounts + their keys (admins).
import { useState } from "react";
import {
  useAPIKeys, useCreateAPIKey, useRevokeAPIKey,
  useServiceAccounts, useCreateServiceAccount, useUpdateServiceAccount,
  type APIKey, type ServiceAccount,
} from "@/lib/queries";
import { useAuth, isAdminRole } from "@/lib/auth";
import {
  Card, CardHeader, Table, Row, Cell, Badge, Button, EmptyState,
  FormField, Input, Select, Modal, CopyButton,
} from "@/components/ui";
import { KeyRound, Bot } from "lucide-react";

function keyTone(status: APIKey["status"]) {
  return status === "active" ? ("green" as const) : status === "expired" ? ("amber" as const) : ("red" as const);
}

function fmt(ts?: string | null) {
  return ts ? new Date(ts).toLocaleString() : "—";
}

export function APIAccess() {
  const { user } = useAuth();
  const isAdmin = isAdminRole(user?.role);
  const { data: keys, isLoading } = useAPIKeys();
  const { data: serviceAccounts } = useServiceAccounts();
  const createKey = useCreateAPIKey();
  const revokeKey = useRevokeAPIKey();
  const createSA = useCreateServiceAccount();
  const updateSA = useUpdateServiceAccount();

  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState("");
  const [ttl, setTtl] = useState("90");
  const [forSA, setForSA] = useState("");
  const [mintedKey, setMintedKey] = useState<string | null>(null);

  const [showSA, setShowSA] = useState(false);
  const [saName, setSaName] = useState("");
  const [saRole, setSaRole] = useState("member");
  const [saDesc, setSaDesc] = useState("");

  const saName2 = (id?: string) => serviceAccounts?.find((s) => s.id === id)?.name;

  const submitKey = async () => {
    const res = await createKey.mutateAsync({
      name,
      ttl_days: Number(ttl) || 0,
      ...(forSA ? { service_account_id: forSA } : {}),
    });
    setMintedKey(res.key);
    setName("");
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader
          title="API keys"
          description="Programmatic access to the NodeHive API. Keys are shown once and stored hashed."
          actions={<Button size="sm" onClick={() => { setMintedKey(null); setShowCreate(true); }}>Create key</Button>}
        />
        {isLoading ? (
          <div className="px-5 py-8 text-center text-sm text-ink-muted">Loading…</div>
        ) : !keys?.length ? (
          <EmptyState
            icon={<KeyRound size={20} />}
            title="No API keys yet"
            description="Create a key to call the API from scripts, CI or SDKs. Use it as a Bearer token."
          />
        ) : (
          <Table columns={[
            { key: "name", label: "Name" },
            { key: "key", label: "Key" },
            { key: "owner", label: "Owner" },
            { key: "status", label: "Status" },
            { key: "used", label: "Last used" },
            { key: "expires", label: "Expires" },
            { key: "actions", label: "" },
          ]}>
            {keys.map((k) => (
              <Row key={k.id}>
                <Cell className="font-medium text-ink">{k.name}</Cell>
                <Cell className="font-mono text-xs text-ink-subtle">{k.prefix}…</Cell>
                <Cell className="text-xs">
                  {k.service_account_id
                    ? <span className="inline-flex items-center gap-1"><Bot size={12} /> {saName2(k.service_account_id) ?? "service account"}</span>
                    : k.user_id === user?.id ? "You" : "Member"}
                </Cell>
                <Cell><Badge tone={keyTone(k.status)}>{k.status}</Badge></Cell>
                <Cell className="text-xs text-ink-subtle">{fmt(k.last_used_at)}</Cell>
                <Cell className="text-xs text-ink-subtle">{k.expires_at ? fmt(k.expires_at) : "Never"}</Cell>
                <Cell>
                  {k.status === "active" && (
                    <Button size="sm" variant="danger" onClick={() => revokeKey.mutate(k.id)}>Revoke</Button>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      {isAdmin && (
        <Card>
          <CardHeader
            title="Service accounts"
            description="API-only identities for automation. Capped at admin role; disabling kills all of their keys instantly."
            actions={<Button size="sm" variant="secondary" onClick={() => setShowSA(true)}>New service account</Button>}
          />
          {!serviceAccounts?.length ? (
            <EmptyState icon={<Bot size={20} />} title="No service accounts"
              description="Create one for CI pipelines or schedulers, then mint it an API key above." />
          ) : (
            <Table columns={[
              { key: "name", label: "Name" },
              { key: "role", label: "Role" },
              { key: "desc", label: "Description" },
              { key: "status", label: "Status" },
              { key: "actions", label: "" },
            ]}>
              {serviceAccounts.map((sa: ServiceAccount) => (
                <Row key={sa.id}>
                  <Cell className="font-medium text-ink">{sa.name}</Cell>
                  <Cell><Badge tone="neutral">{sa.role}</Badge></Cell>
                  <Cell className="max-w-[18rem] truncate text-xs text-ink-subtle">{sa.description || "—"}</Cell>
                  <Cell><Badge tone={sa.disabled_at ? "red" : "green"}>{sa.disabled_at ? "disabled" : "active"}</Badge></Cell>
                  <Cell>
                    <Button size="sm" variant={sa.disabled_at ? "secondary" : "danger"}
                      onClick={() => updateSA.mutate({ id: sa.id, disabled: !sa.disabled_at })}>
                      {sa.disabled_at ? "Enable" : "Disable"}
                    </Button>
                  </Cell>
                </Row>
              ))}
            </Table>
          )}
        </Card>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title={mintedKey ? "API key created" : "Create API key"}>
        {mintedKey ? (
          <div className="space-y-3">
            <p className="text-sm text-ink-muted">
              Copy this key now — it will <span className="font-medium text-ink">not be shown again</span>.
            </p>
            <div className="flex items-center gap-2 rounded-md border border-line bg-subtle px-3 py-2 font-mono text-xs">
              <span className="truncate">{mintedKey}</span>
              <CopyButton value={mintedKey} />
            </div>
            <p className="text-xs text-ink-subtle">Use it as a Bearer token: <code>Authorization: Bearer {mintedKey.slice(0, 12)}…</code></p>
            <div className="flex justify-end">
              <Button size="sm" onClick={() => setShowCreate(false)}>Done</Button>
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <FormField label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="ci-deploy" />
            </FormField>
            <FormField label="Expires in">
              <Select value={ttl} onChange={(e) => setTtl(e.target.value)}>
                <option value="30">30 days</option>
                <option value="90">90 days</option>
                <option value="365">1 year</option>
                <option value="0">Never</option>
              </Select>
            </FormField>
            {isAdmin && (
              <FormField label="Owner">
                <Select value={forSA} onChange={(e) => setForSA(e.target.value)}>
                  <option value="">Personal key (acts as you)</option>
                  {serviceAccounts?.filter((s) => !s.disabled_at).map((s) => (
                    <option key={s.id} value={s.id}>Service account · {s.name}</option>
                  ))}
                </Select>
              </FormField>
            )}
            {createKey.isError && <p className="text-xs text-red-400">{(createKey.error as Error).message}</p>}
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
              <Button size="sm" disabled={!name.trim() || createKey.isPending} onClick={submitKey}>Create</Button>
            </div>
          </div>
        )}
      </Modal>

      <Modal open={showSA} onClose={() => setShowSA(false)} title="New service account">
        <div className="space-y-4">
          <FormField label="Name">
            <Input value={saName} onChange={(e) => setSaName(e.target.value)} placeholder="ci-runner" />
          </FormField>
          <FormField label="Role">
            <Select value={saRole} onChange={(e) => setSaRole(e.target.value)}>
              <option value="member">Member — launch/stop its own workloads</option>
              <option value="admin">Admin — manage fleet, budgets, users</option>
            </Select>
          </FormField>
          <FormField label="Description">
            <Input value={saDesc} onChange={(e) => setSaDesc(e.target.value)} placeholder="What this account automates" />
          </FormField>
          {createSA.isError && <p className="text-xs text-red-400">{(createSA.error as Error).message}</p>}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="secondary" onClick={() => setShowSA(false)}>Cancel</Button>
            <Button size="sm" disabled={!saName.trim() || createSA.isPending}
              onClick={async () => {
                await createSA.mutateAsync({ name: saName, description: saDesc, role: saRole });
                setSaName(""); setSaDesc(""); setShowSA(false);
              }}>
              Create
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
