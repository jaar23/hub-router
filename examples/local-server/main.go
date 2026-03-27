// Example: local processing server using the Go hub-router LocalClient.
//
// Run alongside the online-server example:
//   HR_ONLINE_API_KEYS=online-secret \
//   HR_LOCAL_API_KEYS=local-secret \
//   go run ./cmd/hub-router &
//
//   go run ./examples/local-server
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/jaar23/hub-router/internal/model"
	"github.com/jaar23/hub-router/pkg/client"
)

func main() {
	c := client.NewLocalClient(
		"http://localhost:8080",
		"local-secret",
		client.WithBatchSize(20),
		client.WithWorkers(4),
		client.WithPollInterval(500*time.Millisecond),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("Local server starting — polling hub-router for requests...")

	err := c.Run(ctx, processor)
	if err != nil && err != context.Canceled {
		log.Fatalf("run: %v", err)
	}
	fmt.Println("Local server stopped.")
}

// processor handles each incoming request.
// Replace this with your real logic (DB queries, ML inference, etc.).
func processor(ctx context.Context, req *model.QueuedRequest) (*model.Result, error) {
	// Decode the incoming payload.
	var input map[string]any
	if err := json.Unmarshal(req.Payload, &input); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	fmt.Printf("Processing request %s: %v\n", req.ID, input)

	// Simulate async work (e.g. model inference).
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Build and return the result.
	answer := map[string]any{
		"input":    input,
		"answer":   "42",
		"processed_at": time.Now().Format(time.RFC3339),
	}
	answerBytes, _ := json.Marshal(answer)

	return &model.Result{
		RequestID:  req.ID,
		Payload:    answerBytes,
		StatusCode: 200,
	}, nil
}
