package middleware

import "net/http"

// SecureHeaders adds defensive HTTP response headers to every response.
// These headers protect against common web attacks even though hub-router
// is an API server (not a browser app).
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Prevent MIME-type sniffing.
		h.Set("X-Content-Type-Options", "nosniff")
		// Deny framing (clickjacking).
		h.Set("X-Frame-Options", "DENY")
		// Disable all browser features; this is an API, not a page.
		h.Set("Content-Security-Policy", "default-src 'none'")
		// Do not expose server version info.
		h.Set("Server", "hub-router")
		// Prevent caching of API responses (they contain auth-gated data).
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
