package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jaar23/hub-router/internal/config"
	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/internal/queue"
	"github.com/jaar23/hub-router/internal/store"
)

// LocalHandler handles requests from the local processing server.
type LocalHandler struct {
	q   queue.Queue
	s   store.ResultStore
	cfg *config.Config
}

// NewLocalHandler creates a handler wired to the given queue and store.
func NewLocalHandler(q queue.Queue, s store.ResultStore, cfg *config.Config) *LocalHandler {
	return &LocalHandler{q: q, s: s, cfg: cfg}
}

// HandlePull handles GET /queue/pull?batch=N.
// Returns up to N pending requests immediately (never blocks).
// Returns an empty array when the queue is empty.
func (h *LocalHandler) HandlePull(w http.ResponseWriter, r *http.Request) {
	batchSize := h.cfg.Queue.MaxBatchSize
	if bs := r.URL.Query().Get("batch"); bs != "" {
		if n, err := strconv.Atoi(bs); err == nil && n > 0 {
			if n < batchSize {
				batchSize = n
			}
		}
	}

	requests, err := h.q.Dequeue(r.Context(), batchSize)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to dequeue requests")
		return
	}

	resp := model.PullResponse{
		Requests: requests,
		Count:    len(requests),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleResult handles POST /queue/result.
// Stores the result and wakes any long-poll waiter for that request ID.
func (h *LocalHandler) HandleResult(w http.ResponseWriter, r *http.Request) {
	var result model.Result
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "request body must be valid JSON")
		return
	}
	if result.RequestID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_REQUEST_ID", "request_id is required")
		return
	}

	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now()
	}

	if err := h.s.Put(r.Context(), &result); err == store.ErrNotFound {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown request ID")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to store result")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
