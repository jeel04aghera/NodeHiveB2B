import { cn } from "./cn";

/** Compact metric tile. Calm by default — color only via the optional `tone` dot. */
export function StatCard({
  label,
  value,
  hint,
  icon,
}: {
  label: React.ReactNode;
  value: React.ReactNode;
  hint?: React.ReactNode;
  icon?: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface px-5 py-4 shadow-card">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-ink-muted">{label}</span>
        {icon && <span className="text-ink-subtle">{icon}</span>}
      </div>
      <div className={cn("mt-2 text-2xl font-semibold text-ink tnum")}>{value}</div>
      {hint && <div className="mt-1 text-xs text-ink-subtle">{hint}</div>}
    </div>
  );
}
