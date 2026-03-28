package middleware

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jaar23/hub-router/internal/model"
)

// ipRecord tracks consecutive auth failures and any active lockout for one IP.
type ipRecord struct {
	mu        sync.Mutex
	failures  int
	lockedAt  time.Time // zero if not locked
	resetAt   time.Time // when failure count resets
}

// AuthLockout tracks per-IP authentication failures and blocks IPs that
// exceed the failure threshold for a configurable lockout duration.
//
// This prevents brute-force key guessing: after threshold consecutive
// failures from the same IP, all requests from that IP are rejected with
// HTTP 429 until lockoutDuration elapses.
type AuthLockout struct {
	threshold       int
	lockoutDuration time.Duration
	windowDuration  time.Duration // how long a failure stays "recent"
	logger          *slog.Logger

	mu      sync.Mutex
	records map[string]*ipRecord

	stopCh chan struct{}
	once   sync.Once
}

// NewAuthLockout creates a lockout tracker.
//   - threshold: consecutive failures before lockout.
//   - lockoutDuration: how long the IP stays blocked.
//   - windowDuration: failure counter reset window (e.g. 10m).
func NewAuthLockout(threshold int, lockoutDuration, windowDuration time.Duration, logger *slog.Logger) *AuthLockout {
	al := &AuthLockout{
		threshold:       threshold,
		lockoutDuration: lockoutDuration,
		windowDuration:  windowDuration,
		logger:          logger,
		records:         make(map[string]*ipRecord),
		stopCh:          make(chan struct{}),
	}
	go al.cleanupLoop()
	return al
}

// RecordFailure increments the failure count for ip. Returns true if the IP
// should now be locked out.
func (al *AuthLockout) RecordFailure(ip string) bool {
	rec := al.getOrCreate(ip)
	rec.mu.Lock()
	defer rec.mu.Unlock()

	now := time.Now()

	// Reset failure window if enough time has passed.
	if !rec.resetAt.IsZero() && now.After(rec.resetAt) {
		rec.failures = 0
		rec.lockedAt = time.Time{}
	}

	rec.failures++
	rec.resetAt = now.Add(al.windowDuration)

	if rec.failures >= al.threshold && rec.lockedAt.IsZero() {
		rec.lockedAt = now
		al.logger.Warn("IP locked out after repeated auth failures",
			"ip", ip,
			"failures", rec.failures,
			"duration", al.lockoutDuration,
		)
		return true
	}
	return false
}

// RecordSuccess resets the failure counter for ip.
func (al *AuthLockout) RecordSuccess(ip string) {
	rec := al.getOrCreate(ip)
	rec.mu.Lock()
	rec.failures = 0
	rec.lockedAt = time.Time{}
	rec.resetAt = time.Time{}
	rec.mu.Unlock()
}

// IsLocked reports whether ip is currently locked out.
// Expired lockouts are automatically cleared.
func (al *AuthLockout) IsLocked(ip string) bool {
	al.mu.Lock()
	rec, ok := al.records[ip]
	al.mu.Unlock()
	if !ok {
		return false
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if rec.lockedAt.IsZero() {
		return false
	}
	if time.Since(rec.lockedAt) >= al.lockoutDuration {
		// Lockout expired — reset.
		rec.lockedAt = time.Time{}
		rec.failures = 0
		return false
	}
	return true
}

// UnlockIn returns seconds remaining in the current lockout for ip (0 if not locked).
func (al *AuthLockout) UnlockIn(ip string) int {
	al.mu.Lock()
	rec, ok := al.records[ip]
	al.mu.Unlock()
	if !ok {
		return 0
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.lockedAt.IsZero() {
		return 0
	}
	remaining := al.lockoutDuration - time.Since(rec.lockedAt)
	if remaining <= 0 {
		return 0
	}
	return int(remaining.Seconds()) + 1
}

// Wrap decorates the auth middleware fn so that:
//  1. Locked IPs are rejected before the key is even checked.
//  2. Successful auth records a success (clears counter).
//  3. Failed auth records a failure (may trigger lockout).
//
// keyHeader is the request header carrying the API key (e.g. "X-Online-API-Key").
// authFn should return true if the key is valid.
func (al *AuthLockout) Wrap(keyHeader string, authFn func(string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)

		if al.IsLocked(ip) {
			retry := al.UnlockIn(ip)
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(model.ErrorResponse{
				Code:    "IP_LOCKED",
				Message: fmt.Sprintf("too many failed attempts, retry after %ds", retry),
			})
			return
		}

		key := r.Header.Get(keyHeader)
		if !authFn(key) {
			al.RecordFailure(ip)
			writeAuthError(w)
			return
		}

		al.RecordSuccess(ip)
		next.ServeHTTP(w, r)
	})
}

func (al *AuthLockout) getOrCreate(ip string) *ipRecord {
	al.mu.Lock()
	defer al.mu.Unlock()
	if rec, ok := al.records[ip]; ok {
		return rec
	}
	rec := &ipRecord{}
	al.records[ip] = rec
	return rec
}

// Close stops the background cleanup goroutine.
func (al *AuthLockout) Close() {
	al.once.Do(func() { close(al.stopCh) })
}

func (al *AuthLockout) cleanupLoop() {
	ticker := time.NewTicker(al.lockoutDuration)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			al.cleanup()
		case <-al.stopCh:
			return
		}
	}
}

func (al *AuthLockout) cleanup() {
	cutoff := time.Now().Add(-al.lockoutDuration * 2)
	al.mu.Lock()
	defer al.mu.Unlock()
	for ip, rec := range al.records {
		rec.mu.Lock()
		stale := rec.lockedAt.IsZero() && rec.resetAt.Before(cutoff)
		rec.mu.Unlock()
		if stale {
			delete(al.records, ip)
		}
	}
}
