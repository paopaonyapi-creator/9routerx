// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"9routerx/proxy/internal/auth"
	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/log"
	"9routerx/proxy/internal/models"
	"9routerx/proxy/internal/observ"
	"9routerx/proxy/internal/securitylog"
)

// keypolicy_requests.go is the per-interval request cap of the serenhope
// per-key governance set (port(serenhope) TASK-1.4, upstream
// requestLimit/usedRequests/resetInterval/lastResetAt). The gate counts
// admissions in memory — the same design upstream's checkRateLimits uses for
// RPM: state lives per process, is reserved at admission time so concurrent
// requests cannot overshoot, and is seeded from the DB counters so a restart
// continues a window rather than forgetting it. The DB row itself is bumped
// off the response path by the usage writer (BumpApiKeyRequests), which is
// what the dashboard and the post-restart seed read.

// requestWindow is one key's live window. start is when the current window
// opened; a lifetime cap (interval 0) leaves it zero and never rolls.
type requestWindow struct {
	limit    int64
	interval time.Duration
	start    time.Time
	count    int64
	lastUsed time.Time
}

// maxTrackedRequestWindows bounds the tracker's map. A key enters the map on
// its first capped request and is evicted least-recently-used — the same
// bound-and-evict contract the RPM limiter applies to its own maps.
const maxTrackedRequestWindows = 4096

// requestWindowTracker holds one window per key ID. Every read is under the
// mutex: check both inspects and advances the window, and two goroutines
// racing the same key must count exactly two admissions.
type requestWindowTracker struct {
	mu      sync.Mutex
	entries map[string]*requestWindow
}

func newRequestWindowTracker() *requestWindowTracker {
	return &requestWindowTracker{entries: make(map[string]*requestWindow)}
}

// check evaluates one admission at instant `at` against the key's cap. It
// reports whether the request is over the cap and, when it is, when waiting
// helps again.
func (t *requestWindowTracker) check(key *models.APIKey, at time.Time) (exceeded bool, resetAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	interval := time.Duration(key.RequestResetInterval) * time.Minute
	w, ok := t.entries[key.ID]
	if !ok || w.limit != key.RequestLimit || w.interval != interval {
		w = seedRequestWindow(key, interval, at)
		t.entries[key.ID] = w
		t.evictLocked()
	}
	w.lastUsed = at

	// The window rolls by whole elapsed periods, upstream-style: a window
	// that opened at 10:00 with a 60-minute interval resets at 11:00, and a
	// request at 11:20 opens a window that resets at 12:00 — the boundary
	// stays aligned to when traffic actually started rather than to the
	// wall clock.
	if w.interval > 0 {
		elapsed := at.Sub(w.start)
		if elapsed >= w.interval {
			w.start = w.start.Add(time.Duration(int64(elapsed)/int64(w.interval)) * w.interval)
			w.count = 0
		}
	}

	if w.count >= w.limit {
		if w.interval > 0 {
			return true, w.start.Add(w.interval)
		}
		// A lifetime cap has no boundary: waiting never helps.
		return true, time.Time{}
	}
	w.count++
	return false, time.Time{}
}

// seedRequestWindow opens a window from the key row's persisted state.
func seedRequestWindow(key *models.APIKey, interval time.Duration, at time.Time) *requestWindow {
	w := &requestWindow{limit: key.RequestLimit, interval: interval, lastUsed: at}
	if interval == 0 {
		// Lifetime cap: the persisted counter is the whole truth, so a
		// restart does not give a nearly-exhausted key a fresh budget.
		w.count = key.UsedRequests
		return w
	}
	// A stamped window continues across the restart when it has not rolled
	// yet; a window that already expired seeds as a fresh one at the first
	// boundary after the stamp. No stamp at all means the first capped
	// request opens the window.
	if base, err := time.Parse(time.RFC3339, key.RequestResetAt); err == nil && !base.IsZero() {
		w.start = base
		if at.Sub(base) >= interval {
			w.start = w.start.Add(time.Duration(int64(at.Sub(base))/int64(interval)) * interval)
			return w
		}
		w.count = key.UsedRequests
		return w
	}
	w.start = at
	return w
}

// evictLocked drops the least-recently-used window once the map outgrows its
// bound. Called with the mutex held; the scan is O(n) but runs only when a
// new key pushes past the bound, which is rare against 4096.
func (t *requestWindowTracker) evictLocked() {
	for len(t.entries) > maxTrackedRequestWindows {
		oldest := ""
		var oldestAt time.Time
		for id, w := range t.entries {
			if oldest == "" || w.lastUsed.Before(oldestAt) {
				oldest, oldestAt = id, w.lastUsed
			}
		}
		if oldest == "" {
			return
		}
		delete(t.entries, oldest)
	}
}

// refuseRequestLimit answers a key that has spent its per-interval cap
// (port(serenhope) TASK-1.4). The refusal is upstream's exact sentence with
// Retry-After naming the window's remaining seconds; a lifetime cap carries
// no Retry-After because waiting never helps — the same convention
// rejectQuotaExhausted keeps for the lifetime token quota.
func (p *KeyPolicy) refuseRequestLimit(w http.ResponseWriter, r *http.Request, apiKey *models.APIKey) bool {
	if apiKey.RequestLimit <= 0 {
		return false
	}
	exceeded, resetAt := p.windows.check(apiKey, p.now())
	if !exceeded {
		return false
	}
	clientIP := auth.LoginClientIP(r)
	w.Header().Set("X-Request-Limit-Limit", strconv.FormatInt(apiKey.RequestLimit, 10))
	w.Header().Set("X-Request-Limit-Remaining", "0")
	if !resetAt.IsZero() {
		secs := int(resetAt.Sub(p.now()).Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		w.Header().Set("X-Request-Limit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
	}
	log.Warn("rate limit", "request limit exceeded", "key", apiKey.ID, "limit", apiKey.RequestLimit)
	observ.IncRateLimitReject("request_limit", apiKey.ID)
	securitylog.Record(securitylog.Entry{
		Type:      "rate_limit_exceeded",
		Severity:  "warn",
		IP:        clientIP,
		Actor:     apiKey.ID,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    http.StatusTooManyRequests,
		Detail:    fmt.Sprintf("Request limit exceeded (%d)", apiKey.RequestLimit),
		UserAgent: r.UserAgent(),
	})
	handlerutil.WriteJSONError(w, http.StatusTooManyRequests, "API key request limit exceeded")
	return true
}
