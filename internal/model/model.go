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
