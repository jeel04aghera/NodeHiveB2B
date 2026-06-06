# Deploying the NodeHive control plane on Railway

The control plane listens on **two** ports:

| Port | Protocol | Purpose | Public exposure |
|------|----------|---------|-----------------|
| 8080 | HTTP/1.1 | REST API + `install.sh` + `/dist` agent binaries | Railway domain (HTTPS) |
| 9090 | gRPC (HTTP/2) | Agent gateway — enroll + heartbeat stream | Railway **TCP Proxy** |

## Why two public routes are required

Railway's HTTP edge proxy **downgrades HTTP/2 to HTTP/1.1**, which breaks gRPC, and it
will not route a second arbitrary TCP port. So the agent **cannot** reach gRPC through
the HTTPS domain. The agent dials `--server <host>:9090`; if 9090 isn't publicly routed
the enrollment fails with:

```
enroll: rpc error: code = Unavailable ... dial tcp <edge-ip>:9090: i/o timeout
```

A **TCP Proxy** forwards raw bytes, so gRPC/HTTP2 passes through untouched.

## One-time setup

1. **HTTP** — add a domain (Service → Settings → Networking → Public Networking →
   *Generate Domain*) with **target port `8080`**. This serves the dashboard, `install.sh`
   and `/dist/agent-*`.

2. **gRPC** — same service, add a **TCP Proxy** with target port **`9090`**. Railway
   assigns an endpoint like `shuttle.proxy.rlwy.net:15140`.

3. **Tell the installer about it** — set a service variable:

   ```
   AGENT_PUBLIC_GRPC_ADDR=shuttle.proxy.rlwy.net:15140
   ```

   `install.sh` bakes this into the agent's `--server`, so every enrolling node dials the
   TCP-proxy endpoint instead of the (unroutable) `<http-host>:9090`.

If `AGENT_PUBLIC_GRPC_ADDR` is **not** set on an HTTPS deployment, `install.sh` still
derives `<host>:9090` but prints a warning explaining this exact problem and the fix.

## Other required service variables

```
DATABASE_URL=...           # Railway Postgres plugin
JWT_SECRET=<32+ random chars>   # REQUIRED. Startup FAILS if missing/placeholder/<16 chars.
GRPC_INSECURE=true         # plaintext gRPC over the TCP proxy (see note)
# Do NOT set ENV=development in prod. ENV defaults to "production", which disables the
# DEV_* bootstrap admin/token entirely (they are ignored even if set) and enforces a
# strong JWT_SECRET. Create real orgs via POST /auth/register.

# --- Sessions (Phase 2) ---
ACCESS_TOKEN_TTL=15m       # short-lived Bearer access token. Unset => falls back to SESSION_TTL.
REFRESH_TOKEN_TTL=720h     # refresh-token session lifetime (720h = 30 days).
# Cross-origin cookie auth: the frontend and API are separate Railway origins, so the
# refresh cookie is cross-site. REQUIRED in production:
CORS_ALLOWED_ORIGINS=https://<web-host>   # explicit allow-list, NO wildcard; enables credentials.
APP_BASE_URL=https://<web-host>           # OAuth callback redirect + invitation accept links.
# In production (ENV unset/production) cookies are emitted Secure + SameSite=None, which the
# browser only accepts over HTTPS — Railway serves HTTPS, so this works out of the box.

# --- Invitation email (Phase 3.5) ---
RESEND_API_KEY=re_xxxxxxxx                 # Resend API key. Unset => emails logged to console.
INVITE_FROM_EMAIL=NodeHive <noreply@yourdomain.com>  # verified Resend sender; required to send.
# When both are set, invites are emailed (async) and the raw invite token is NO LONGER
# returned in the API response. When unset, invites still work — the URL is returned/logged.
```

> **Generate the secret:** `openssl rand -hex 32`. If `JWT_SECRET` is unset or a known
> placeholder (`change-me-in-production`, etc.), the control plane refuses to start —
> this is intentional (fail closed) so tokens can never be signed with a public secret.

> **Security note:** with `GRPC_INSECURE=true` the agent↔control-plane stream is plaintext
> across the public internet (the TCP proxy does not add TLS). For production, terminate
> TLS on the gRPC listener (`GRPC_CERT_FILE`/`GRPC_KEY_FILE`, agent `AGENT_CA_CERT`) — see
> T-026 (mTLS + credential rotation).
