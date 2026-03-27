package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/internal/queue"
	"github.com/jaar23/hub-router/internal/store"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// AdminHandler handles health and metrics endpoints.
type AdminHandler struct {
	q         *queue.MemoryQueue
	s         *store.MemoryStore
	startTime time.Time
}

// NewAdminHandler creates an admin handler.
func NewAdminHandler(q *queue.MemoryQueue, s *store.MemoryStore) *AdminHandler {
	return &AdminHandler{q: q, s: s, startTime: time.Now()}
}

// HandleHealth handles GET /health.
func (h *AdminHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	resp := model.HealthResponse{
		Status:        "ok",
		QueueDepth:    h.q.Len(),
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleMetrics returns Prometheus-format metrics.
func (h *AdminHandler) HandleMetrics() http.Handler {
	return promhttp.Handler()
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(model.ErrorResponse{Code: code, Message: message})
}
