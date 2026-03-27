package store

import (
	"context"
	"sync"
	"time"

	"github.com/jaar23/hub-router/internal/model"
)

type entry struct {
	result    *model.Result
	expiresAt time.Time
}

// MemoryStore is a thread-safe, in-memory ResultStore with long-poll support.
//
// Race-free long-poll design:
//   - Put always writes to results map before signalling waiters.
//   - Get's slow path re-reads results map after registering the waiter channel,
//     eliminating the "lost wakeup" race where Put fires between the first
//     results-miss and the waiter registration.
type MemoryStore struct {
	mu      sync.Mutex
	results map[string]*entry        // requestID → stored result
	pending map[string]struct{}      // requestIDs that have been submitted but not yet resolved
	waiters map[string]chan *model.Result // requestID → waiter channel (one per in-flight long-poll)

	resultTTL time.Duration
	stopCh    chan struct{}
	once      sync.Once
}

// NewMemoryStore creates a store that sweeps expired results every sweepInterval.
func NewMemoryStore(resultTTL time.Duration) *MemoryStore {
	s := &MemoryStore{
		results:   make(map[string]*entry),
		pending:   make(map[string]struct{}),
		waiters:   make(map[string]chan *model.Result),
		resultTTL: resultTTL,
		stopCh:    make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

// RegisterPending marks a request ID as known so Get can differentiate
// "result not yet ready" from "ID never existed".
func (s *MemoryStore) RegisterPending(requestID string) {
	s.mu.Lock()
	s.pending[requestID] = struct{}{}
	s.mu.Unlock()
}

// Put stores a result and wakes any long-poll waiters for that ID.
func (s *MemoryStore) Put(_ context.Context, result *model.Result) error {
	s.mu.Lock()
	// 1. Store result first — so a concurrent Get sees it after registering.
	s.results[result.RequestID] = &entry{
		result:    result,
		expiresAt: time.Now().Add(s.resultTTL),
	}
	delete(s.pending, result.RequestID)

	// 2. Signal waiter if one is registered.
	ch, hasWaiter := s.waiters[result.RequestID]
	s.mu.Unlock()

	if hasWaiter {
		select {
		case ch <- result:
		default: // waiter already timed out and cleaned itself up
		}
	}
	return nil
}

// Get returns the result for requestID. If not yet available, it blocks until
// ctx is cancelled or the result arrives.
// Returns (nil, nil) when ctx expires — caller should return HTTP 204.
// Returns (nil, ErrNotFound) when the ID is unknown.
func (s *MemoryStore) Get(ctx context.Context, requestID string) (*model.Result, error) {
	// Fast path: result already stored.
	s.mu.Lock()
	if e, ok := s.results[requestID]; ok {
		result := e.result
		delete(s.results, requestID)
		s.mu.Unlock()
		return result, nil
	}

	// Check that the ID is known (either still pending, or result is present).
	if _, isPending := s.pending[requestID]; !isPending {
		s.mu.Unlock()
		return nil, ErrNotFound
	}

	// Slow path: register a waiter channel, then re-check (double-check to close race).
	ch := make(chan *model.Result, 1)
	s.waiters[requestID] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.waiters, requestID)
		s.mu.Unlock()
	}()

	// Re-check after registering: Put may have fired between our first miss and registration.
	s.mu.Lock()
	if e, ok := s.results[requestID]; ok {
		result := e.result
		delete(s.results, requestID)
		s.mu.Unlock()
		return result, nil
	}
	s.mu.Unlock()

	// Block until result arrives or context expires.
	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return nil, nil // caller returns 204, client retries
	}
}

// Delete removes a result from the store (no-op if not present).
func (s *MemoryStore) Delete(_ context.Context, requestID string) error {
	s.mu.Lock()
	delete(s.results, requestID)
	delete(s.pending, requestID)
	s.mu.Unlock()
	return nil
}

// ActiveWaiters returns the number of long-poll connections currently blocking.
func (s *MemoryStore) ActiveWaiters() int {
	s.mu.Lock()
	n := len(s.waiters)
	s.mu.Unlock()
	return n
}

// ResultCount returns the number of stored (uncollected) results.
func (s *MemoryStore) ResultCount() int {
	s.mu.Lock()
	n := len(s.results)
	s.mu.Unlock()
	return n
}

// Close stops the background sweep goroutine.
func (s *MemoryStore) Close() error {
	s.once.Do(func() { close(s.stopCh) })
	return nil
}

func (s *MemoryStore) sweepLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.sweepExpired()
		case <-s.stopCh:
			return
		}
	}
}

func (s *MemoryStore) sweepExpired() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.results {
		if now.After(e.expiresAt) {
			delete(s.results, id)
		}
	}
}
