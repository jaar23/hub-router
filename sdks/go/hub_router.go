// Package hubrouter provides a single-file client for hub-router.
//
// Copy this file into your project and adjust the package name — no go get required.
// Uses only the Go standard library. Requires Go 1.21+.
//
// Usage:
//
//	client := NewOnlineClient("https://hub.example.com", "api-key")
//
//	// Simple payload
//	result, err := client.Do(ctx, map[string]any{"query": "hello"}, nil)
//
//	// Forward an incoming HTTP request verbatim (headers + body pass-through)
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    result, err := client.DoRequest(ctx, r)
//	    json.NewEncoder(w).Encode(result.Payload)
//	}
//
//	// Local side — process requests from the queue
//	local := NewLocalClient("https://hub.example.com", "local-key")
//	local.Run(ctx, func(ctx context.Context, req *QueuedRequest) (*Result, error) {
//	    resp := myModel.Infer(req.Payload)
//	    return &Result{RequestID: req.ID, Payload: resp}, nil
//	})
package hubrouter

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
)

// ─── Models ──────────────────────────────────────────────────────────────────

// QueuedRequest is a pending request pulled by the local server.
type QueuedRequest struct {
	ID         string            `json:"id"`
	Payload    json.RawMessage   `json:"payload"`
	Headers    map[string]string `json:"headers"`
	EnqueuedAt time.Time         `json:"enqueued_at"`
	ExpiresAt  time.Time         `json:"expires_at"`
}

// Result is a processed result pushed back by the local server.
type Result struct {
	RequestID   string          `json:"request_id"`
	Payload     json.RawMessage `json:"payload"`
	StatusCode  int             `json:"status_code"`
	Error       string          `json:"error,omitempty"`
	CompletedAt time.Time       `json:"completed_at"`
}

// SubmitResponse is returned by POST /request and POST /request/sync (202).
type SubmitResponse struct {
	ID              string `json:"id"`
	EstimatedWaitMs int64  `json:"estimated_wait_ms"`
}

// PullResponse is returned by GET /queue/pull.
type PullResponse struct {
	Requests []*QueuedRequest `json:"requests"`
	Count    int              `json:"count"`
}

// hop-by-hop headers that must not be forwarded between proxies.
var hopByHopHeaders = map[string]bool{
	"Content-Length":      true,
	"Host":                true,
	"Transfer-Encoding":   true,
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Upgrade":             true,
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

// ─── OnlineClient ─────────────────────────────────────────────────────────────

// OnlineClient submits requests to hub-router and waits for results.
// It is safe for concurrent use.
type OnlineClient struct {
	baseURL         string
	apiKey          string
	longPollTimeout time.Duration
	maxRetries      int
	httpClient      *http.Client
}

// NewOnlineClient creates an OnlineClient with sensible defaults.
// Functional options (WithLongPollTimeout, WithMaxRetries, WithHTTPClient) may be applied.
func NewOnlineClient(baseURL, apiKey string, opts ...func(*OnlineClient)) *OnlineClient {
	c := &OnlineClient{
		baseURL:         strings.TrimRight(baseURL, "/"),
		apiKey:          apiKey,
		longPollTimeout: 30 * time.Second,
		maxRetries:      10,
		httpClient:      defaultHTTPClient(),
	}
	for _, fn := range opts {
		fn(c)
	}
	return c
}

// WithLongPollTimeout sets the long-poll timeout per request.
func WithLongPollTimeout(d time.Duration) func(*OnlineClient) {
	return func(c *OnlineClient) { c.longPollTimeout = d }
}

// WithMaxRetries sets the maximum number of 204 retries before WaitResult gives up.
func WithMaxRetries(n int) func(*OnlineClient) {
	return func(c *OnlineClient) { c.maxRetries = n }
}

// WithHTTPClient injects a custom *http.Client (e.g. with TLS config or tracing).
func WithHTTPClient(hc *http.Client) func(*OnlineClient) {
	return func(c *OnlineClient) { c.httpClient = hc }
}

// Submit enqueues a request and returns its correlation ID.
// headers is optional metadata forwarded with the request.
func (c *OnlineClient) Submit(
	ctx context.Context,
	payload any,
	headers map[string]string,
) (string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	body := map[string]any{
		"payload": json.RawMessage(payloadBytes),
		"headers": headers,
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/request", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Online-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return "", fmt.Errorf("queue full: %s", e.Message)
	}
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}

	var sr SubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("decode submit response: %w", err)
	}
	return sr.ID, nil
}

