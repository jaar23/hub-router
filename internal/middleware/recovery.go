package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/jaar23/hub-router/internal/model"
)

// Recovery returns a middleware that catches panics, logs them with a stack
// trace, and returns a 500 Internal Server Error to the client.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					logger.Error("panic recovered",
						"panic", v,
						"stack", string(debug.Stack()),
						"method", r.Method,
						"path", r.URL.Path,
					)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					resp := model.ErrorResponse{
						Code:    "INTERNAL_ERROR",
						Message: "an unexpected error occurred",
					}
					_ = json.NewEncoder(w).Encode(resp)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
