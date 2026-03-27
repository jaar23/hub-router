package queue

import (
	"context"
	"errors"

	"github.com/jaar23/hub-router/internal/model"
)

// ErrQueueFull is returned when Enqueue is called on a full queue.
var ErrQueueFull = errors.New("queue is full")

// Queue is the interface for the pending-request store.
type Queue interface {
	// Enqueue adds a request. Returns ErrQueueFull if at capacity.
	Enqueue(ctx context.Context, req *model.QueuedRequest) error

	// Dequeue removes and returns up to n pending requests (FIFO).
	// Expired requests are silently dropped.
	Dequeue(ctx context.Context, n int) ([]*model.QueuedRequest, error)

	// Len returns the current number of items in the queue (including possibly expired ones).
	Len() int

	// Close releases resources.
	Close() error
}
