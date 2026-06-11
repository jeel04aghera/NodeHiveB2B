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

// getToken exposes the current access token for transports that cannot use the
// api() wrapper (the SSE EventSource cannot set an Authorization header).
export function getToken(): string | null {
  return authToken;
}

// AuthProvider registers these so api-client can persist a refreshed token and signal
// when the session is irrecoverably lost (refresh failed) — keeps localStorage + React
// state in sync without a circular import.
let onTokenRefreshed: ((token: string) => void) | null = null;
let onSessionLost: (() => void) | null = null;
export function registerAuthHandlers(h: { onToken: (t: string) => void; onLost: () => void }) {
  onTokenRefreshed = h.onToken;
  onSessionLost = h.onLost;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

// tryRefresh rotates the refresh-token cookie into a fresh access token. Single-flight:
// concurrent 401s share one in-flight refresh so we don't stampede the endpoint.
let refreshInFlight: Promise<string | null> | null = null;
export function tryRefresh(): Promise<string | null> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    try {
      const res = await fetch(`${BASE}/auth/refresh`, {
        method: "POST",
        credentials: "include",
      });
      if (!res.ok) {
        onSessionLost?.();
        return null;
      }
      const body = (await res.json()) as { token: string };
      authToken = body.token;
      onTokenRefreshed?.(body.token);
      return body.token;
    } catch {
      // Network error — don't nuke the session, let the caller surface it.
      return null;
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

async function rawFetch(path: string, opts: RequestInit): Promise<Response> {
  return fetch(`${BASE}${path}`, {
    ...opts,
    // Only the cookie-backed /auth endpoints need credentials. Other calls authenticate
    // with the Bearer header, so they keep the simpler (and CORS-friendlier) default —
    // important so existing cross-origin requests are unaffected.
    credentials: path.startsWith("/auth/") ? "include" : "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
      ...(opts.headers ?? {}),
    },
  });
}

export async function api<T>(path: string, opts: RequestInit = {}): Promise<T> {
  let res = await rawFetch(path, opts);

  // Transparent access-token refresh: on a 401 for a normal API call, try to rotate the
  // refresh cookie once and replay the request. Auth endpoints are excluded so we never
  // recurse on the refresh/login flow itself.
  if (res.status === 401 && !path.startsWith("/auth/")) {
    const newToken = await tryRefresh();
    if (newToken) res = await rawFetch(path, opts);
  }

  if (!res.ok) throw new ApiError(res.status, await res.text());
  return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
}
