package queue

import (
	"context"
	"sort"
	"sync"

	"github.com/jaar23/hub-router/internal/model"
)

// DefaultKey is the queue key used when no key is specified.
const DefaultKey = "default"

// KeyedQueue holds a separate MemoryQueue per key, auto-created on first use.
// All per-key queues share the same maxSize.
type KeyedQueue struct {
	mu      sync.RWMutex
	queues  map[string]*MemoryQueue
	maxSize int
}

// NewKeyedQueue creates a KeyedQueue with a pre-initialised "default" queue.
func NewKeyedQueue(maxSize int) *KeyedQueue {
	kq := &KeyedQueue{
		queues:  make(map[string]*MemoryQueue),
		maxSize: maxSize,
	}
	kq.queues[DefaultKey] = NewMemoryQueue(maxSize)
	return kq
}

// normalizeKey maps an empty string to DefaultKey.
func normalizeKey(key string) string {
	if key == "" {
		return DefaultKey
	}
	return key
}

// getOrCreate returns the queue for a key, creating it if it doesn't exist.
// Uses double-checked locking to avoid contention on the common (read) path.
func (kq *KeyedQueue) getOrCreate(key string) *MemoryQueue {
	kq.mu.RLock()
	q, ok := kq.queues[key]
	kq.mu.RUnlock()
	if ok {
		return q
	}

	kq.mu.Lock()
	defer kq.mu.Unlock()
	// Re-check under write lock.
	if q, ok = kq.queues[key]; ok {
		return q
	}
	q = NewMemoryQueue(kq.maxSize)
	kq.queues[key] = q
	return q
}

// Enqueue adds a request to the queue identified by key (empty → "default").
func (kq *KeyedQueue) Enqueue(ctx context.Context, key string, req *model.QueuedRequest) error {
	return kq.getOrCreate(normalizeKey(key)).Enqueue(ctx, req)
}

// Dequeue removes and returns up to n requests from the queue identified by key.
func (kq *KeyedQueue) Dequeue(ctx context.Context, key string, n int) ([]*model.QueuedRequest, error) {
	return kq.getOrCreate(normalizeKey(key)).Dequeue(ctx, n)
}

// Len returns the depth of the queue for a single key (0 if key doesn't exist yet).
func (kq *KeyedQueue) Len(key string) int {
	kq.mu.RLock()
	q, ok := kq.queues[normalizeKey(key)]
	kq.mu.RUnlock()
	if !ok {
		return 0
	}
	return q.Len()
}

// TotalLen returns the sum of depths across all keys.
func (kq *KeyedQueue) TotalLen() int {
	kq.mu.RLock()
	defer kq.mu.RUnlock()
	total := 0
	for _, q := range kq.queues {
		total += q.Len()
	}
	return total
}

// Cap returns the maximum capacity shared by all per-key queues.
func (kq *KeyedQueue) Cap() int {
	return kq.maxSize
}

// Keys returns a sorted snapshot of all active key names.
// "default" is always first when present.
func (kq *KeyedQueue) Keys() []string {
	kq.mu.RLock()
	defer kq.mu.RUnlock()
	keys := make([]string, 0, len(kq.queues))
	for k := range kq.queues {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == DefaultKey {
			return true
		}
		if keys[j] == DefaultKey {
			return false
		}
		return keys[i] < keys[j]
	})
	return keys
}

// StatsAll returns a per-key snapshot of QueueStats. Safe for concurrent use.
func (kq *KeyedQueue) StatsAll() map[string]model.QueueStats {
	kq.mu.RLock()
	defer kq.mu.RUnlock()
	result := make(map[string]model.QueueStats, len(kq.queues))
	for k, q := range kq.queues {
		enqueued, dequeued, expired, dropped := q.Stats()
		result[k] = model.QueueStats{
			Depth:         q.Len(),
			Capacity:      q.Cap(),
			EnqueuedTotal: enqueued,
			DequeuedTotal: dequeued,
			ExpiredTotal:  expired,
			DroppedTotal:  dropped,
		}
	}
	return result
}
