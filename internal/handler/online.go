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
	q   queue.Queue
	s   store.ResultStore
	cfg *config.Config
}

// NewOnlineHandler creates a handler wired to the given queue and store.
func NewOnlineHandler(q queue.Queue, s store.ResultStore, cfg *config.Config) *OnlineHandler {
	return &OnlineHandler{q: q, s: s, cfg: cfg}
}

type submitRequest struct {
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
		Payload:    body.Payload,
		Headers:    body.Headers,
		EnqueuedAt: now,
		ExpiresAt:  now.Add(h.cfg.Queue.RequestTTL),
	}

	if enqErr := h.q.Enqueue(r.Context(), req); enqErr == queue.ErrQueueFull {
		writeError(w, http.StatusServiceUnavailable, "QUEUE_FULL", "queue is at capacity, try again later")
		return
	} else if enqErr != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to enqueue request")
		return
	}

	// Register the ID as pending so Get can distinguish "not yet ready" from "unknown".
	h.s.RegisterPending(req.ID)

	depth := h.q.Len()
	resp := model.SubmitResponse{
		ID:              req.ID,
		EstimatedWaitMs: int64(depth) * 100, // rough estimate: 100ms per queued item
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
