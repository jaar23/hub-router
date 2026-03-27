package queue_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/internal/queue"
)

func makeReq(id string, ttl time.Duration) *model.QueuedRequest {
	now := time.Now()
	return &model.QueuedRequest{
		ID:         id,
		Payload:    json.RawMessage(`{"key":"value"}`),
		EnqueuedAt: now,
		ExpiresAt:  now.Add(ttl),
	}
}

func TestEnqueueDequeue(t *testing.T) {
	q := queue.NewMemoryQueue(10)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		req := makeReq(string(rune('a'+i)), time.Minute)
		if err := q.Enqueue(ctx, req); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	if got := q.Len(); got != 3 {
		t.Fatalf("expected Len=3, got %d", got)
	}

	reqs, err := q.Dequeue(ctx, 10)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if len(reqs) != 3 {
		t.Fatalf("expected 3 items, got %d", len(reqs))
	}
}

func TestQueueFull(t *testing.T) {
	q := queue.NewMemoryQueue(2)
	ctx := context.Background()

	_ = q.Enqueue(ctx, makeReq("1", time.Minute))
	_ = q.Enqueue(ctx, makeReq("2", time.Minute))

	err := q.Enqueue(ctx, makeReq("3", time.Minute))
	if err != queue.ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
}

func TestExpiredItemsDropped(t *testing.T) {
	q := queue.NewMemoryQueue(10)
	ctx := context.Background()

	// Enqueue one already-expired request.
	expired := makeReq("expired", -time.Second)
	_ = q.Enqueue(ctx, expired)

	valid := makeReq("valid", time.Minute)
	_ = q.Enqueue(ctx, valid)

	reqs, err := q.Dequeue(ctx, 10)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 non-expired item, got %d", len(reqs))
	}
	if reqs[0].ID != "valid" {
		t.Fatalf("expected 'valid', got %q", reqs[0].ID)
	}
}

func TestConcurrentEnqueueDequeue(t *testing.T) {
	q := queue.NewMemoryQueue(1000)
	ctx := context.Background()

	var wg sync.WaitGroup
	n := 100

	// Concurrent enqueue.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = q.Enqueue(ctx, makeReq(string(rune(i)), time.Minute))
		}(i)
	}
	wg.Wait()

	// Concurrent dequeue.
	total := 0
	var mu sync.Mutex
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reqs, _ := q.Dequeue(ctx, 20)
			mu.Lock()
			total += len(reqs)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if total != n {
		t.Fatalf("expected %d items dequeued total, got %d", n, total)
	}
}