// WaitResult long-polls GET /result/:id until the result arrives or maxRetries 204s occur.
// On success returns the Result. On exhaustion returns an error.
func (c *OnlineClient) WaitResult(ctx context.Context, requestID string) (*Result, error) {
	url := fmt.Sprintf("%s/result/%s?timeout=%s",
		c.baseURL, requestID, c.longPollTimeout)

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Online-API-Key", c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("poll result: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var result Result
			err = json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("decode result: %w", err)
			}
			return &result, nil

		case http.StatusNoContent:
			resp.Body.Close()
			continue

		case http.StatusNotFound:
			resp.Body.Close()
			return nil, fmt.Errorf("request ID %s not found (may have expired)", requestID)

		default:
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
		}
	}

	return nil, fmt.Errorf("result not available after %d retries for request %s",
		c.maxRetries, requestID)
}

// DoSync sends POST /request/sync — enqueue + wait in one HTTP connection.
//
// Returns:
//   - (result, "", nil)     — fast path: result arrived before timeout.
//   - (nil, requestID, nil) — slow path: local server is busy; call WaitResult.
//   - (nil, "", err)        — queue full, auth error, etc.
func (c *OnlineClient) DoSync(
	ctx context.Context,
	payload any,
	headers map[string]string,
) (*Result, string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal payload: %w", err)
	}
	body := map[string]any{
		"payload": json.RawMessage(payloadBytes),
		"headers": headers,
	}
	bodyBytes, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/request/sync?timeout=%s", c.baseURL, c.longPollTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Online-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("sync request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var result Result
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, "", fmt.Errorf("decode sync result: %w", err)
		}
		return &result, "", nil

	case http.StatusAccepted:
		var sr SubmitResponse
		if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			return nil, "", fmt.Errorf("decode submit response: %w", err)
		}
		return nil, sr.ID, nil

	case http.StatusServiceUnavailable:
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return nil, "", fmt.Errorf("queue full: %s", e.Message)

	default:
		raw, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}
}

// Do submits a request and waits for its result.
// Uses DoSync for a single round-trip when the local server is fast,
// falling back to WaitResult for slow processors. Transparent to the caller.
func (c *OnlineClient) Do(
	ctx context.Context,
	payload any,
	headers map[string]string,
) (*Result, error) {
	result, id, err := c.DoSync(ctx, payload, headers)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil // fast path — done in one request
	}
	return c.WaitResult(ctx, id) // slow path — keep polling
}

// DoRequest extracts the body and headers from an incoming *http.Request and
// forwards them verbatim to hub-router. Hop-by-hop headers are excluded.
//
// This makes hub-router behave as a transparent proxy: the local server
// receives the exact headers and body that the original client sent.
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    result, err := client.DoRequest(r.Context(), r)
//	    if err != nil { http.Error(w, err.Error(), 502); return }
//	    w.Header().Set("Content-Type", "application/json")
//	    w.Write(result.Payload)
//	}
func (c *OnlineClient) DoRequest(ctx context.Context, r *http.Request) (*Result, error) {
	headers := make(map[string]string)
	for k, vals := range r.Header {
		if !hopByHopHeaders[k] {
			headers[k] = strings.Join(vals, ", ")
		}
	}

	var payload string
	if r.Body != nil {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		payload = string(raw)
	}

	return c.Do(ctx, payload, headers)
}

