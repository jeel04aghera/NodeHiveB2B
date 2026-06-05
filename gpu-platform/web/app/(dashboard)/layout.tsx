"use client";
import { Suspense, useEffect } from "react";
import { useRouter, usePathname, useSearchParams } from "next/navigation";
import Link from "next/link";
import {
  LayoutDashboard, Boxes, LayoutTemplate, Server, Cpu, Activity, Layers,
  Wallet, Receipt, Users, Building2, ScrollText, Settings, LogOut, FlaskConical, Zap,
  ListOrdered, CalendarClock, Target, BellRing,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import { useDeploymentConfig, useAlerts } from "@/lib/queries";
import { useOrgProfile } from "@/lib/org";
import { cn } from "@/components/ui";
export const dynamic = 'force-dynamic';

type NavItem = { href: string; label: string; icon: React.ElementType; match?: (path: string, tab: string | null) => boolean };
type NavGroup = { heading?: string; items: NavItem[] };

const NAV: NavGroup[] = [
  { items: [{ href: "/overview", label: "Overview", icon: LayoutDashboard }] },
  {
    heading: "Compute",
    items: [
      { href: "/workloads", label: "Workloads", icon: Boxes },
      { href: "/queue", label: "Queue", icon: ListOrdered },
      { href: "/projects", label: "Projects", icon: Layers },
      { href: "/templates", label: "Templates", icon: LayoutTemplate },
    ],
  },
  {
    heading: "Infrastructure",
    items: [
      { href: "/nodes", label: "Nodes", icon: Server },
      { href: "/gpus", label: "GPUs", icon: Cpu },
      { href: "/capacity", label: "Capacity", icon: Activity },
      { href: "/reservations", label: "Reservations", icon: CalendarClock },
    ],
  },
  {
    heading: "Finance",
    items: [
      { href: "/billing", label: "Billing", icon: Wallet },
      { href: "/chargeback", label: "Chargeback", icon: Receipt },
      { href: "/budgets", label: "Budgets", icon: Target },
      { href: "/alerts", label: "Cost Alerts", icon: BellRing },
    ],
  },
  {
    heading: "Administration",
    items: [
      { href: "/settings?tab=users", label: "Users", icon: Users, match: (_p, tab) => tab === "users" },
      { href: "/settings?tab=departments", label: "Departments", icon: Building2, match: (_p, tab) => tab === "departments" },
      { href: "/audit", label: "Audit Logs", icon: ScrollText },
      { href: "/settings", label: "Settings", icon: Settings, match: (p, tab) => p === "/settings" && !tab },
    ],
  },
];

function itemActive(item: NavItem, pathname: string, tab: string | null): boolean {
  if (item.match) return item.match(pathname, tab);
  const base = item.href.split("?")[0];
  // /settings (no tab) handled by its own match; other items match path prefix.
  if (base === "/settings") return false;
  return pathname === base || pathname.startsWith(base + "/");
}

function currentLabel(pathname: string, tab: string | null): string {
  for (const g of NAV) for (const it of g.items) if (itemActive(it, pathname, tab)) return it.label;
  if (pathname.startsWith("/settings")) return "Settings";
  return "NodeHive";
}

function DashboardLayoutInner({ children }: { children: React.ReactNode }) {
  const { user, ready, logout } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  const search = useSearchParams();
  const tab = search.get("tab");
  const { data: cfg } = useDeploymentConfig();
  const { data: activeAlerts } = useAlerts(false);
  const org = useOrgProfile();

  useEffect(() => {
    if (!ready) return;
    if (!user) router.replace("/login");
    else if (user.onboarded === false) router.replace("/onboarding"); // pre-onboarding (no org yet)
  }, [ready, user, router]);

  if (!ready) return <div className="min-h-screen bg-canvas" />;
  if (!user || user.onboarded === false) return null;

  return (
    <div className="flex min-h-screen bg-canvas text-ink">
      <aside className="fixed inset-y-0 left-0 z-30 flex w-[248px] flex-col border-r border-line bg-surface">
        <div className="flex h-[52px] items-center gap-2.5 border-b border-line px-5">
          <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-ink text-[13px] font-bold text-canvas">N</div>
          <div className="min-w-0 leading-tight">
            <div className="truncate text-[15px] font-semibold tracking-tight text-ink">NodeHive</div>
            {org.name && org.name !== "NodeHive" && <div className="truncate text-[11px] text-ink-subtle">{org.name}</div>}
          </div>
        </div>

        <nav className="flex-1 overflow-y-auto px-3 py-4">
          {NAV.map((group, gi) => (
            <div key={gi} className={cn(gi > 0 && "mt-5")}>
              {group.heading && (
                <div className="px-3 pb-1.5 text-[10px] font-semibold uppercase tracking-wider text-ink-subtle">{group.heading}</div>
              )}
              <div className="space-y-0.5">
                {group.items.map((item) => {
                  const active = itemActive(item, pathname, tab);
                  const Icon = item.icon;
                  return (
                    <Link
                      key={item.label}
                      href={item.href}
                      className={cn(
                        "flex items-center gap-2.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
                        active ? "bg-subtle text-ink" : "text-ink-muted hover:bg-subtle/60 hover:text-ink",
                      )}
                    >
                      <Icon size={16} />
                      {item.label}
                    </Link>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>

        <div className="border-t border-line p-3">
          <div className="flex items-center gap-2.5 rounded-md px-2 py-1.5">
            <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-subtle text-xs font-semibold text-ink-muted">
              {(user.name || user.email).slice(0, 1).toUpperCase()}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-xs font-medium text-ink">{user.name || user.email}</div>
              <div className="text-[11px] text-ink-subtle">{user.role === "admin" ? "Administrator" : "Member"}</div>
            </div>
            <button
              onClick={() => { logout(); router.replace("/login"); }}
              title="Sign out"
              className="rounded-md p-1.5 text-ink-subtle transition-colors hover:bg-subtle hover:text-ink"
            >
              <LogOut size={15} />
            </button>
          </div>
        </div>
      </aside>

      <div className="flex min-h-screen flex-1 flex-col pl-[248px]">
        <header className="sticky top-0 z-20 flex h-[52px] items-center justify-between border-b border-line bg-surface/85 px-8 backdrop-blur">
          <div className="text-sm font-medium text-ink">{currentLabel(pathname, tab)}</div>
          <div className="flex items-center gap-2.5">
            {cfg?.dev_mode && (
              <span
                title="Development environment — synthetic GPU inventory and generated metrics, not real hardware."
                className="inline-flex items-center gap-1.5 rounded-md border border-amber-500/20 bg-amber-500/10 px-2 py-1 text-[11px] font-medium text-amber-400"
              >
                <FlaskConical size={12} />
                <span className="font-semibold">DEV</span>
                <span className="text-amber-400/80">· Synthetic Fleet{cfg.synthetic_gpu_count ? ` (${cfg.synthetic_gpu_count})` : ""}</span>
              </span>
            )}
            <Link
              href="/alerts"
              title={activeAlerts?.length ? `${activeAlerts.length} active alert${activeAlerts.length > 1 ? "s" : ""}` : "Cost alerts"}
              className="relative inline-flex h-8 w-8 items-center justify-center rounded-md border border-line text-ink-muted transition-colors hover:bg-subtle hover:text-ink"
            >
              <BellRing size={15} />
              {activeAlerts && activeAlerts.length > 0 && (
                <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-semibold text-white tnum">
                  {activeAlerts.length}
                </span>
              )}
            </Link>
            <Link href="/workloads?launch=1">
              <span className="inline-flex h-8 items-center gap-1.5 rounded-md bg-ink px-3 text-xs font-medium text-canvas transition-colors hover:bg-white">
                <Zap size={13} /> Quick Launch
              </span>
            </Link>
          </div>
        </header>

        <main className="flex-1 px-8 py-7">
          <div className="mx-auto max-w-[1440px]">{children}</div>
        </main>
      </div>
    </div>
  );
}

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return (
    <Suspense fallback={<div className="min-h-screen bg-canvas" />}>
      <DashboardLayoutInner>{children}</DashboardLayoutInner>
    </Suspense>
  );
}
