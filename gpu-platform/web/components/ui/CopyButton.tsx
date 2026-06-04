"use client";
import { useState } from "react";
import { Copy, Check } from "lucide-react";
import { cn } from "./cn";

export function CopyButton({
  value,
  label,
  className,
}: {
  value: string;
  label?: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1800);
  }
  return (
    <button
      type="button"
      onClick={copy}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-ink-muted transition-colors hover:bg-subtle hover:text-ink",
        className,
      )}
    >
      {copied ? <Check size={13} className="text-emerald-400" /> : <Copy size={13} />}
      {label && <span>{copied ? "Copied" : label}</span>}
    </button>
  );
}
