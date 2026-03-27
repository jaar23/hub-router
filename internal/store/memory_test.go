package store_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/internal/store"
)

func makeResult(id string) *model.Result {
	return &model.Result{
		RequestID:   id,
		Payload:     json.RawMessage(`{"ok":true}`),
		StatusCode:  200,
		CompletedAt: time.Now(),
	}
}

func TestPutGet(t *testing.T) {
	s := store.NewMemoryStore(time.Minute)
	defer s.Close()

	s.RegisterPending("r1")
	if err := s.Put(context.Background(), makeResult("r1")); err != nil {
		t.Fatalf("put: %v", err)
	}

	result, err := s.Get(context.Background(), "r1")
	if err != nil || result == nil {
		t.Fatalf("get: err=%v result=%v", err, result)
	}
	if result.RequestID != "r1" {
		t.Fatalf("unexpected ID %q", result.RequestID)
	}
}

func TestGetUnknownID(t *testing.T) {
	s := store.NewMemoryStore(time.Minute)
	defer s.Close()

	_, err := s.Get(context.Background(), "does-not-exist")
	if err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLongPollWakesOnPut(t *testing.T) {
	s := store.NewMemoryStore(time.Minute)
	defer s.Close()

	s.RegisterPending("r2")

	var wg sync.WaitGroup
	wg.Add(1)
	var gotResult *model.Result
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gotResult, _ = s.Get(ctx, "r2")
	}()

	time.Sleep(50 * time.Millisecond) // let goroutine block in Get
	_ = s.Put(context.Background(), makeResult("r2"))
	wg.Wait()

	if gotResult == nil || gotResult.RequestID != "r2" {
		t.Fatal("long-poll did not receive result")
	}
}

func TestLongPollTimeoutReturnsNil(t *testing.T) {
	s := store.NewMemoryStore(time.Minute)
	defer s.Close()

	s.RegisterPending("r3")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := s.Get(ctx, "r3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result on timeout")
	}
}

// TestNoLostWakeup verifies Put and Get can race without dropping a result.
func TestNoLostWakeup(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := store.NewMemoryStore(time.Minute)
		id := "race-id"
		s.RegisterPending(id)

		var wg sync.WaitGroup
		var got *model.Result
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, _ = s.Get(ctx, id)
		}()

		// Small random delay to trigger race between Put and Get registration.
		time.Sleep(time.Duration(i%3) * time.Millisecond)
		_ = s.Put(context.Background(), makeResult(id))
		wg.Wait()
		_ = s.Close()

		if got == nil {
			t.Fatalf("iteration %d: lost wakeup — result not delivered", i)
		}
	}
}

func TestConcurrentPutGet(t *testing.T) {
	s := store.NewMemoryStore(time.Minute)
	defer s.Close()

	n := 50
	for i := 0; i < n; i++ {
		s.RegisterPending(string(rune(i + 100)))
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := string(rune(i + 100))
		wg.Add(2)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = s.Get(ctx, id)
		}()
		go func() {
			defer wg.Done()
			_ = s.Put(context.Background(), makeResult(id))
		}()
	}
	wg.Wait()
}
