import { forwardRef } from "react";
import { cn } from "./cn";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md" | "icon";

const VARIANTS: Record<Variant, string> = {
  // High-contrast primary: near-white on dark — the strong action on a dark canvas.
  primary: "bg-ink text-canvas hover:bg-white border border-ink",
  secondary:
    "bg-surface text-ink-muted border border-line hover:border-line-strong hover:bg-subtle hover:text-ink",
  ghost: "bg-transparent text-ink-muted hover:bg-subtle hover:text-ink border border-transparent",
  danger: "bg-transparent text-red-400 border border-red-500/30 hover:bg-red-500/10 hover:text-red-300",
};

const SIZES: Record<Size, string> = {
  sm: "h-8 px-3 text-xs gap-1.5",
  md: "h-9 px-4 text-sm gap-2",
  icon: "h-9 w-9 justify-center",
};

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", className, ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      className={cn(
        "inline-flex items-center rounded-md font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ink/20 disabled:opacity-50 disabled:pointer-events-none",
        VARIANTS[variant],
        SIZES[size],
        className,
      )}
      {...props}
    />
  );
});
