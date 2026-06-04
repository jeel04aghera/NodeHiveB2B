import { cn } from "./cn";

/** Actionable empty state: subtle icon, a clear title, guidance, and optional steps/actions. */
export function EmptyState({
  icon,
  title,
  description,
  steps,
  action,
  className,
}: {
  icon?: React.ReactNode;
  title: string;
  description?: React.ReactNode;
  steps?: React.ReactNode[];
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center px-6 py-14 text-center", className)}>
      {icon && (
        <div className="mb-4 flex h-11 w-11 items-center justify-center rounded-lg border border-line bg-subtle text-ink-subtle">
          {icon}
        </div>
      )}
      <p className="text-sm font-medium text-ink">{title}</p>
      {description && <p className="mt-1 max-w-sm text-sm text-ink-muted">{description}</p>}
      {steps && steps.length > 0 && (
        <ol className="mt-4 space-y-1.5 text-left text-sm text-ink-muted">
          {steps.map((s, i) => (
            <li key={i} className="flex items-start gap-2.5">
              <span className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-line bg-surface text-[10px] font-medium text-ink-muted">
                {i + 1}
              </span>
              <span>{s}</span>
            </li>
          ))}
        </ol>
      )}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}
