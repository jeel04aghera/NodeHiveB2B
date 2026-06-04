// Single typed fetch wrapper for the control-plane REST API. When the backend is
// unreachable it surfaces a clear error — no silent fallback, no mock data.

// The one place the backend URL is defined. Override per-environment with
// NEXT_PUBLIC_API_URL (origin only) or NEXT_PUBLIC_API_BASE_URL (full base incl.
// /api/v1). Defaults to the Railway deployment so production builds work without
// extra config; set NEXT_PUBLIC_API_BASE_URL to http://localhost:8080/api/v1 for
// local development.
const API_ORIGIN = (
  process.env.NEXT_PUBLIC_API_URL ?? "https://nodehiveb2b-production.up.railway.app"
).replace(/\/+$/, "");

export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? `${API_ORIGIN}/api/v1`;

// Backend origin with the /api/v1 suffix stripped — for non-REST endpoints such
// as the agent install script served by the control plane.
export const API_BASE_ORIGIN = API_BASE_URL.replace(/\/api\/v1\/?$/, "");

const BASE = API_BASE_URL;

let authToken: string | null = null;

export function setToken(t: string | null) {
  authToken = t;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
      ...(opts.headers ?? {}),
    },
  });
  if (!res.ok) throw new ApiError(res.status, await res.text());
  return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
}
