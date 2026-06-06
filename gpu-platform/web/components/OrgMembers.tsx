"use client";
import { useState } from "react";
import { useAuth, roleLabel } from "@/lib/auth";
import {
  useMembers, useChangeMemberRole, useRemoveMember,
  useInvitations, useCreateInvitation, useResendInvitation, useRevokeInvitation,
  useJoinCodes, useCreateJoinCode, useRevokeJoinCode,
  type Member,
} from "@/lib/queries";
import {
  Card, CardHeader, Table, Row, Cell, Badge, Button, Input, Select, FormField, CopyButton,
} from "@/components/ui";
import { Plus, Trash2, Send, UserMinus } from "lucide-react";

function roleTone(role: string) {
  return role === "owner" ? "blue" : role === "admin" ? "amber" : "neutral";
}
function inviteTone(status: string) {
  return status === "pending" ? "amber" : status === "accepted" ? "green" : "neutral";
}

/** Organization administration: members & roles, pending invitations, and join codes. */
export function OrgMembers() {
  const { user } = useAuth();
  const isOwner = user?.role === "owner";

  const { data: members, isLoading: membersLoading } = useMembers();
  const changeRole = useChangeMemberRole();
  const removeMember = useRemoveMember();

  const { data: invites } = useInvitations();
  const createInvite = useCreateInvitation();
  const resendInvite = useResendInvitation();
  const revokeInvite = useRevokeInvitation();

  const { data: codes } = useJoinCodes();
  const createCode = useCreateJoinCode();
  const revokeCode = useRevokeJoinCode();

  const [inviteForm, setInviteForm] = useState({ email: "", role: "member" });
  const [inviteError, setInviteError] = useState("");
  const [lastInviteLink, setLastInviteLink] = useState<string | null>(null);

  const [codeForm, setCodeForm] = useState({ description: "", ttl_days: 7, max_uses: 0 });
  const [lastCode, setLastCode] = useState<string | null>(null);

  // What roles can the current actor assign? Owners can assign any; admins can't touch owner.
  const assignableRoles = isOwner ? ["owner", "admin", "member"] : ["admin", "member"];

  async function handleInvite(e: React.FormEvent) {
    e.preventDefault();
    setInviteError(""); setLastInviteLink(null);
    try {
      const r = await createInvite.mutateAsync(inviteForm);
      setLastInviteLink(r.accept_url || r.token);
      setInviteForm({ email: "", role: "member" });
    } catch (err) {
      setInviteError(err instanceof Error ? err.message : "Failed to send invitation.");
    }
  }
  async function handleCreateCode(e: React.FormEvent) {
    e.preventDefault();
    setLastCode(null);
    const r = await createCode.mutateAsync(codeForm);
    setLastCode(r.code);
    setCodeForm({ description: "", ttl_days: 7, max_uses: 0 });
  }

  function canManage(m: Member): boolean {
    if (m.user_id === user?.id) return false;       // not yourself
    if (m.role === "owner" && !isOwner) return false; // only owners manage owners
    return true;
  }

  return (
    <div className="space-y-4">
      {/* Members */}
      <Card className="overflow-hidden">
        <CardHeader title="Members" meta={`${members?.length ?? 0} in this organization`} />
        {membersLoading ? (
          <div className="py-6 text-center text-sm text-ink-muted">Loading members…</div>
        ) : (
          <Table columns={[{ key: "n", label: "Name" }, { key: "e", label: "Email" }, { key: "r", label: "Role" }, { key: "a", label: "", align: "right" }]}>
            {(members ?? []).map((m) => (
              <Row key={m.id}>
                <Cell className="font-medium text-ink">
                  {m.name || "—"} {m.user_id === user?.id && <span className="text-ink-subtle">(you)</span>}
                </Cell>
                <Cell>{m.email}</Cell>
                <Cell>
                  {canManage(m) ? (
                    <Select
                      value={m.role}
                      onChange={(e) => changeRole.mutate({ userId: m.user_id, role: e.target.value })}
                      className="w-auto py-1 text-xs"
                      disabled={changeRole.isPending}
                    >
                      {assignableRoles.map((r) => <option key={r} value={r}>{roleLabel(r)}</option>)}
                      {/* keep the current role visible even if not normally assignable */}
                      {!assignableRoles.includes(m.role) && <option value={m.role}>{roleLabel(m.role)}</option>}
                    </Select>
                  ) : (
                    <Badge tone={roleTone(m.role)}>{roleLabel(m.role)}</Badge>
                  )}
                </Cell>
                <Cell align="right">
                  {canManage(m) && (
                    <Button variant="danger" size="sm" disabled={removeMember.isPending}
                      onClick={() => { if (confirm(`Remove ${m.email} from the organization?`)) removeMember.mutate(m.user_id); }}>
                      <UserMinus size={12} /> Remove
                    </Button>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      {/* Invitations */}
      <Card className="p-5">
        <h3 className="mb-4 flex items-center gap-2 text-sm font-medium text-ink"><Send size={14} /> Invite by email</h3>
        <form onSubmit={handleInvite}>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-3">
            <FormField label="Email"><Input type="email" value={inviteForm.email} required
              onChange={(e) => setInviteForm({ ...inviteForm, email: e.target.value })} placeholder="teammate@company.com" /></FormField>
            <FormField label="Role"><Select value={inviteForm.role} onChange={(e) => setInviteForm({ ...inviteForm, role: e.target.value })}>
              <option value="member">Member</option><option value="admin">Admin</option>
            </Select></FormField>
          </div>
          {inviteError && <p className="mt-3 text-sm text-red-400">{inviteError}</p>}
          <Button type="submit" variant="primary" className="mt-4" disabled={createInvite.isPending}>
            {createInvite.isPending ? "Sending…" : "Send invitation"}
          </Button>
        </form>
        {lastInviteLink && (
          <div className="mt-4 rounded-md border border-line bg-subtle px-3 py-2">
            <p className="mb-1.5 text-xs text-ink-muted">Share this invite link with the invitee (shown once):</p>
            <div className="flex items-center gap-2">
              <code className="flex-1 truncate font-mono text-xs text-emerald-400">{lastInviteLink}</code>
              <CopyButton value={lastInviteLink} label="Copy" />
            </div>
          </div>
        )}
      </Card>

      <Card className="overflow-hidden">
        <CardHeader title="Pending invitations" />
        {!invites?.length ? <div className="py-6 text-center text-sm text-ink-muted">No invitations.</div> : (
          <Table columns={[{ key: "e", label: "Email" }, { key: "r", label: "Role" }, { key: "s", label: "Status" }, { key: "x", label: "Expires" }, { key: "a", label: "", align: "right" }]}>
            {invites.map((inv) => (
              <Row key={inv.id}>
                <Cell className="text-ink">{inv.email}</Cell>
                <Cell><Badge tone={roleTone(inv.role)}>{roleLabel(inv.role)}</Badge></Cell>
                <Cell><Badge tone={inviteTone(inv.status)} dot>{inv.status}</Badge></Cell>
                <Cell className="text-xs">{new Date(inv.expires_at).toLocaleDateString()}</Cell>
                <Cell align="right">
                  {inv.status === "pending" && (
                    <div className="flex justify-end gap-2">
                      <Button variant="secondary" size="sm" disabled={resendInvite.isPending}
                        onClick={async () => { const r = await resendInvite.mutateAsync(inv.id); setLastInviteLink(r.accept_url || r.token); }}>Resend</Button>
                      <Button variant="danger" size="sm" disabled={revokeInvite.isPending} onClick={() => revokeInvite.mutate(inv.id)}>
                        <Trash2 size={12} /> Revoke
                      </Button>
                    </div>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>

      {/* Join codes */}
      <Card className="p-5">
        <h3 className="mb-4 flex items-center gap-2 text-sm font-medium text-ink"><Plus size={14} /> Create join code</h3>
        <form onSubmit={handleCreateCode}>
          <div className="grid grid-cols-3 gap-3">
            <FormField label="Description"><Input value={codeForm.description}
              onChange={(e) => setCodeForm({ ...codeForm, description: e.target.value })} placeholder="Eng team" /></FormField>
            <FormField label="Expires in (days)"><Input type="number" min={1} value={codeForm.ttl_days}
              onChange={(e) => setCodeForm({ ...codeForm, ttl_days: parseInt(e.target.value) || 7 })} /></FormField>
            <FormField label="Max uses (0 = ∞)"><Input type="number" min={0} value={codeForm.max_uses}
              onChange={(e) => setCodeForm({ ...codeForm, max_uses: parseInt(e.target.value) || 0 })} /></FormField>
          </div>
          <Button type="submit" variant="primary" className="mt-4" disabled={createCode.isPending}>
            {createCode.isPending ? "Creating…" : "Create code"}
          </Button>
        </form>
        {lastCode && (
          <div className="mt-4 rounded-md border border-line bg-subtle px-3 py-2">
            <p className="mb-1.5 text-xs text-ink-muted">Share this join code (shown once):</p>
            <div className="flex items-center gap-2">
              <code className="flex-1 truncate font-mono text-sm text-emerald-400">{lastCode}</code>
              <CopyButton value={lastCode} label="Copy" />
            </div>
          </div>
        )}
      </Card>

      <Card className="overflow-hidden">
        <CardHeader title="Active join codes" />
        {!codes?.length ? <div className="py-6 text-center text-sm text-ink-muted">No join codes.</div> : (
          <Table columns={[{ key: "d", label: "Description" }, { key: "s", label: "Status" }, { key: "u", label: "Uses", align: "right" }, { key: "x", label: "Expires" }, { key: "a", label: "", align: "right" }]}>
            {codes.map((c) => (
              <Row key={c.id}>
                <Cell className="text-ink">{c.description || <span className="text-ink-subtle">—</span>}</Cell>
                <Cell><Badge tone={inviteTone(c.status === "active" ? "pending" : c.status)} dot>{c.status}</Badge></Cell>
                <Cell align="right" className="tnum">{c.uses}{c.max_uses > 0 ? ` / ${c.max_uses}` : ""}</Cell>
                <Cell className="text-xs">{new Date(c.expires_at).toLocaleDateString()}</Cell>
                <Cell align="right">
                  {c.status === "active" && (
                    <Button variant="danger" size="sm" disabled={revokeCode.isPending} onClick={() => revokeCode.mutate(c.id)}>
                      <Trash2 size={12} /> Revoke
                    </Button>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Card>
    </div>
  );
}
