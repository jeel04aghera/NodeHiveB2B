package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimiter is a fixed-window per-key request limiter. In-memory and per-process —
// matching the single-replica control plane (the same trade-off the agent Dispatcher
// makes). Replace with a Redis-backed limiter when the control plane scales out.
//
// Fixed windows admit at most 2× the limit across a window boundary; with brute-force
// style limits (tens per minute against bcrypt) that's an acceptable bound.
type rateLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string]*rlBucket
	lastGC  time.Time
}

type rlBucket struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*rlBucket),
		lastGC:  time.Now(),
	}
}

// allow records a hit for key and reports whether it is within the limit. When the
// limit is exceeded it also returns the seconds until the window resets (Retry-After).
func (rl *rateLimiter) allow(key string) (ok bool, retryAfterSec int) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Amortized cleanup: every window, drop expired buckets so the map can't grow
	// unboundedly under key churn (e.g. spoofed XFF spread across many IPs).
	if now.Sub(rl.lastGC) > rl.window {
		for k, b := range rl.buckets {
			if now.After(b.resetAt) {
				delete(rl.buckets, k)
			}
		}
		rl.lastGC = now
	}

	b := rl.buckets[key]
	if b == nil || now.After(b.resetAt) {
		rl.buckets[key] = &rlBucket{count: 1, resetAt: now.Add(rl.window)}
		return true, 0
	}
	b.count++
	if b.count > rl.limit {
		return false, int(time.Until(b.resetAt).Seconds()) + 1
	}
	return true, 0
}

// middleware enforces the limit per client IP. It relies on chi's middleware.RealIP
// (applied at the top of the router) having already normalized X-Forwarded-For into
// RemoteAddr — the same source the session device log uses.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		if ok, retry := rl.allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeErr(w, http.StatusTooManyRequests, "rate_limited",
				"too many requests, slow down and retry shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}
