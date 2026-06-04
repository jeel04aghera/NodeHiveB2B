import { useQueries } from "@tanstack/react-query";
import { api } from "./api-client";
import type { WorkloadDetail } from "./queries";

// The /workloads list response omits owner, cost and the department/template names
// (those live on /workloads/{id}). Rather than change the backend, we enrich the
// visible rows from the detail endpoint. React Query dedupes and caches these against
// the same key the detail page uses, so navigating in/out is free.
export interface Enriched {
  owner?: string;
  department?: string;
  template?: string;
  runtime_seconds?: number;
  runtime_cost?: number; // USD (display-converted to INR at render time)
}

export function useEnrichedWorkloads(ids: string[]): Record<string, Enriched> {
  const results = useQueries({
    queries: ids.map((id) => ({
      queryKey: ["workloads", id, "detail"],
      queryFn: () => api<WorkloadDetail>(`/workloads/${id}`),
      staleTime: 5_000,
      enabled: !!id,
    })),
  });

  const map: Record<string, Enriched> = {};
  results.forEach((r, i) => {
    const d = r.data;
    if (d) {
      map[ids[i]] = {
        owner: d.owner,
        department: d.department,
        template: d.template,
        runtime_seconds: d.runtime_seconds,
        runtime_cost: d.runtime_cost,
      };
    }
  });
  return map;
}
