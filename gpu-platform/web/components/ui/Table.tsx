import { cn } from "./cn";

/** Table wrapped in a bordered card with horizontal scroll for narrow viewports. */
export function Table({
  columns,
  className,
  children,
  stickyHeader = true,
}: {
  columns: { key: string; label: React.ReactNode; align?: "left" | "right" | "center"; className?: string }[];
  className?: string;
  children: React.ReactNode;
  stickyHeader?: boolean;
}) {
  return (
    <div className={cn("overflow-x-auto", className)}>
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-line bg-subtle">
            {columns.map((c) => (
              <th
                key={c.key}
                className={cn(
                  "px-5 py-2.5 text-xs font-medium text-ink-muted whitespace-nowrap",
                  c.align === "right" ? "text-right" : c.align === "center" ? "text-center" : "text-left",
                  stickyHeader && "sticky top-0 z-10 bg-subtle",
                  c.className,
                )}
              >
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-line">{children}</tbody>
      </table>
    </div>
  );
}

export function Row({
  className,
  ...props
}: React.HTMLAttributes<HTMLTableRowElement>) {
  return <tr className={cn("transition-colors hover:bg-subtle/70", className)} {...props} />;
}

export function Cell({
  align,
  className,
  children,
  ...props
}: React.TdHTMLAttributes<HTMLTableCellElement> & { align?: "left" | "right" | "center" }) {
  return (
    <td
      className={cn(
        "px-5 py-3 text-ink-muted align-middle",
        align === "right" ? "text-right" : align === "center" ? "text-center" : "text-left",
        className,
      )}
      {...props}
    >
      {children}
    </td>
  );
}
