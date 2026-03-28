package middleware

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jaar23/hub-router/internal/model"
)

// bucket is a token-bucket for a single IP.
type bucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// RateLimiter is a per-IP token-bucket rate limiter.
// Each unique client IP gets an independent bucket.
// Stale buckets (no requests for cleanupAfter) are removed by a background goroutine.
type RateLimiter struct {
	rps          float64        // tokens added per second
	burst        float64        // max token accumulation
	cleanupAfter time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket

	stopCh chan struct{}
	once   sync.Once
}

// NewRateLimiter creates a limiter allowing rps requests/second with a burst of burst.
func NewRateLimiter(rps float64, burst int, cleanupAfter time.Duration) *RateLimiter {
	rl := &RateLimiter{
		rps:          rps,
		burst:        float64(burst),
		cleanupAfter: cleanupAfter,
		buckets:      make(map[string]*bucket),
		stopCh:       make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Allow reports whether a request from ip should be allowed.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: rl.burst, lastSeen: time.Now()}
		rl.buckets[ip] = b
	}
	rl.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.lastSeen = now

	// Refill tokens proportional to elapsed time.
	b.tokens += elapsed * rl.rps
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// RetryAfterSeconds returns the wait time in seconds until the bucket has a token.
func (rl *RateLimiter) RetryAfterSeconds(ip string) int {
	if rl.rps <= 0 {
		return 60
	}
	rl.mu.Lock()
	b, ok := rl.buckets[ip]
	rl.mu.Unlock()
	if !ok || b.tokens >= 1 {
		return 0
	}
	b.mu.Lock()
	need := 1.0 - b.tokens
	b.mu.Unlock()
	secs := int(need/rl.rps) + 1
	return secs
}

// Middleware returns an http.Handler middleware that enforces the rate limit.
// Requests that exceed the limit receive HTTP 429 with a Retry-After header.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.Allow(ip) {
			retry := rl.RetryAfterSeconds(ip)
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(model.ErrorResponse{
				Code:    "RATE_LIMITED",
				Message: fmt.Sprintf("rate limit exceeded, retry after %ds", retry),
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Close stops the background cleanup goroutine.
func (rl *RateLimiter) Close() {
	rl.once.Do(func() { close(rl.stopCh) })
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanupAfter / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *RateLimiter) cleanup() {
	cutoff := time.Now().Add(-rl.cleanupAfter)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, b := range rl.buckets {
		b.mu.Lock()
		stale := b.lastSeen.Before(cutoff)
		b.mu.Unlock()
		if stale {
			delete(rl.buckets, ip)
		}
	}
}

// clientIP extracts the real client IP, respecting X-Forwarded-For if set
// by a trusted proxy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first (leftmost) address — the original client.
		for _, part := range splitCSV(xff) {
			if ip := net.ParseIP(part); ip != nil {
				return ip.String()
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(xri); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func splitCSV(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, trimSpace(s[start:]))
	return parts
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
