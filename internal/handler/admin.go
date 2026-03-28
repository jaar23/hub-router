package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jaar23/hub-router/internal/middleware"
	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/internal/queue"
	"github.com/jaar23/hub-router/internal/store"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// AdminHandler handles health, metrics, and stats endpoints.
type AdminHandler struct {
	q         *queue.MemoryQueue
	s         *store.MemoryStore
	rl        *middleware.RateLimiter  // nil when rate limiting is disabled
	al        *middleware.AuthLockout
	startTime time.Time
}

// NewAdminHandler creates an admin handler.
func NewAdminHandler(
	q *queue.MemoryQueue,
	s *store.MemoryStore,
	rl *middleware.RateLimiter,
	al *middleware.AuthLockout,
) *AdminHandler {
	return &AdminHandler{q: q, s: s, rl: rl, al: al, startTime: time.Now()}
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

// HandleStats handles GET /debug/stats — returns a detailed snapshot for the TUI.
func (h *AdminHandler) HandleStats(w http.ResponseWriter, r *http.Request) {
	enqueued, dequeued, expired, dropped := h.q.Stats()

	resp := model.StatsResponse{
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
		Queue: model.QueueStats{
			Depth:         h.q.Len(),
			Capacity:      h.q.Cap(),
			EnqueuedTotal: enqueued,
			DequeuedTotal: dequeued,
			ExpiredTotal:  expired,
			DroppedTotal:  dropped,
		},
		Store: model.StoreStats{
			Results:       h.s.ResultCount(),
			ActiveWaiters: h.s.ActiveWaiters(),
		},
	}

	sec := model.SecurityStats{}
	if h.rl != nil {
		sec.RateLimitEnabled = true
		rlStats := h.rl.Stats()
		sec.TrackedIPs = rlStats.TrackedIPs
		sec.ThrottledIPs = rlStats.ThrottledIPs
	}
	if h.al != nil {
		alStats := h.al.Stats()
		sec.LockedIPs = alStats.LockedIPs
		sec.WatchedIPs = alStats.WatchedIPs
	}
	resp.Security = sec

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
