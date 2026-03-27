package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jaar23/hub-router/internal/model"
)

// ProcessorFunc is the callback the local server provides to handle a request.
// It receives a QueuedRequest and must return a Result (or an error).
// Returning an error causes a Result with StatusCode=500 and the error message to be pushed.
type ProcessorFunc func(ctx context.Context, req *model.QueuedRequest) (*model.Result, error)

// LocalClient polls hub-router for pending requests, processes them, and pushes results back.
// It is safe for concurrent use.
type LocalClient struct {
	baseURL string
	apiKey  string
	opts    LocalOptions
}

// NewLocalClient creates a LocalClient.
// baseURL should be the hub-router base URL. apiKey is the X-Local-API-Key value.
func NewLocalClient(baseURL, apiKey string, opts ...func(*LocalOptions)) *LocalClient {
	o := defaultLocalOptions()
	for _, fn := range opts {
		fn(&o)
	}
	return &LocalClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		opts:    o,
	}
}

// WithBatchSize sets the number of requests to pull per cycle.
func WithBatchSize(n int) func(*LocalOptions) {
	return func(o *LocalOptions) { o.BatchSize = n }
}

// WithPollInterval sets the sleep duration when the queue is empty.
func WithPollInterval(d time.Duration) func(*LocalOptions) {
	return func(o *LocalOptions) { o.PollInterval = d }
}

// WithWorkers sets the number of concurrent processing goroutines.
func WithWorkers(n int) func(*LocalOptions) {
	return func(o *LocalOptions) { o.Workers = n }
}

// WithLocalHTTPClient injects a custom HTTP client.
func WithLocalHTTPClient(hc *http.Client) func(*LocalOptions) {
	return func(o *LocalOptions) { o.HTTPClient = hc }
}

// Run starts the poll loop. It blocks until ctx is cancelled.
// Each batch is dispatched concurrently, bounded by opts.Workers.
func (c *LocalClient) Run(ctx context.Context, processor ProcessorFunc) error {
	sem := make(chan struct{}, c.opts.Workers)
	var wg sync.WaitGroup

	for {
		if ctx.Err() != nil {
			break
		}

		requests, err := c.PullBatch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			// Transient error — wait and retry.
			select {
			case <-ctx.Done():
				break
			case <-time.After(c.opts.PollInterval):
			}
			continue
		}

		if len(requests) == 0 {
			select {
			case <-ctx.Done():
				goto done
			case <-time.After(c.opts.PollInterval):
			}
			continue
		}

		for _, req := range requests {
			req := req // capture
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				result := c.process(ctx, processor, req)
				// Best-effort push; ignore error on cancellation.
				_ = c.PushResult(ctx, result)
			}()
		}
	}

done:
	// Wait for in-flight processors to finish.
	wg.Wait()
	return ctx.Err()
}

// PullBatch fetches up to opts.BatchSize pending requests. Never blocks.
func (c *LocalClient) PullBatch(ctx context.Context) ([]*model.QueuedRequest, error) {
	url := fmt.Sprintf("%s/queue/pull?batch=%d", c.baseURL, c.opts.BatchSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Local-API-Key", c.apiKey)

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}

	var pullResp model.PullResponse
	if err := json.NewDecoder(resp.Body).Decode(&pullResp); err != nil {
		return nil, fmt.Errorf("decode pull response: %w", err)
	}
	return pullResp.Requests, nil
}

// PushResult submits a completed result to hub-router.
func (c *LocalClient) PushResult(ctx context.Context, result *model.Result) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/queue/result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Local-API-Key", c.apiKey)

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("push result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("request ID %s not found (may have expired)", result.RequestID)
	}
	if resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// process calls the ProcessorFunc and wraps any error into a Result.
func (c *LocalClient) process(ctx context.Context, fn ProcessorFunc, req *model.QueuedRequest) *model.Result {
	result, err := fn(ctx, req)
	if err != nil {
		errBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
		return &model.Result{
			RequestID:   req.ID,
			Payload:     errBytes,
			StatusCode:  500,
			Error:       err.Error(),
			CompletedAt: time.Now(),
		}
	}
	if result == nil {
		result = &model.Result{RequestID: req.ID, StatusCode: 200}
	}
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now()
	}
	return result
}
