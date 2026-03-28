package middleware_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jaar23/hub-router/internal/middleware"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestLockoutNotTriggeredBelowThreshold(t *testing.T) {
	al := middleware.NewAuthLockout(5, time.Minute, 5*time.Minute, testLogger())
	defer al.Close()

	for i := 0; i < 4; i++ {
		al.RecordFailure("3.3.3.3")
	}
	if al.IsLocked("3.3.3.3") {
		t.Fatal("should not be locked below threshold")
	}
}

func TestLockoutTriggeredAtThreshold(t *testing.T) {
	al := middleware.NewAuthLockout(3, time.Minute, 5*time.Minute, testLogger())
	defer al.Close()

	al.RecordFailure("4.4.4.4")
	al.RecordFailure("4.4.4.4")
	al.RecordFailure("4.4.4.4")

	if !al.IsLocked("4.4.4.4") {
		t.Fatal("IP should be locked after threshold failures")
	}
}

func TestLockoutExpiresAfterDuration(t *testing.T) {
	al := middleware.NewAuthLockout(1, 50*time.Millisecond, time.Minute, testLogger())
	defer al.Close()

	al.RecordFailure("5.5.5.5")
	if !al.IsLocked("5.5.5.5") {
		t.Fatal("should be locked immediately")
	}

	time.Sleep(100 * time.Millisecond)
	if al.IsLocked("5.5.5.5") {
		t.Fatal("lockout should have expired")
	}
}

func TestLockoutResetOnSuccess(t *testing.T) {
	al := middleware.NewAuthLockout(3, time.Minute, 5*time.Minute, testLogger())
	defer al.Close()

	al.RecordFailure("6.6.6.6")
	al.RecordFailure("6.6.6.6")
	al.RecordSuccess("6.6.6.6")
	al.RecordFailure("6.6.6.6")
	al.RecordFailure("6.6.6.6")

	// Only 2 failures since success — should not be locked.
	if al.IsLocked("6.6.6.6") {
		t.Fatal("IP should not be locked after success reset")
	}
}

func TestLockoutIsolatesIPs(t *testing.T) {
	al := middleware.NewAuthLockout(1, time.Minute, 5*time.Minute, testLogger())
	defer al.Close()

	al.RecordFailure("7.7.7.7")
	if al.IsLocked("8.8.8.8") {
		t.Fatal("different IP should not be locked")
	}
}

func TestLockoutWrapBlocks(t *testing.T) {
	al := middleware.NewAuthLockout(2, time.Minute, 5*time.Minute, testLogger())
	defer al.Close()

	authFn := func(key string) bool { return key == "good-key" }
	h := al.Wrap("X-Test-Key", authFn, http.HandlerFunc(okHandler))

	ip := "9.9.9.9:1234"

	// First failure.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = ip
	req1.Header.Set("X-Test-Key", "bad-key")
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr1.Code)
	}

	// Second failure — triggers lockout.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = ip
	req2.Header.Set("X-Test-Key", "bad-key")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr2.Code)
	}

	// Third request (even with good key) — locked out.
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = ip
	req3.Header.Set("X-Test-Key", "good-key")
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 (locked), got %d", rr3.Code)
	}
	if rr3.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on lockout response")
	}
}

func TestLockoutWrapAllowsGoodKey(t *testing.T) {
	al := middleware.NewAuthLockout(5, time.Minute, 5*time.Minute, testLogger())
	defer al.Close()

	authFn := func(key string) bool { return key == "valid" }
	h := al.Wrap("X-Test-Key", authFn, http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "11.11.11.11:80"
	req.Header.Set("X-Test-Key", "valid")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