// ─── LocalClient ─────────────────────────────────────────────────────────────

// ProcessorFunc is the callback the local server provides to handle a request.
// Return an error to send a 500 result back to the online server.
type ProcessorFunc func(ctx context.Context, req *QueuedRequest) (*Result, error)

// LocalClient polls hub-router for pending requests, processes them, and pushes
// results back. It is safe for concurrent use.
type LocalClient struct {
	baseURL      string
	apiKey       string
	batchSize    int
	pollInterval time.Duration
	workers      int
	httpClient   *http.Client
}

// NewLocalClient creates a LocalClient with sensible defaults.
// Functional options (WithBatchSize, WithPollInterval, WithWorkers, WithLocalHTTPClient) may be applied.
func NewLocalClient(baseURL, apiKey string, opts ...func(*LocalClient)) *LocalClient {
	c := &LocalClient{
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		batchSize:    10,
		pollInterval: time.Second,
		workers:      1,
		httpClient:   defaultHTTPClient(),
	}
	for _, fn := range opts {
		fn(c)
	}
	return c
}

// WithBatchSize sets the number of requests to pull per poll cycle.
func WithBatchSize(n int) func(*LocalClient) {
	return func(c *LocalClient) { c.batchSize = n }
}

// WithPollInterval sets the sleep duration when the queue is empty.
func WithPollInterval(d time.Duration) func(*LocalClient) {
	return func(c *LocalClient) { c.pollInterval = d }
}

// WithWorkers sets the number of concurrent processing goroutines.
func WithWorkers(n int) func(*LocalClient) {
	return func(c *LocalClient) { c.workers = n }
}

// WithLocalHTTPClient injects a custom *http.Client for the LocalClient.
func WithLocalHTTPClient(hc *http.Client) func(*LocalClient) {
	return func(c *LocalClient) { c.httpClient = hc }
}

// Run starts the poll loop. It blocks until ctx is cancelled.
// Each batch is dispatched concurrently, bounded by workers.
func (c *LocalClient) Run(ctx context.Context, processor ProcessorFunc) error {
	sem := make(chan struct{}, c.workers)
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
			select {
			case <-ctx.Done():
				goto done
			case <-time.After(c.pollInterval):
			}
			continue
		}

		if len(requests) == 0 {
			select {
			case <-ctx.Done():
				goto done
			case <-time.After(c.pollInterval):
			}
			continue
		}

		for _, req := range requests {
			req := req
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				result := c.process(ctx, processor, req)
				_ = c.PushResult(ctx, result) // best-effort
			}()
		}
	}

done:
	wg.Wait()
	return ctx.Err()
}

// PullBatch fetches up to batchSize pending requests. Never blocks.
func (c *LocalClient) PullBatch(ctx context.Context) ([]*QueuedRequest, error) {
	url := fmt.Sprintf("%s/queue/pull?batch=%d", c.baseURL, c.batchSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Local-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}

	var pr PullResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decode pull response: %w", err)
	}
	return pr.Requests, nil
}

// PushResult submits a completed result to hub-router.
func (c *LocalClient) PushResult(ctx context.Context, result *Result) error {
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now()
	}
	body, _ := json.Marshal(result)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/queue/result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Local-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
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

func (c *LocalClient) process(
	ctx context.Context,
	fn ProcessorFunc,
	req *QueuedRequest,
) *Result {
	result, err := fn(ctx, req)
	if err != nil {
		errBytes, _ := json.Marshal(map[string]string{"error": err.Error()})
		return &Result{
			RequestID:   req.ID,
			Payload:     errBytes,
			StatusCode:  500,
			Error:       err.Error(),
			CompletedAt: time.Now(),
		}
	}
	if result == nil {
		result = &Result{RequestID: req.ID, StatusCode: 200}
	}
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now()
	}
	return result
}
