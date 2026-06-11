"use client";
// Phase 6 — realtime updates over SSE.
//
// The server pushes cache-invalidation HINTS ({topic}), not data: on each event we
// invalidate the TanStack Query keys that topic feeds and let the existing fetchers
// refetch. The 5s polling intervals remain as the fallback, so a dropped SSE
// connection degrades gracefully instead of freezing the UI.
import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { API_BASE_URL, getToken } from "./api-client";
import { useAuth } from "./auth";

// Topic → query-key prefixes to invalidate (must match keys in lib/queries.ts).
const TOPIC_KEYS: Record<string, string[][]> = {
  nodes: [["nodes"], ["gpus"], ["metrics"], ["config"]],
  workloads: [["workloads"], ["metrics"]],
  queue: [["queue"], ["workloads"]],
  budgets: [["budgets"], ["billing"]],
  members: [["org", "members"], ["org", "invitations"], ["org", "join-codes"], ["users"]],
};

export function RealtimeUpdates() {
  const qc = useQueryClient();
  const { user } = useAuth();
  const authed = !!user && !!user.org_id;

  useEffect(() => {
    if (!authed) return;
    const token = getToken();
    if (!token) return;

    // EventSource cannot send headers; the server accepts ?token= for this stream.
    const es = new EventSource(
      `${API_BASE_URL}/events/stream?token=${encodeURIComponent(token)}`,
    );
    const onChange = (e: MessageEvent) => {
      try {
        const { topic } = JSON.parse(e.data) as { topic: string };
        for (const key of TOPIC_KEYS[topic] ?? []) {
          qc.invalidateQueries({ queryKey: key });
        }
      } catch {
        // Malformed event — ignore; polling remains the safety net.
      }
    };
    es.addEventListener("change", onChange);
    // Access tokens are short-lived: when the stream errors (e.g. expired token on
    // an auto-reconnect), close it; the next effect run (user/token change) or page
    // navigation re-establishes it, and polling covers the gap.
    let failures = 0;
    es.onerror = () => {
      failures += 1;
      if (failures >= 5) es.close();
    };
    es.onopen = () => {
      failures = 0;
    };
    return () => {
      es.removeEventListener("change", onChange);
      es.close();
    };
    // Re-subscribe when the signed-in user changes (token rotation is handled by
    // the error-close + remount on navigation; polling bridges any gap).
  }, [authed, user?.id, qc]);

  return null;
}
