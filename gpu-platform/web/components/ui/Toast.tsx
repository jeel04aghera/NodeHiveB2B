"use client";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Check, AlertCircle, Info, X } from "lucide-react";
import { cn } from "./cn";

type ToastKind = "success" | "error" | "info";
interface ToastItem { id: number; kind: ToastKind; message: string }

interface ToastCtx {
  show: (message: string, kind?: ToastKind) => void;
  success: (message: string) => void;
  error: (message: string) => void;
  info: (message: string) => void;
}

const Ctx = createContext<ToastCtx | null>(null);

const ICONS: Record<ToastKind, React.ReactNode> = {
  success: <Check size={15} className="text-emerald-400" />,
  error: <AlertCircle size={15} className="text-red-400" />,
  info: <Info size={15} className="text-blue-400" />,
};

function Toast({ item, onClose }: { item: ToastItem; onClose: () => void }) {
  useEffect(() => {
    const t = setTimeout(onClose, 3200);
    return () => clearTimeout(t);
  }, [onClose]);
  return (
    <div
      role="status"
      className={cn(
        "pointer-events-auto flex items-center gap-2.5 rounded-lg border border-line bg-surface px-3.5 py-2.5 shadow-pop",
        "animate-[nh-toast-in_180ms_ease-out]",
      )}
    >
      <span className="shrink-0">{ICONS[item.kind]}</span>
      <span className="text-sm text-ink">{item.message}</span>
      <button onClick={onClose} className="ml-1 shrink-0 rounded p-0.5 text-ink-subtle transition-colors hover:bg-subtle hover:text-ink">
        <X size={13} />
      </button>
    </div>
  );
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);

  const show = useCallback((message: string, kind: ToastKind = "success") => {
    setItems((prev) => [...prev, { id: Date.now() + Math.random(), kind, message }]);
  }, []);
  const remove = useCallback((id: number) => setItems((prev) => prev.filter((t) => t.id !== id)), []);

  const api: ToastCtx = {
    show,
    success: (m) => show(m, "success"),
    error: (m) => show(m, "error"),
    info: (m) => show(m, "info"),
  };

  return (
    <Ctx.Provider value={api}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2">
        {items.map((t) => (
          <Toast key={t.id} item={t} onClose={() => remove(t.id)} />
        ))}
      </div>
      <style>{`@keyframes nh-toast-in{from{opacity:0;transform:translateY(8px)}to{opacity:1;transform:translateY(0)}}`}</style>
    </Ctx.Provider>
  );
}

export function useToast(): ToastCtx {
  const c = useContext(Ctx);
  // No-op fallback so components never crash if rendered outside the provider.
  return c ?? { show: () => {}, success: () => {}, error: () => {}, info: () => {} };
}
