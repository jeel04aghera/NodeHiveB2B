import { forwardRef } from "react";
import { cn } from "./cn";

const baseField =
  "w-full rounded-md border border-line bg-subtle px-3 py-2 text-sm text-ink placeholder:text-ink-subtle " +
  "focus:outline-none focus:border-line-strong focus:ring-2 focus:ring-ink/10 transition-colors";

export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  function Input({ className, ...props }, ref) {
    return <input ref={ref} className={cn(baseField, className)} {...props} />;
  },
);

export const Select = forwardRef<HTMLSelectElement, React.SelectHTMLAttributes<HTMLSelectElement>>(
  function Select({ className, children, ...props }, ref) {
    return (
      <select ref={ref} className={cn(baseField, "pr-8", className)} {...props}>
        {children}
      </select>
    );
  },
);

export function Label({ className, children, ...props }: React.LabelHTMLAttributes<HTMLLabelElement>) {
  return (
    <label className={cn("mb-1.5 block text-xs font-medium text-ink-muted", className)} {...props}>
      {children}
    </label>
  );
}

/** Label + control wrapper. */
export function FormField({
  label,
  hint,
  required,
  children,
}: {
  label: React.ReactNode;
  hint?: React.ReactNode;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div>
      <Label>
        {label}
        {required && <span className="ml-0.5 text-red-500">*</span>}
      </Label>
      {children}
      {hint && <p className="mt-1 text-xs text-ink-subtle">{hint}</p>}
    </div>
  );
}
