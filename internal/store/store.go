package store

import (
	"context"
	"errors"

	"github.com/jaar23/hub-router/internal/model"
)

// ErrNotFound is returned when a result ID is unknown.
var ErrNotFound = errors.New("result not found")

// ErrAlreadyFetched is returned when a result has already been consumed.
var ErrAlreadyFetched = errors.New("result already fetched")

// ResultStore stores results and supports long-poll blocking retrieval.
type ResultStore interface {
	// Put stores a result and signals any long-poll waiters.
	Put(ctx context.Context, result *model.Result) error

	// Get retrieves a result. If not yet available, blocks until:
	//   - the result arrives (returns result, nil)
	//   - ctx is cancelled/timed out (returns nil, nil — caller should return 204)
	// Returns ErrNotFound if the ID was never submitted or has expired.
	Get(ctx context.Context, requestID string) (*model.Result, error)

	// Delete removes a result (called after successful delivery).
	Delete(ctx context.Context, requestID string) error

	// RegisterPending marks a request ID as expected so Get can distinguish
	// "not yet arrived" from "never existed".
	RegisterPending(requestID string)

	// Close releases background goroutines.
	Close() error
}
