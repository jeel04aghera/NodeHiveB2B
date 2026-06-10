package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// H3 regression: the fixed-window limiter must admit exactly `limit` hits per key per
// window, isolate keys, and recover after the window elapses.
func TestRateLimiterAllow(t *testing.T) {
	rl := newRateLimiter(3, 50*time.Millisecond)

	for i := 1; i <= 3; i++ {
		if ok, _ := rl.allow("1.2.3.4"); !ok {
			t.Fatalf("hit %d should be allowed", i)
		}
	}
	ok, retry := rl.allow("1.2.3.4")
	if ok {
		t.Fatal("hit 4 should be blocked")
	}
	if retry < 1 {
		t.Errorf("blocked hit should report a positive Retry-After, got %d", retry)
	}

	// A different key is unaffected by the first key's exhaustion.
	if ok, _ := rl.allow("5.6.7.8"); !ok {
		t.Fatal("separate key should be allowed")
	}

	// After the window passes the original key recovers.
	time.Sleep(60 * time.Millisecond)
	if ok, _ := rl.allow("1.2.3.4"); !ok {
		t.Fatal("key should recover after the window resets")
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	h := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	do := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		req.RemoteAddr = ip + ":51234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do("9.9.9.9"); rec.Code != 200 {
		t.Fatalf("first request: want 200, got %d", rec.Code)
	}
	if rec := do("9.9.9.9"); rec.Code != 200 {
		t.Fatalf("second request: want 200, got %d", rec.Code)
	}
	rec := do("9.9.9.9")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: want 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response must carry a Retry-After header")
	}
	// Other clients are unaffected.
	if rec := do("8.8.8.8"); rec.Code != 200 {
		t.Fatalf("other IP: want 200, got %d", rec.Code)
	}
}
