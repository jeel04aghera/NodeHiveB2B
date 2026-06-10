package httpapi

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/nodehive/gpu-platform/internal/audit"
	"github.com/nodehive/gpu-platform/internal/domain"
	"github.com/nodehive/gpu-platform/internal/obs"
)

// ── Request correlation IDs ───────────────────────────────────────────────────

type ctxRequestIDKey struct{}

// requestMeta is a mutable holder set early in the middleware chain so inner
// middleware (auth) can attribute the request to a user for the access log written
// by OUTER middleware — values added to a child context never propagate back up.
type requestMeta struct {
	userID string
	orgID  string
}

type ctxRequestMetaKey struct{}

const requestIDHeader = "X-Request-ID"

// requestIDMiddleware accepts a sane inbound X-Request-ID (so IDs correlate across
// an upstream proxy/frontend), otherwise generates one. The ID is echoed on the
// response, stored in the context, and stamped on access logs and audit events.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if rid == "" {
			rid = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, rid)
		ctx := context.WithValue(r.Context(), ctxRequestIDKey{}, rid)
		ctx = context.WithValue(ctx, ctxRequestMetaKey{}, &requestMeta{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFromCtx(ctx context.Context) string {
	rid, _ := ctx.Value(ctxRequestIDKey{}).(string)
	return rid
}

// sanitizeRequestID keeps client-supplied IDs log-safe: bounded length, printable
// token characters only. Anything else is discarded and regenerated.
func sanitizeRequestID(s string) string {
	if len(s) == 0 || len(s) > 64 {
		return ""
	}
	for _, c := range s {
		if !(c == '-' || c == '_' || c == '.' ||
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return ""
		}
	}
	return s
}

// noteUser records the authenticated principal for the access-log line.
func noteUser(ctx context.Context, u domain.User) {
	if m, ok := ctx.Value(ctxRequestMetaKey{}).(*requestMeta); ok {
		m.userID = u.ID.String()
		if u.Onboarded() {
			m.orgID = u.OrgID.String()
		}
	}
}

// ── Structured request logging ────────────────────────────────────────────────

// requestLogger writes one structured access-log line per request. Probe endpoints
// log at Debug so steady-state logs aren't dominated by health checks.
func (a *API) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)

		level := slog.LevelInfo
		switch {
		case ww.Status() >= 500:
			level = slog.LevelError
		case r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics":
			level = slog.LevelDebug
		}
		attrs := []any{
			"request_id", requestIDFromCtx(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r),
		}
		if m, ok := r.Context().Value(ctxRequestMetaKey{}).(*requestMeta); ok && m.userID != "" {
			attrs = append(attrs, "user_id", m.userID, "org_id", m.orgID)
		}
		a.logger().LogAttrs(r.Context(), level, "http_request", toSlogAttrs(attrs)...)

		if a.metrics != nil {
			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			a.metrics.ObserveHTTP(r.Method, route, ww.Status(), time.Since(start))
		}
	})
}

func toSlogAttrs(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, slog.Any(kv[i].(string), kv[i+1]))
	}
	return out
}

// recoverer converts panics into 500s, logs them with the request ID and forwards
// them to Sentry (no-op when Sentry is disabled). Replaces chi's Recoverer so panics
// reach error monitoring instead of just stderr.
func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison per net/http docs
					panic(rec)
				}
				rid := requestIDFromCtx(r.Context())
				eventID := obs.CapturePanic(rec, rid, r.Method, r.URL.Path)
				a.logger().Error("panic recovered",
					"request_id", rid, "method", r.Method, "path", r.URL.Path,
					"panic", rec, "sentry_event_id", eventID)
				writeErr(w, 500, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logger returns the configured logger (default for tests constructed without one).
func (a *API) logger() *slog.Logger {
	if a.log != nil {
		return a.log
	}
	return slog.Default()
}

// clientIP returns the request IP without the port (RealIP middleware has already
// normalized X-Forwarded-For into RemoteAddr).
func clientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
	return ip
}

// ── Audit helper ──────────────────────────────────────────────────────────────

