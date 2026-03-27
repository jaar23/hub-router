package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaar23/hub-router/internal/middleware"
)

func okHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func TestOnlineAuthValidKey(t *testing.T) {
	m := middleware.NewAPIKeyMiddleware([]string{"online-key-1"}, []string{"local-key-1"}, "")
	h := m.OnlineAuth(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Online-API-Key", "online-key-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestOnlineAuthInvalidKey(t *testing.T) {
	m := middleware.NewAPIKeyMiddleware([]string{"online-key-1"}, []string{"local-key-1"}, "")
	h := m.OnlineAuth(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Online-API-Key", "wrong-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestOnlineAuthMissingKey(t *testing.T) {
	m := middleware.NewAPIKeyMiddleware([]string{"online-key-1"}, []string{"local-key-1"}, "")
	h := m.OnlineAuth(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestLocalAuthValidKey(t *testing.T) {
	m := middleware.NewAPIKeyMiddleware([]string{"online-key-1"}, []string{"local-key-1"}, "")
	h := m.LocalAuth(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Local-API-Key", "local-key-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestLocalAuthWrongSide(t *testing.T) {
	// Online key should not work for local routes.
	m := middleware.NewAPIKeyMiddleware([]string{"online-key-1"}, []string{"local-key-1"}, "")
	h := m.LocalAuth(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Local-API-Key", "online-key-1") // wrong key
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAdminAuthDisabled(t *testing.T) {
	// No admin key configured → pass through.
	m := middleware.NewAPIKeyMiddleware([]string{"ok"}, []string{"ok"}, "")
	h := m.AdminAuth(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAdminAuthEnabled(t *testing.T) {
	m := middleware.NewAPIKeyMiddleware([]string{"ok"}, []string{"ok"}, "admin-secret")
	h := m.AdminAuth(http.HandlerFunc(okHandler))

	// Valid.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Admin-API-Key", "admin-secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	// Invalid.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Admin-API-Key", "wrong")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr2.Code)
	}
}

func TestMultipleKeys(t *testing.T) {
	// Both keys in the set should be accepted.
	m := middleware.NewAPIKeyMiddleware(
		[]string{"key-a", "key-b"},
		[]string{"local-key"},
		"",
	)
	h := m.OnlineAuth(http.HandlerFunc(okHandler))

	for _, key := range []string{"key-a", "key-b"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Online-API-Key", key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("key %q: expected 200, got %d", key, rr.Code)
		}
	}
}
