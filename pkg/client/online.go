// Package client provides ready-to-use clients for both sides of hub-router.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jaar23/hub-router/internal/model"
)

// OnlineClient submits requests to hub-router and waits for results.
// It is safe for concurrent use.
type OnlineClient struct {
	baseURL string
	apiKey  string
	opts    OnlineOptions
}

// NewOnlineClient creates an OnlineClient.
// baseURL should be the hub-router base URL, e.g. "https://hub.example.com".
// apiKey is the X-Online-API-Key value.
func NewOnlineClient(baseURL, apiKey string, opts ...func(*OnlineOptions)) *OnlineClient {
	o := defaultOnlineOptions()
	for _, fn := range opts {
		fn(&o)
	}
	return &OnlineClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		opts:    o,
	}
}

// WithOnlineLongPollTimeout sets the long-poll timeout per request.
func WithOnlineLongPollTimeout(d interface{ String() string }) func(*OnlineOptions) {
	return func(o *OnlineOptions) {
		if dd, ok := d.(interface{ Nanoseconds() int64 }); ok {
			_ = dd
		}
	}
}

// Submit enqueues a request and returns its correlation ID.
// headers is optional metadata forwarded with the request.
func (c *OnlineClient) Submit(ctx context.Context, payload any, headers map[string]string) (string, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	body := map[string]any{
		"payload": json.RawMessage(payloadBytes),
		"headers": headers,
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/request", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Online-API-Key", c.apiKey)

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		var errResp model.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return "", fmt.Errorf("queue full: %s", errResp.Message)
	}
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}

	var submitResp model.SubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
		return "", fmt.Errorf("decode submit response: %w", err)
	}
	return submitResp.ID, nil
}

// WaitResult polls GET /result/:id using long-polling until the result arrives
// or MaxRetries 204 responses are received.
// On success returns the Result. On exhaustion returns an error.
func (c *OnlineClient) WaitResult(ctx context.Context, requestID string) (*model.Result, error) {
	timeout := c.opts.LongPollTimeout.String()
	url := fmt.Sprintf("%s/result/%s?timeout=%s", c.baseURL, requestID, timeout)

	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Online-API-Key", c.apiKey)

		resp, err := c.opts.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("poll result: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var result model.Result
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				resp.Body.Close()
				return nil, fmt.Errorf("decode result: %w", err)
			}
			resp.Body.Close()
			return &result, nil

		case http.StatusNoContent:
			// Server timed out waiting — retry (long-poll is stateless, same ID).
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

	return nil, fmt.Errorf("result not available after %d retries for request %s", c.opts.MaxRetries, requestID)
}

// Do is a convenience wrapper: Submit + WaitResult in one call.
func (c *OnlineClient) Do(ctx context.Context, payload any, headers map[string]string) (*model.Result, error) {
	id, err := c.Submit(ctx, payload, headers)
	if err != nil {
		return nil, err
	}
	return c.WaitResult(ctx, id)
}
