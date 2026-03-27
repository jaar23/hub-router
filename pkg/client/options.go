package client

import (
	"net/http"
	"time"
)

// OnlineOptions configure the OnlineClient.
type OnlineOptions struct {
	// LongPollTimeout is passed to the server as the ?timeout param.
	LongPollTimeout time.Duration
	// MaxRetries is how many 204 responses to tolerate before giving up.
	MaxRetries int
	// HTTPClient allows injection of a custom *http.Client (e.g. with TLS config).
	HTTPClient *http.Client
}

// LocalOptions configure the LocalClient.
type LocalOptions struct {
	// BatchSize is the number of requests to pull per poll cycle.
	BatchSize int
	// PollInterval is the sleep duration when the queue is empty.
	PollInterval time.Duration
	// Workers is the number of concurrent processor goroutines.
	Workers int
	// HTTPClient allows injection of a custom *http.Client.
	HTTPClient *http.Client
}

// defaultHTTPClient returns a sensible default HTTP client.
func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

func defaultOnlineOptions() OnlineOptions {
	return OnlineOptions{
		LongPollTimeout: 30 * time.Second,
		MaxRetries:      10,
		HTTPClient:      defaultHTTPClient(),
	}
}

func defaultLocalOptions() LocalOptions {
	return LocalOptions{
		BatchSize:    10,
		PollInterval: time.Second,
		Workers:      1,
		HTTPClient:   defaultHTTPClient(),
	}
}