// auditEvent records an audit entry for an HTTP-originated action, stamping the
// client IP and request correlation ID. Asynchronous and best-effort: the audit
// trail must never fail or slow the request it describes.
func (a *API) auditEvent(r *http.Request, e domain.AuditLog) {
	if e.Metadata == nil {
		e.Metadata = map[string]any{}
	}
	if rid := requestIDFromCtx(r.Context()); rid != "" {
		e.Metadata["request_id"] = rid
	}
	if e.IP == "" {
		e.IP = clientIP(r)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.audit.Record(ctx, e); err != nil {
			a.logger().Warn("audit record failed", "action", e.Action, "err", err)
		}
	}()
}

// userEvent is auditEvent pre-filled with the acting user.
func (a *API) userEvent(r *http.Request, u domain.User, action, targetType, targetID string, meta map[string]any) {
	a.auditEvent(r, domain.AuditLog{
		OrgID: u.OrgID, ActorType: "user", ActorID: u.ID.String(),
		Action: action, TargetType: targetType, TargetID: targetID, Metadata: meta,
	})
}

// ── Health endpoints ──────────────────────────────────────────────────────────

// healthz is the liveness probe: process is up and serving. No dependencies.
func (a *API) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// readyz is the readiness probe: 503 until the database is reachable, so the edge
// stops routing to an instance that lost its backing store.
func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	if a.health == nil {
		writeJSON(w, 200, map[string]string{"status": "ready"})
		return
	}
	if err := a.health.Ready(r.Context()); err != nil {
		writeJSON(w, 503, map[string]string{"status": "unavailable", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

// adminHealth is the deployment diagnostics document (admin-gated): database
// latency/pool, command-outbox depth, agent connectivity, background-job health and
// build/version info. Org admins see platform-level operational state but no other
// org's data.
func (a *API) adminHealth(w http.ResponseWriter, r *http.Request) {
	if a.health == nil {
		writeErr(w, 404, "not_found", "diagnostics not configured")
		return
	}
	rep := a.health.Check(r.Context())

	// Org-scoped agent view: the caller's own nodes, by status.
	u := userFromCtx(r)
	if views, err := a.nodes.List(r.Context(), u.OrgID); err == nil {
		online := 0
		for _, v := range views {
			if v.Status == "online" {
				online++
			}
		}
		if rep.Agents.Detail == nil {
			rep.Agents.Detail = map[string]any{}
		}
		rep.Agents.Detail["org_nodes_online"] = online
		rep.Agents.Detail["org_nodes_total"] = len(views)
	}
	writeJSON(w, 200, rep)
}

// metricsHandler gates the Prometheus endpoint. Production fails closed: without a
// configured METRICS_TOKEN the endpoint 404s; with one it requires Bearer auth.
// Dev (no token configured + insecure cookies) serves it openly for local scraping.
func (a *API) metricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.metrics == nil {
			writeErr(w, 404, "not_found", "metrics not configured")
			return
		}
		if a.metricsToken == "" {
			if a.secureCookies { // production posture
				writeErr(w, 404, "not_found", "metrics not configured")
				return
			}
		} else if bearerToken(r) != a.metricsToken {
			writeErr(w, 401, "unauthorized", "invalid metrics token")
			return
		}
		a.metrics.Handler().ServeHTTP(w, r)
	})
}

// ── Audit search endpoint (replaces the fixed 24h window) ─────────────────────

func (a *API) auditLogsSearch(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	f := auditFilterFromQuery(r.URL.Query())
	logs, total, err := a.audit.Query(r.Context(), u.OrgID, f)
	if err != nil {
		writeErr(w, 500, "internal", "could not query audit logs")
		return
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100 // mirror the service default so the paging echo is accurate
	}
	writeJSON(w, 200, map[string]any{
		"items":  logs,
		"total":  total,
		"limit":  f.Limit,
		"offset": f.Offset,
	})
}

func auditFilterFromQuery(q url.Values) audit.QueryFilter {
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	return audit.QueryFilter{
		From:       parseTime(q.Get("from"), time.Time{}),
		To:         parseTime(q.Get("to"), time.Time{}),
		Action:     q.Get("action"),
		ActorID:    q.Get("actor_id"),
		TargetType: q.Get("target_type"),
		TargetID:   q.Get("target_id"),
		Q:          q.Get("q"),
		Limit:      limit,
		Offset:     offset,
	}
}
