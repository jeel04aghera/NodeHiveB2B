"use client";
import { useSessions, useRevokeSession, useRevokeAllSessions, type Session } from "@/lib/queries";
import { Card, CardHeader, Badge, Button, EmptyState } from "@/components/ui";
import { Monitor, Smartphone, ShieldCheck, LogOut, RefreshCw } from "lucide-react";

function deviceIcon(os: string) {
  const l = os.toLowerCase();
  if (l.includes("ios") || l.includes("android")) return <Smartphone size={16} />;
  return <Monitor size={16} />;
}

function relativeTime(iso: string): string {
  const d = new Date(iso).getTime();
  const diff = Date.now() - d;
  const min = Math.floor(diff / 60000);
  if (min < 1) return "just now";
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day}d ago`;
  return new Date(iso).toLocaleDateString();
}

function SessionRow({ session, onRevoke, revoking }: { session: Session; onRevoke: () => void; revoking: boolean }) {
  return (
    <div className="flex items-center justify-between gap-4 px-5 py-4">
      <div className="flex items-start gap-3">
        <div className="mt-0.5 flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-subtle text-ink-subtle">
          {deviceIcon(session.os)}
        </div>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-medium text-ink">{session.device_name || "Unknown device"}</span>
            {session.current && <Badge tone="green" dot>This device</Badge>}
          </div>
          <p className="mt-0.5 text-xs text-ink-muted">
            {[session.browser, session.os].filter(Boolean).join(" · ") || "Unknown"}
            {session.ip_address ? ` · ${session.ip_address}` : ""}
          </p>
          <p className="mt-0.5 text-xs text-ink-subtle">
            Active {relativeTime(session.last_active_at)} · Signed in {new Date(session.created_at).toLocaleDateString()}
          </p>
        </div>
      </div>
      <Button variant={session.current ? "secondary" : "danger"} size="sm" onClick={onRevoke} disabled={revoking}>
        <LogOut size={12} /> {session.current ? "Sign out" : "Revoke"}
      </Button>
    </div>
  );
}

/** Active Sessions: the user's signed-in devices with per-session and bulk revocation. */
export function SecuritySessions() {
  const { data: sessions, isLoading, isError, error, refetch } = useSessions();
  const revoke = useRevokeSession();
  const revokeAll = useRevokeAllSessions();

  const current = sessions?.find((s) => s.current);
  const others = (sessions ?? []).filter((s) => !s.current);

  return (
    <div className="space-y-4">
      <Card className="overflow-hidden">
        <CardHeader
          title="Active Sessions"
          meta="Devices currently signed in to your account"
          actions={
            others.length > 0 ? (
              <Button
                variant="danger"
                size="sm"
                onClick={() => { if (confirm("Sign out of all other devices?")) revokeAll.mutate(); }}
                disabled={revokeAll.isPending}
              >
                <ShieldCheck size={12} /> {revokeAll.isPending ? "Revoking…" : "Revoke all others"}
              </Button>
            ) : undefined
          }
        />

        {isLoading ? (
          <div className="py-10 text-center text-sm text-ink-muted">Loading sessions…</div>
        ) : isError ? (
          <EmptyState
            title="Couldn’t load sessions"
            description={error instanceof Error ? error.message : "Something went wrong."}
            action={<Button variant="secondary" size="sm" onClick={() => refetch()}><RefreshCw size={12} /> Retry</Button>}
          />
        ) : !sessions?.length ? (
          <EmptyState
            icon={<Monitor size={18} />}
            title="No active sessions"
            description="When you sign in on a device it will appear here."
          />
        ) : (
          <div className="divide-y divide-line">
            {current && (
              <SessionRow
                session={current}
                revoking={revoke.isPending}
                onRevoke={() => { if (confirm("Sign out of this device? You’ll need to sign in again.")) revoke.mutate(current.id); }}
              />
            )}
            {others.length > 0 && (
              <>
                <div className="bg-subtle px-5 py-2 text-xs font-medium uppercase tracking-wide text-ink-subtle">
                  Other devices ({others.length})
                </div>
                {others.map((s) => (
                  <SessionRow key={s.id} session={s} revoking={revoke.isPending} onRevoke={() => revoke.mutate(s.id)} />
                ))}
              </>
            )}
          </div>
        )}
      </Card>

      <p className="px-1 text-xs text-ink-subtle">
        Revoking a session immediately ends its ability to stay signed in. Access tokens
        already issued expire within minutes; the revoked device is signed out on its next refresh.
      </p>
    </div>
  );
}
