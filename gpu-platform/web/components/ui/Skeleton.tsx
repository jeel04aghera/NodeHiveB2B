import { cn } from "./cn";
import { Card } from "./Card";

/** Base shimmer block. */
export function Skeleton({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return <div style={style} className={cn("animate-pulse rounded bg-neutral-200/70", className)} />;
}

/** Row of metric cards matching the StatCard grid, to hold layout while loading. */
export function SkeletonStats({ count = 4 }: { count?: number }) {
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
      {Array.from({ length: count }).map((_, i) => (
        <div key={i} className="rounded-lg border border-line bg-surface px-5 py-4 shadow-card">
          <Skeleton className="h-3 w-24" />
          <Skeleton className="mt-3 h-7 w-16" />
          <Skeleton className="mt-2 h-3 w-20" />
        </div>
      ))}
    </div>
  );
}

/** Table skeleton inside a card — same chrome as a real table so nothing jumps. */
export function SkeletonTable({ rows = 6, cols = 5 }: { rows?: number; cols?: number }) {
  return (
    <Card className="overflow-hidden">
      <div className="flex gap-6 border-b border-line bg-subtle px-5 py-2.5">
        {Array.from({ length: cols }).map((_, i) => (
          <Skeleton key={i} className="h-3 flex-1" />
        ))}
      </div>
      <div className="divide-y divide-line">
        {Array.from({ length: rows }).map((_, r) => (
          <div key={r} className="flex items-center gap-6 px-5 py-3.5">
            {Array.from({ length: cols }).map((_, c) => (
              <Skeleton key={c} className={cn("h-4 flex-1", c === 0 && "max-w-[40%]")} />
            ))}
          </div>
        ))}
      </div>
    </Card>
  );
}

/** Chart placeholder with the same height as the real chart card body. */
export function SkeletonChart({ height = 220 }: { height?: number }) {
  const bars = [40, 65, 50, 80, 60, 72, 45, 68, 55, 78, 62, 70];
  return (
    <div className="flex items-end gap-2" style={{ height }}>
      {bars.map((h, i) => (
        <Skeleton key={i} className="flex-1 rounded-sm" style={{ height: `${h}%` }} />
      ))}
    </div>
  );
}

/** Generic card body skeleton (lines). */
export function SkeletonLines({ lines = 3 }: { lines?: number }) {
  return (
    <div className="space-y-2.5">
      {Array.from({ length: lines }).map((_, i) => (
        <Skeleton key={i} className={cn("h-4", i === lines - 1 ? "w-2/3" : "w-full")} />
      ))}
    </div>
  );
}
