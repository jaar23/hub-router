package queue

import (
	"context"
	"sync/atomic"

	"github.com/jaar23/hub-router/internal/model"
)

// MemoryQueue is a thread-safe, in-memory FIFO queue backed by a buffered channel.
type MemoryQueue struct {
	ch       chan *model.QueuedRequest
	enqueued atomic.Int64
	dequeued atomic.Int64
	expired  atomic.Int64
	dropped  atomic.Int64
}

// NewMemoryQueue creates a queue with the given capacity.
func NewMemoryQueue(maxSize int) *MemoryQueue {
	return &MemoryQueue{
		ch: make(chan *model.QueuedRequest, maxSize),
	}
}

// Enqueue adds a request to the queue. Returns ErrQueueFull if the channel is at capacity.
func (q *MemoryQueue) Enqueue(_ context.Context, req *model.QueuedRequest) error {
	select {
	case q.ch <- req:
		q.enqueued.Add(1)
		return nil
	default:
		q.dropped.Add(1)
		return ErrQueueFull
	}
}

// Dequeue removes and returns up to n requests. Expired requests are dropped silently.
// Never blocks — returns whatever is immediately available.
func (q *MemoryQueue) Dequeue(_ context.Context, n int) ([]*model.QueuedRequest, error) {
	out := make([]*model.QueuedRequest, 0, n)
	for len(out) < n {
		select {
		case req := <-q.ch:
			if req.IsExpired() {
				q.expired.Add(1)
				continue
			}
			out = append(out, req)
			q.dequeued.Add(1)
		default:
			// channel is empty
			return out, nil
		}
	}
	return out, nil
}

// Len returns the current number of items buffered in the channel (may include expired items).
func (q *MemoryQueue) Len() int {
	return len(q.ch)
}

// Stats returns queue counters for metrics/health endpoints.
func (q *MemoryQueue) Stats() (enqueued, dequeued, expired, dropped int64) {
	return q.enqueued.Load(), q.dequeued.Load(), q.expired.Load(), q.dropped.Load()
}

// Close is a no-op for the in-memory queue; the channel is GC'd when the queue is dropped.
func (q *MemoryQueue) Close() error {
	return nil
}
