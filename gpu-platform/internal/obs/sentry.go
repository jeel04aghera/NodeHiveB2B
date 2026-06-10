package obs

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
)

var sentryEnabled bool

// InitSentry configures error monitoring. Disabled (returns false) when dsn is empty,
// so deployments without a Sentry project run exactly as before. Errors-only: no
// performance tracing, keeping the free tier and the overhead negligible.
func InitSentry(dsn, environment, release string) (bool, error) {
	if dsn == "" {
		return false, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:           dsn,
		Environment:   environment,
		Release:       release,
		SampleRate:    1.0, // error events only; no traces
		EnableTracing: false,
	})
	if err != nil {
		return false, fmt.Errorf("sentry init: %w", err)
	}
	sentryEnabled = true
	return true, nil
}

// FlushSentry drains buffered events on shutdown.
func FlushSentry() {
	if sentryEnabled {
		sentry.Flush(3 * time.Second)
	}
}

// CapturePanic reports a recovered panic with request context. Returns the event id
// ("" when Sentry is disabled).
func CapturePanic(recovered any, requestID, method, path string) string {
	if !sentryEnabled {
		return ""
	}
	var id *sentry.EventID
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("request_id", requestID)
		scope.SetContext("request", sentry.Context{"method": method, "path": path})
		id = sentry.CurrentHub().Recover(recovered)
	})
	if id == nil {
		return ""
	}
	return string(*id)
}

// captureLogRecord forwards an Error-level slog record to Sentry as a message event
// with the record's attributes as tags.
func captureLogRecord(r slog.Record) {
	if !sentryEnabled {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		attrs := sentry.Context{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		scope.SetContext("log_attrs", attrs)
		sentry.CaptureMessage(r.Message)
	})
}
