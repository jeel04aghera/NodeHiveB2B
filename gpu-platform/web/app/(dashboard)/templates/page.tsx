"use client";
import { useMemo, useState } from "react";
import Link from "next/link";
import { useTemplates, type Template } from "@/lib/queries";
import { LayoutTemplate, Play, Terminal, BookOpen, Star } from "lucide-react";
import { PageHeader, Card, Badge, Button, EmptyState, SearchInput, cn } from "@/components/ui";

type Category = "All" | "Development" | "Training" | "Inference" | "Data Science" | "Custom";
const CATEGORIES: Category[] = ["All", "Development", "Training", "Inference", "Data Science", "Custom"];

function categorize(t: Template): Exclude<Category, "All"> {
  const hay = `${t.name} ${t.tags?.join(" ")} ${t.base_image}`.toLowerCase();
  if (!t.built_in || hay.includes("vllm") || hay.includes("custom")) {
    if (hay.includes("vllm") || hay.includes("inference") || hay.includes("serving")) return "Inference";
    if (!t.built_in) return "Custom";
  }
  if (hay.includes("datascience") || hay.includes("jupyter") || hay.includes("pandas")) return "Data Science";
  if (hay.includes("pytorch") || hay.includes("tensorflow") || hay.includes("cuda") || hay.includes("train")) return "Training";
  if (hay.includes("inference") || hay.includes("vllm") || hay.includes("serving")) return "Inference";
  return "Development";
}

// Templates teams reach for most — pinned to the front of the catalog.
const POPULAR = ["PyTorch", "Python", "JupyterLab"];
function isPopular(t: Template) { return POPULAR.some((p) => t.name.includes(p)); }

export default function TemplatesPage() {
  const { data: templates, isLoading, error } = useTemplates();
  const [query, setQuery] = useState("");
  const [cat, setCat] = useState<Category>("All");

  const counts = useMemo(() => {
    const c: Record<string, number> = { All: templates?.length ?? 0 };
    for (const t of templates ?? []) { const k = categorize(t); c[k] = (c[k] ?? 0) + 1; }
    return c;
  }, [templates]);

  const filtered = useMemo(() => {
    let rows = templates ?? [];
    if (cat !== "All") rows = rows.filter((t) => categorize(t) === cat);
    if (query.trim()) {
      const q = query.toLowerCase();
      rows = rows.filter((t) => `${t.name} ${t.description} ${t.base_image} ${t.tags?.join(" ")}`.toLowerCase().includes(q));
    }
    return [...rows].sort((a, b) => Number(isPopular(b)) - Number(isPopular(a)) || a.name.localeCompare(b.name));
  }, [templates, cat, query]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Templates"
        description="Pre-built environments your team can launch in seconds."
        actions={<Link href="/workloads?launch=1"><Button variant="primary" size="md"><Play size={14} /> Launch Workload</Button></Link>}
      />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-1.5">
          {CATEGORIES.map((c) => (
            <button
              key={c}
              onClick={() => setCat(c)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-sm font-medium transition-colors",
                cat === c ? "border-ink bg-ink text-canvas" : "border-line bg-surface text-ink-muted hover:text-ink",
              )}
            >
              {c}
              {typeof counts[c] === "number" && counts[c] > 0 && (
                <span className={cn("text-xs", cat === c ? "text-neutral-300" : "text-ink-subtle")}>{counts[c]}</span>
              )}
            </button>
          ))}
        </div>
        <SearchInput value={query} onChange={setQuery} placeholder="Search templates…" className="w-64" />
      </div>

      {error ? (
        <Card><EmptyState icon={<LayoutTemplate size={20} />} title="Failed to load templates" /></Card>
      ) : isLoading ? (
        <Card className="p-10 text-center text-sm text-ink-muted">Loading templates…</Card>
      ) : !filtered.length ? (
        <Card><EmptyState icon={<LayoutTemplate size={20} />} title="No templates match" description="Try a different category or search term." /></Card>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {filtered.map((t) => (
            <Card key={t.id} className="flex flex-col p-5">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <h3 className="truncate text-sm font-semibold text-ink">{t.name}</h3>
                    {isPopular(t) && <Star size={13} className="shrink-0 fill-amber-400 text-amber-400" />}
                  </div>
                  <p className="mt-0.5 text-xs text-ink-subtle">{categorize(t)}{t.version ? ` · v${t.version}` : ""}</p>
                </div>
                <Badge tone={t.built_in ? "neutral" : "blue"}>{t.built_in ? "Built-in" : "Custom"}</Badge>
              </div>

              <p className="mt-2 line-clamp-2 text-sm text-ink-muted">{t.description}</p>

              <div className="mt-3 flex items-center gap-1.5 text-xs">
                <span className="text-ink-subtle">Image</span>
                <code className="truncate font-mono text-ink">{t.base_image}</code>
              </div>

              {t.software?.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-1">
                  {t.software.slice(0, 5).map((s) => (
                    <span key={s.name} className="rounded bg-subtle px-1.5 py-0.5 text-[11px] text-ink-muted">{s.name}{s.version ? ` ${s.version}` : ""}</span>
                  ))}
                </div>
              )}

              <div className="mt-4 flex items-center gap-3 border-t border-line pt-3 text-xs text-ink-muted">
                {t.default_expose_ssh && <span className="inline-flex items-center gap-1"><Terminal size={12} /> SSH</span>}
                {t.default_expose_jupyter && <span className="inline-flex items-center gap-1"><BookOpen size={12} /> Jupyter</span>}
                {!t.default_expose_ssh && !t.default_expose_jupyter && <span className="text-ink-subtle">Compute only</span>}
                <Link href="/workloads?launch=1" className="ml-auto font-medium text-ink hover:underline">Launch →</Link>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
