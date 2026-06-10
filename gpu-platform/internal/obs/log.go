// Package obs is the operations & observability toolkit (Phase 5): structured
// logging, error monitoring (Sentry), background-job health tracking, deployment
// health checks and Prometheus metrics. It is infrastructure-only — no domain logic.
package obs

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds the process logger. format: "json" (production default — one
// object per line for log drains) or "text" (dev). level: debug|info|warn|error.
// Error-level records are additionally forwarded to Sentry when it is initialized.
func NewLogger(format, level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if strings.ToLower(format) == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(&sentryForwardHandler{Handler: h})
}

// sentryForwardHandler mirrors Error-level log records to Sentry (no-op when Sentry
// is disabled), so every log.Error anywhere in the control plane becomes a tracked,
// alertable event without per-call-site instrumentation.
type sentryForwardHandler struct{ slog.Handler }

func (h *sentryForwardHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		captureLogRecord(r)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *sentryForwardHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &sentryForwardHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *sentryForwardHandler) WithGroup(name string) slog.Handler {
	return &sentryForwardHandler{Handler: h.Handler.WithGroup(name)}
}
