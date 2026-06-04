// Single source of truth for status → color. The whole app maps domain states
// (workload, GPU, node, token, role, audit action) onto five tones. Color is used
// ONLY to communicate status — never decoration.

export type Tone = "neutral" | "green" | "amber" | "red" | "blue";

/** Badge classes (filled pill) per tone — translucent tints on a dark surface. */
export const TONE_BADGE: Record<Tone, string> = {
  neutral: "bg-white/[0.06] text-ink-muted border-white/10",
  green: "bg-emerald-500/10 text-emerald-400 border-emerald-500/20",
  amber: "bg-amber-500/10 text-amber-400 border-amber-500/20",
  red: "bg-red-500/10 text-red-400 border-red-500/20",
  blue: "bg-blue-500/10 text-blue-400 border-blue-500/20",
};

/** Status dot fill per tone. */
export const TONE_DOT: Record<Tone, string> = {
  neutral: "bg-neutral-400",
  green: "bg-emerald-500",
  amber: "bg-amber-500",
  red: "bg-red-500",
  blue: "bg-blue-500",
};

/** Hex per tone for charts / inline SVG (no gradients). Tuned for dark surfaces. */
export const TONE_HEX: Record<Tone, string> = {
  neutral: "#A1A1AA",
  green: "#34d399",
  amber: "#fbbf24",
  red: "#f87171",
  blue: "#60a5fa",
};

// ── Domain → tone maps ───────────────────────────────────────────────────────

export const WORKLOAD_TONE: Record<string, Tone> = {
  queued: "blue",
  pending: "amber",
  running: "green",
  stopping: "neutral",
  stopped: "neutral",
  failed: "red",
};

export const GPU_TONE: Record<string, Tone> = {
  idle: "green",
  in_use: "blue",
  healthy: "neutral",
  unhealthy: "red",
};

export const NODE_TONE: Record<string, Tone> = {
  online: "green",
  degraded: "amber",
  offline: "neutral",
};

export const HEALTH_TONE: Record<string, Tone> = {
  healthy: "green",
  stale: "amber",
  offline: "neutral",
};

export const TOKEN_TONE: Record<string, Tone> = {
  active: "green",
  revoked: "red",
  expired: "neutral",
  exhausted: "neutral",
};

export const AUDIT_TONE: Record<string, Tone> = {
  "workload.launch": "green",
  "workload.stop": "amber",
  "user.create": "blue",
  "node.enroll": "blue",
};

export function toneFor(map: Record<string, Tone>, key: string): Tone {
  return map[key] ?? "neutral";
}
