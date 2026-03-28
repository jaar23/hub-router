package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/jaar23/hub-router/internal/model"
)

// BodyLimit returns a middleware that rejects requests whose body exceeds
// maxBytes. This prevents memory exhaustion from abnormally large payloads.
// Uses http.MaxBytesReader so the limit is enforced during body reading,
// not by pre-reading the entire body.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_ = json.NewEncoder(w).Encode(model.ErrorResponse{
					Code:    "PAYLOAD_TOO_LARGE",
					Message: "request body exceeds maximum allowed size",
				})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
