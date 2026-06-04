import { Search } from "lucide-react";
import { cn } from "./cn";

/** Segmented control (single source for the group-by / filter pills). */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  className,
}: {
  options: { value: T; label: React.ReactNode }[];
  value: T;
  onChange: (v: T) => void;
  className?: string;
}) {
  return (
    <div className={cn("inline-flex rounded-md border border-line bg-surface p-0.5", className)}>
      {options.map((o) => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded px-3 py-1.5 text-sm font-medium transition-colors",
            value === o.value ? "bg-subtle text-ink shadow-sm" : "text-ink-muted hover:text-ink",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function SearchInput({
  value,
  onChange,
  placeholder = "Search…",
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  className?: string;
}) {
  return (
    <div className={cn("relative", className)}>
      <Search size={15} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-ink-subtle" />
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="h-9 w-full rounded-md border border-line bg-subtle pl-8 pr-3 text-sm text-ink placeholder:text-ink-subtle focus:border-line-strong focus:outline-none focus:ring-2 focus:ring-ink/10"
      />
    </div>
  );
}

/** Thin allocation/utilization meter with a tone. */
export function Meter({
  value,
  tone = "neutral",
  className,
}: {
  value: number; // 0..100
  tone?: "neutral" | "green" | "amber" | "red" | "blue";
  className?: string;
}) {
  const fill = {
    neutral: "bg-ink-subtle",
    green: "bg-emerald-500",
    amber: "bg-amber-500",
    red: "bg-red-500",
    blue: "bg-blue-500",
  }[tone];
  return (
    <div className={cn("h-1.5 overflow-hidden rounded-full bg-subtle", className)}>
      <div className={cn("h-full rounded-full", fill)} style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
    </div>
  );
}
