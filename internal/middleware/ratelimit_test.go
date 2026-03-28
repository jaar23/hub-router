package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jaar23/hub-router/internal/middleware"
)

func TestRateLimiterAllows(t *testing.T) {
	rl := middleware.NewRateLimiter(10, 5, time.Minute)
	defer rl.Close()

	// First 5 requests (burst) should pass.
	for i := 0; i < 5; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
}

func TestRateLimiterBlocks(t *testing.T) {
	rl := middleware.NewRateLimiter(1, 2, time.Minute)
	defer rl.Close()

	// Drain burst.
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")

	// Next request should be denied.
	if rl.Allow("10.0.0.1") {
		t.Fatal("expected request to be rate-limited")
	}
}

func TestRateLimiterRefills(t *testing.T) {
	rl := middleware.NewRateLimiter(100, 1, time.Minute) // 100 rps
	defer rl.Close()

	rl.Allow("2.2.2.2") // drain the single token
	if rl.Allow("2.2.2.2") {
		t.Fatal("should be blocked after draining")
	}

	time.Sleep(20 * time.Millisecond) // wait ~2 tokens worth at 100rps
	if !rl.Allow("2.2.2.2") {
		t.Fatal("should have refilled after sleep")
	}
}

func TestRateLimiterIsolatesIPs(t *testing.T) {
	rl := middleware.NewRateLimiter(1, 1, time.Minute)
	defer rl.Close()

	rl.Allow("1.1.1.1") // drain IP A
	// IP B should still have its own full bucket.
	if !rl.Allow("2.2.2.2") {
		t.Fatal("different IP should not be rate-limited")
	}
}

func TestRateLimiterMiddleware429(t *testing.T) {
	rl := middleware.NewRateLimiter(1, 1, time.Minute)
	defer rl.Close()

	h := rl.Middleware(http.HandlerFunc(okHandler))

	// First request — allowed.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "5.5.5.5:1234"
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr1.Code)
	}

	// Second request — should be rate-limited.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "5.5.5.5:1235"
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr2.Code)
	}
	if rr2.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}
