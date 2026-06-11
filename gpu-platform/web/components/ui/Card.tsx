import { cn } from "./cn";

export function Card({
  className,
  children,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("rounded-lg border border-line bg-surface shadow-card", className)}
      {...props}
    >
      {children}
    </div>
  );
}

/** Card header row with a title, optional description line and right-aligned actions. */
export function CardHeader({
  title,
  description,
  meta,
  actions,
  className,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  meta?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center justify-between gap-3 border-b border-line px-5 py-3", className)}>
      <div className="min-w-0">
        <div className="flex items-center gap-2.5 min-w-0">
          <h2 className="text-sm font-semibold text-ink truncate">{title}</h2>
          {meta && <span className="text-xs text-ink-muted">{meta}</span>}
        </div>
        {description && <p className="mt-0.5 text-xs text-ink-muted">{description}</p>}
      </div>
      {actions}
    </div>
  );
}
