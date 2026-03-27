package middleware

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/jaar23/hub-router/internal/model"
)

// APIKeyMiddleware enforces API key authentication on HTTP handlers.
// Two separate key sets are maintained: one for the online server side,
// one for the local server side, enabling independent rotation.
type APIKeyMiddleware struct {
	onlineKeys map[string]struct{}
	localKeys  map[string]struct{}
	adminKey   string // optional; empty string disables admin auth
}

// NewAPIKeyMiddleware creates the middleware from key slices.
func NewAPIKeyMiddleware(onlineKeys, localKeys []string, adminKey string) *APIKeyMiddleware {
	m := &APIKeyMiddleware{
		onlineKeys: toSet(onlineKeys),
		localKeys:  toSet(localKeys),
		adminKey:   adminKey,
	}
	return m
}

// OnlineAuth wraps h, requiring a valid X-Online-API-Key header.
func (m *APIKeyMiddleware) OnlineAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Online-API-Key")
		if !m.validKey(key, m.onlineKeys) {
			writeAuthError(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LocalAuth wraps h, requiring a valid X-Local-API-Key header.
func (m *APIKeyMiddleware) LocalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Local-API-Key")
		if !m.validKey(key, m.localKeys) {
			writeAuthError(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminAuth wraps h, requiring a valid X-Admin-API-Key header.
// If no admin key is configured, the handler is passed through unauthenticated.
func (m *APIKeyMiddleware) AdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.adminKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-Admin-API-Key")
		if key == "" {
			writeAuthError(w)
			return
		}
		// Timing-safe comparison against the single admin key.
		if subtle.ConstantTimeCompare([]byte(key), []byte(m.adminKey)) != 1 {
			writeAuthError(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// validKey checks whether key exists in the allowed set using timing-safe comparisons.
func (m *APIKeyMiddleware) validKey(key string, allowed map[string]struct{}) bool {
	if key == "" {
		return false
	}
	keyBytes := []byte(key)
	for k := range allowed {
		if subtle.ConstantTimeCompare(keyBytes, []byte(k)) == 1 {
			return true
		}
	}
	return false
}

func writeAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	resp := model.ErrorResponse{Code: "UNAUTHORIZED", Message: "invalid or missing API key"}
	_ = json.NewEncoder(w).Encode(resp)
}

func toSet(keys []string) map[string]struct{} {
	s := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k != "" {
			s[k] = struct{}{}
		}
	}
	return s
}
