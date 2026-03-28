package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jaar23/hub-router/internal/config"
	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/internal/queue"
	"github.com/jaar23/hub-router/internal/store"
)

// OnlineHandler handles requests from the online (web app) server.
type OnlineHandler struct {
	q   *queue.KeyedQueue
	s   store.ResultStore
	cfg *config.Config
}

// NewOnlineHandler creates a handler wired to the given queue and store.
func NewOnlineHandler(q *queue.KeyedQueue, s store.ResultStore, cfg *config.Config) *OnlineHandler {
	return &OnlineHandler{q: q, s: s, cfg: cfg}
}

type submitRequest struct {
	Key     string            `json:"key"`
	Payload json.RawMessage   `json:"payload"`
	Headers map[string]string `json:"headers"`
}

// HandleSubmit handles POST /request.
// Enqueues the request and returns a correlation ID immediately (HTTP 202).
func (h *OnlineHandler) HandleSubmit(w http.ResponseWriter, r *http.Request) {
	var body submitRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "request body must be valid JSON")
		return
	}
	if len(body.Payload) == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_PAYLOAD", "payload is required")
		return
	}

	id, err := uuid.NewV7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate request ID")
		return
	}

	now := time.Now()
	req := &model.QueuedRequest{
		ID:         id.String(),
		Key:        body.Key,
		Payload:    body.Payload,
		Headers:    body.Headers,
		EnqueuedAt: now,
		ExpiresAt:  now.Add(h.cfg.Queue.RequestTTL),
	}

	if enqErr := h.q.Enqueue(r.Context(), body.Key, req); enqErr == queue.ErrQueueFull {
		writeError(w, http.StatusServiceUnavailable, "QUEUE_FULL", "queue is at capacity, try again later")
		return
	} else if enqErr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to enqueue request")
		return
	}

	// Register the ID as pending so Get can distinguish "not yet ready" from "unknown".
	h.s.RegisterPending(req.ID)

	depth := h.q.TotalLen()
	resp := model.SubmitResponse{
		ID:              req.ID,
		EstimatedWaitMs: int64(depth) * 100, // rough estimate: 100ms per queued item
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleSync handles POST /request/sync.
// It enqueues the request and immediately waits for the result within the same
// HTTP connection, behaving like a regular reverse proxy for fast local servers.
//
// Responses:
//   - 200 OK          – result arrived before timeout; body is the full Result JSON.
//   - 202 Accepted    – local server is slow / not running; body is SubmitResponse
//     with the correlation ID so the client can continue polling GET /result/{id}.
//   - 503 Service Unavailable – queue is full.
func (h *OnlineHandler) HandleSync(w http.ResponseWriter, r *http.Request) {
	var body submitRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "request body must be valid JSON")
		return
	}
	if len(body.Payload) == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_PAYLOAD", "payload is required")
		return
	}

	id, err := uuid.NewV7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate request ID")
		return
	}

	now := time.Now()
	req := &model.QueuedRequest{
		ID:         id.String(),
		Key:        body.Key,
		Payload:    body.Payload,
		Headers:    body.Headers,
		EnqueuedAt: now,
		ExpiresAt:  now.Add(h.cfg.Queue.RequestTTL),
	}

	// RegisterPending before Enqueue so the store never returns ErrNotFound
	// in the Get call below, even if the local server processes and pushes
	// the result before we reach the Get.
	h.s.RegisterPending(req.ID)

	if enqErr := h.q.Enqueue(r.Context(), body.Key, req); enqErr == queue.ErrQueueFull {
		// Clean up the pending registration since we won't be waiting.
		_ = h.s.Delete(r.Context(), req.ID)
		writeError(w, http.StatusServiceUnavailable, "QUEUE_FULL", "queue is at capacity, try again later")
		return
	} else if enqErr != nil {
		_ = h.s.Delete(r.Context(), req.ID)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to enqueue request")
		return
	}

	// Wait for the result using the same long-poll timeout as GET /result/{id}.
	timeout := h.cfg.Result.LongPollTimeout
	if ts := r.URL.Query().Get("timeout"); ts != "" {
		if d, parseErr := time.ParseDuration(ts); parseErr == nil && d > 0 && d < timeout {
			timeout = d
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	result, err := h.s.Get(ctx, req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "store error")
		return
	}

	if result != nil {
		// Fast path: result arrived within the timeout — return it directly.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
		return
	}

	// Slow path: timeout elapsed — fall back to async. Return 202 with the
	// correlation ID so the client can poll GET /result/{id}.
	depth := h.q.TotalLen()
	resp := model.SubmitResponse{
		ID:              req.ID,
		EstimatedWaitMs: int64(depth)*100 + timeout.Milliseconds(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleResult handles GET /result/{id}.
// Long-polls until the result is ready or the timeout elapses.
// Returns 204 on timeout so the client can retry with the same ID.
func (h *OnlineHandler) HandleResult(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	if requestID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "request ID is required")
		return
	}

	// Honour an optional ?timeout query param, capped at the server config max.
	timeout := h.cfg.Result.LongPollTimeout
	if ts := r.URL.Query().Get("timeout"); ts != "" {
		if d, parseErr := time.ParseDuration(ts); parseErr == nil && d > 0 && d < timeout {
			timeout = d
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	result, err := h.s.Get(ctx, requestID)
	if err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown request ID")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "store error")
		return
	}
	if result == nil {
		// Context timed out — tell client to retry.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}
