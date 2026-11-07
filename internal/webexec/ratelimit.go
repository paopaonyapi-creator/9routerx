// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webexec

import (
	"sync"
	"time"
)

// Rate-limit defaults for the web-executor pool: max 20 requests per account
// per minute, sliding window.
const (
	DefaultMaxReqPerMin = 20
	DefaultRateWindow   = time.Minute
	// staleEvictAfter is the silence bound on the limiter map: an account with
	// no request for 2 minutes is dropped, so the map never grows with the
	// number of accounts ever seen — it holds only accounts seen recently.
	staleEvictAfter = 2 * time.Minute
	// sweepEvery spaces the eviction passes so Allow stays cheap: a full scan
	// of a map bounded by recently-active accounts, once a minute.
	sweepEvery = staleEvictAfter / 2
	// maxTrackedAccounts is the hard ceiling that forces an eviction pass even
	// between sweeps, bounding the map against an account-ID rotation flood.
	maxTrackedAccounts = 4096
)

// RateLimiter is a mutex-guarded sliding-window rate limiter, one window per
// account. Clock-injection on Allow keeps it deterministic under test.
type RateLimiter struct {
	mu sync.Mutex
	// hits holds each account's request timestamps inside the window. Entries
	// are pruned on every Allow, so a slice never grows past max.
	hits map[string][]time.Time
	// lastSeen records the newest request per account and is what the
	// eviction pass ages entries out on.
	lastSeen  map[string]time.Time
	max       int
	window    time.Duration
	lastSweep time.Time
}

// NewRateLimiter builds a limiter of max requests per window per account.
// Non-positive arguments fall back to the defaults (20/min).
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	if max <= 0 {
		max = DefaultMaxReqPerMin
	}
	if window <= 0 {
		window = DefaultRateWindow
	}
	return &RateLimiter{
		hits:     make(map[string][]time.Time),
		lastSeen: make(map[string]time.Time),
		max:      max,
		window:   window,
	}
}

// Allow records one request for the account and reports whether it fits in
// the account's current window.
func (l *RateLimiter) Allow(accountID string, now time.Time) bool {
	if accountID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) >= sweepEvery || len(l.lastSeen) >= maxTrackedAccounts {
		l.evictStaleLocked(now)
	}
	if len(l.windowHitsLocked(accountID, now)) >= l.max {
		return false
	}
	l.hits[accountID] = append(l.hits[accountID], now)
	l.lastSeen[accountID] = now
	return true
}

// Reset drops the account's window entirely — the retry path after a session
// refresh uses it to let the next request through immediately.
func (l *RateLimiter) Reset(accountID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, accountID)
	delete(l.lastSeen, accountID)
}

// Remaining reports how many requests the account may still make in its
// current window. It prunes but does not record anything.
func (l *RateLimiter) Remaining(accountID string, now time.Time) int {
	if accountID == "" {
		return l.max
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.max - len(l.windowHitsLocked(accountID, now))
}

// SetMax updates the ceiling; the health job applies the operator's
// webExecMaxReqPerMin setting on every pass. The current windows are kept.
func (l *RateLimiter) SetMax(max int) {
	if max <= 0 {
		max = DefaultMaxReqPerMin
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.max = max
}

// Max returns the configured ceiling.
func (l *RateLimiter) Max() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.max
}

// windowHitsLocked returns the account's in-window timestamps, pruning the
// expired ones in place. Callers hold mu.
func (l *RateLimiter) windowHitsLocked(accountID string, now time.Time) []time.Time {
	hits := l.hits[accountID]
	kept := hits[:0]
	for _, t := range hits {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	l.hits[accountID] = kept
	return kept
}

// evictStaleLocked drops accounts silent for staleEvictAfter. Callers hold mu.
func (l *RateLimiter) evictStaleLocked(now time.Time) {
	for accountID, last := range l.lastSeen {
		if now.Sub(last) >= staleEvictAfter {
			delete(l.hits, accountID)
			delete(l.lastSeen, accountID)
		}
	}
	l.lastSweep = now
}
