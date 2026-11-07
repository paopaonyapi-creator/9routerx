// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webexec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// baseClock is the instant every rate-limit test starts from; tests advance
// it with base.Add to drive the window deterministically.
var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestRateLimiterAllow(t *testing.T) {
	tests := []struct {
		name      string
		max       int
		requests  int
		wantAllow int // how many of the requests must be accepted
	}{
		{
			name:      "fresh account is allowed",
			max:       20,
			requests:  1,
			wantAllow: 1,
		},
		{
			name:      "exhausting the window blocks the rest",
			max:       5,
			requests:  8,
			wantAllow: 5,
		},
		{
			name:      "exact max is fully accepted",
			max:       20,
			requests:  20,
			wantAllow: 20,
		},
		{
			name:      "non-positive max falls back to the default",
			max:       0,
			requests:  DefaultMaxReqPerMin + 2,
			wantAllow: DefaultMaxReqPerMin,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewRateLimiter(tt.max, DefaultRateWindow)
			allowed := 0
			for i := 0; i < tt.requests; i++ {
				if limiter.Allow("acc-1", base) {
					allowed++
				}
			}
			if allowed != tt.wantAllow {
				t.Fatalf("allowed = %d, want %d", allowed, tt.wantAllow)
			}
		})
	}
}

func TestRateLimiterWindowSlides(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	if !limiter.Allow("acc-1", base) || !limiter.Allow("acc-1", base) {
		t.Fatalf("first two requests must be allowed")
	}
	if limiter.Allow("acc-1", base) {
		t.Fatalf("third request inside the window must be blocked")
	}
	// 61 seconds later the window has slid past both hits.
	if !limiter.Allow("acc-1", base.Add(61*time.Second)) {
		t.Fatalf("request after the window must be allowed")
	}
}

func TestRateLimiterReset(t *testing.T) {
	limiter := NewRateLimiter(1, time.Minute)
	if !limiter.Allow("acc-1", base) {
		t.Fatalf("first request must be allowed")
	}
	if limiter.Allow("acc-1", base) {
		t.Fatalf("second request inside the window must be blocked")
	}
	limiter.Reset("acc-1")
	if !limiter.Allow("acc-1", base) {
		t.Fatalf("request after Reset must be allowed")
	}
}

func TestRateLimiterRemaining(t *testing.T) {
	limiter := NewRateLimiter(20, time.Minute)
	for i := 0; i < 6; i++ {
		if !limiter.Allow("acc-1", base) {
			t.Fatalf("request %d must be allowed", i+1)
		}
	}
	if got := limiter.Remaining("acc-1", base); got != 14 {
		t.Fatalf("Remaining = %d, want 14", got)
	}
	// A different account has its own window.
	if got := limiter.Remaining("acc-2", base); got != 20 {
		t.Fatalf("Remaining for untouched account = %d, want 20", got)
	}
	// Expiry returns the full budget: the window has slid past every hit.
	if got := limiter.Remaining("acc-1", base.Add(2*time.Minute)); got != 20 {
		t.Fatalf("Remaining after window expiry = %d, want 20", got)
	}
	// Remaining observes without consuming budget.
	if got := limiter.Remaining("acc-1", base.Add(2*time.Minute)); got != 20 {
		t.Fatalf("Remaining after an observing probe = %d, want 20", got)
	}
	if !limiter.Allow("acc-1", base.Add(2*time.Minute)) {
		t.Fatalf("request after expiry must be allowed")
	}
	if got := limiter.Remaining("acc-1", base.Add(2*time.Minute)); got != 19 {
		t.Fatalf("Remaining after the next Allow = %d, want 19", got)
	}
}

func TestRateLimiterEvictsStaleAccounts(t *testing.T) {
	limiter := NewRateLimiter(20, time.Minute)
	if !limiter.Allow("stale", base) {
		t.Fatalf("stale account request must be allowed")
	}
	// Two minutes of silence later the sweep cadence has passed: the next
	// Allow evicts the silent account instead of growing the map.
	if !limiter.Allow("fresh", base.Add(staleEvictAfter+time.Second)) {
		t.Fatalf("fresh account request must be allowed")
	}
	limiter.mu.Lock()
	_, staleHits := limiter.hits["stale"]
	_, staleSeen := limiter.lastSeen["stale"]
	limiter.mu.Unlock()
	if staleHits || staleSeen {
		t.Fatalf("silent account must be evicted after %s", staleEvictAfter)
	}
}

func TestRateLimiterEmptyAccountRefused(t *testing.T) {
	limiter := NewRateLimiter(20, time.Minute)
	if limiter.Allow("", base) {
		t.Fatalf("an empty account id must be refused")
	}
}

func TestRateLimiterConcurrentSameAccount(t *testing.T) {
	const (
		goroutines = 32
		perGoroute = 10
		max        = 20
	)
	limiter := NewRateLimiter(max, time.Minute)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroute; i++ {
				if limiter.Allow("acc-1", base) {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if allowed != max {
		t.Fatalf("concurrent Allow accepted %d, want exactly the ceiling %d", allowed, max)
	}
	if got := limiter.Remaining("acc-1", base); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}
}

func TestRateLimiterSetMax(t *testing.T) {
	limiter := NewRateLimiter(20, time.Minute)
	limiter.SetMax(2)
	if limiter.Max() != 2 {
		t.Fatalf("Max = %d, want 2", limiter.Max())
	}
	for i := 0; i < 2; i++ {
		if !limiter.Allow("acc-1", base) {
			t.Fatalf("request %d must be allowed", i+1)
		}
	}
	if limiter.Allow("acc-1", base) {
		t.Fatalf("request over the lowered ceiling must be blocked")
	}
	// A non-positive ceiling falls back to the default instead of disabling
	// the limiter.
	limiter.SetMax(0)
	if limiter.Max() != DefaultMaxReqPerMin {
		t.Fatalf("Max after SetMax(0) = %d, want %d", limiter.Max(), DefaultMaxReqPerMin)
	}
}

func TestSentinelErrorsAreDistinct(t *testing.T) {
	errs := []error{ErrRateLimited, ErrPoolFull, ErrCooldown}
	for i, a := range errs {
		if a == nil {
			t.Fatalf("sentinel %d must not be nil", i)
		}
		for j, b := range errs {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d must be distinct", i, j)
			}
		}
	}
}
