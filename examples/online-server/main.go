// Example: online server using the Go hub-router OnlineClient.
//
// Run:
//   HR_ONLINE_API_KEYS=online-secret \
//   HR_LOCAL_API_KEYS=local-secret \
//   go run ./cmd/hub-router &
//
//   go run ./examples/online-server
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jaar23/hub-router/pkg/client"
)

func main() {
	c := client.NewOnlineClient(
		"http://localhost:8080",
		"online-secret",
		func(o *client.OnlineOptions) {
			o.LongPollTimeout = 15 * time.Second
			o.MaxRetries = 5
		},
	)

	ctx := context.Background()

	// --- Single request: submit + wait for result ---
	payload := map[string]any{
		"input": "What is 2 + 2?",
	}
	fmt.Println("Submitting request...")
	result, err := c.Do(ctx, payload, nil)
	if err != nil {
		log.Fatalf("request failed: %v", err)
	}

	var out any
	_ = json.Unmarshal(result.Payload, &out)
	fmt.Printf("Result (status=%d): %v\n", result.StatusCode, out)

	// --- Fan-out: multiple concurrent requests ---
	type reqResult struct {
		id  string
		err error
		res *client.OnlineClient
	}

	fmt.Println("\nSubmitting 3 requests concurrently...")
	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		p := map[string]any{"n": i}
		id, err := c.Submit(ctx, p, nil)
		if err != nil {
			log.Fatalf("submit %d: %v", i, err)
		}
		ids[i] = id
		fmt.Printf("  submitted [%d] → id=%s\n", i, id)
	}

	for _, id := range ids {
		res, err := c.WaitResult(ctx, id)
		if err != nil {
			log.Printf("  wait %s: %v", id, err)
			continue
		}
		var out any
		_ = json.Unmarshal(res.Payload, &out)
		fmt.Printf("  result for %s (status=%d): %v\n", id, res.StatusCode, out)
	}
}
