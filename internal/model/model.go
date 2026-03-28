package model

import (
	"encoding/json"
	"time"
)

// QueuedRequest is the unit stored in the pending queue.
// It is created by the online server and consumed by the local server.
type QueuedRequest struct {
	ID         string            `json:"id"`          // UUIDv7 — time-sortable, globally unique
	Payload    json.RawMessage   `json:"payload"`     // opaque to middleware
	Headers    map[string]string `json:"headers"`     // forwarded headers from original request
	EnqueuedAt time.Time         `json:"enqueued_at"`
	ExpiresAt  time.Time         `json:"expires_at"`
}

// IsExpired reports whether the request has passed its TTL.
func (r *QueuedRequest) IsExpired() bool {
	return time.Now().After(r.ExpiresAt)
}

// Result is what the local server pushes back after processing.
type Result struct {
	RequestID   string          `json:"request_id"`
	Payload     json.RawMessage `json:"payload"`
	StatusCode  int             `json:"status_code"` // semantic status from local server
	Error       string          `json:"error,omitempty"`
	CompletedAt time.Time       `json:"completed_at"`
}

// SubmitResponse is returned by POST /request.
type SubmitResponse struct {
	ID              string `json:"id"`
	EstimatedWaitMs int64  `json:"estimated_wait_ms"`
}

// PullResponse is the batch response to GET /queue/pull.
type PullResponse struct {
	Requests []*QueuedRequest `json:"requests"`
	Count    int              `json:"count"`
}

// ErrorResponse is the standard error envelope for all 4xx/5xx responses.
type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status        string `json:"status"`
	QueueDepth    int    `json:"queue_depth"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// StatsResponse is returned by GET /debug/stats — a detailed snapshot for the TUI.
type StatsResponse struct {
	UptimeSeconds int64         `json:"uptime_seconds"`
	Queue         QueueStats    `json:"queue"`
	Store         StoreStats    `json:"store"`
	Security      SecurityStats `json:"security"`
}

// QueueStats holds queue counters for the stats endpoint.
type QueueStats struct {
	Depth         int   `json:"depth"`
	Capacity      int   `json:"capacity"`
	EnqueuedTotal int64 `json:"enqueued_total"`
	DequeuedTotal int64 `json:"dequeued_total"`
	ExpiredTotal  int64 `json:"expired_total"`
	DroppedTotal  int64 `json:"dropped_total"`
}

// StoreStats holds result-store counters for the stats endpoint.
type StoreStats struct {
	Results       int `json:"results"`
	ActiveWaiters int `json:"active_waiters"`
}

// SecurityStats holds rate-limiter and lockout counters for the stats endpoint.
type SecurityStats struct {
	RateLimitEnabled bool `json:"rate_limit_enabled"`
	TrackedIPs       int  `json:"tracked_ips"`
	ThrottledIPs     int  `json:"throttled_ips"`
	LockedIPs        int  `json:"locked_ips"`
	WatchedIPs       int  `json:"watched_ips"`
}
