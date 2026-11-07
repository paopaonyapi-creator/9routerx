// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package resilience

import (
	"sync"
	"testing"
	"time"
)

func TestIsProviderFailureStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		// The FASE 5 task doc pins the provider-failure set to EXACTLY
		// {408, 500, 502, 503, 504}. This test previously accepted 520/524 as
		// well; the set was narrowed to match the task doc, so those two now
		// deliberately fail here — they are edge-statuses, not provider
		// failures.
		{name: "request timeout 408", status: 408, want: true},
		{name: "internal error 500", status: 500, want: true},
		{name: "bad gateway 502", status: 502, want: true},
		{name: "service unavailable 503", status: 503, want: true},
		{name: "gateway timeout 504", status: 504, want: true},
		{name: "cloudflare edge 520 is not a provider failure", status: 520, want: false},
		{name: "cloudflare edge 524 is not a provider failure", status: 524, want: false},
		// 429 is deliberately absent: it is per-account/per-model rate
		// limiting, and counting it would trip the breaker once a few accounts
		// are throttled, blocking every healthy account with them.
		{name: "rate limit 429", status: 429, want: false},
		{name: "bad request 400", status: 400, want: false},
		{name: "unauthorized 401", status: 401, want: false},
		{name: "forbidden 403", status: 403, want: false},
		{name: "not found 404", status: 404, want: false},
		{name: "server error 501", status: 501, want: false},
		{name: "server error 505", status: 505, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsProviderFailureStatus(tt.status); got != tt.want {
				t.Errorf("IsProviderFailureStatus(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestIsRateLimitStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "429 is a rate limit", status: 429, want: true},
		{name: "403 is not", status: 403, want: false},
		{name: "500 is not", status: 500, want: false},
		{name: "transport error 0 is not", status: 0, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRateLimitStatus(tt.status); got != tt.want {
				t.Errorf("IsRateLimitStatus(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestBreakerOpensAfterThreshold(t *testing.T) {
	ResetAllBreakers()
	b := BreakerFor("test-open", BreakerOptions{FailureThreshold: 3, ResetTimeout: time.Second})

	if b.State() != StateClosed {
		t.Fatalf("initial state = %s, want CLOSED", b.State())
	}
	for range 2 {
		b.RecordFailure()
	}
	// Two failures with a threshold of 3 must degrade, not open.
	if got := b.State(); got != StateDegraded {
		t.Errorf("after 2/3 failures state = %s, want DEGRADED", got)
	}
	// A degraded breaker still passes traffic.
	if !b.Allow() {
		t.Error("DEGRADED breaker refused a request")
	}
	b.RecordFailure()
	if got := b.State(); got != StateOpen {
		t.Errorf("after 3/3 failures state = %s, want OPEN", got)
	}
	if b.Allow() {
		t.Error("OPEN breaker admitted a request before the reset timeout")
	}
	if b.RetryAfter() <= 0 {
		t.Error("RetryAfter() = 0 while OPEN, want a positive duration")
	}
}

func TestBreakerDegradationThresholdIsSixtyPercent(t *testing.T) {
	ResetAllBreakers()
	b := BreakerFor("test-ratio", BreakerOptions{FailureThreshold: 10, ResetTimeout: time.Second})
	if got := b.degradationThreshold(); got != 6 {
		t.Errorf("degradationThreshold() = %d, want 6 (60%% of 10)", got)
	}
	// A threshold of 1 must still degrade at 1 rather than at 0.
	small := BreakerFor("test-ratio-1", BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Second})
	if got := small.degradationThreshold(); got != 1 {
		t.Errorf("degradationThreshold(threshold=1) = %d, want 1", got)
	}
}

func TestBreakerHalfOpenClosesOnSuccess(t *testing.T) {
	ResetAllBreakers()
	base := time.Now()
	clock := base
	b := BreakerFor("test-halfopen", BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Second})
	b.mu.Lock()
	b.now = func() time.Time { return clock }
	b.mu.Unlock()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("state = %s, want OPEN", b.State())
	}
	// Advance the clock past the reset timeout.
	clock = base.Add(2 * time.Second)

	if !b.Allow() {
		t.Fatal("breaker did not admit a probe after the reset timeout")
	}
	if got := b.State(); got != StateHalfOpen {
		t.Errorf("state after probe admission = %s, want HALF_OPEN", got)
	}
	b.RecordSuccess()
	if got := b.State(); got != StateClosed {
		t.Errorf("state after probe success = %s, want CLOSED", got)
	}
}

func TestBreakerHalfOpenReopensOnFailureWithBackoff(t *testing.T) {
	ResetAllBreakers()
	base := time.Now()
	clock := base
	b := BreakerFor("test-backoff", BreakerOptions{
		FailureThreshold:       1,
		ResetTimeout:           time.Second,
		BackoffEscalationCount: 3,
		MaxBackoffMultiplier:   16,
	})
	b.mu.Lock()
	b.now = func() time.Time { return clock }
	b.mu.Unlock()
	b.RecordFailure()

	want := b.RetryAfter()
	clock = base.Add(2 * time.Second)
	b.Allow()
	b.RecordFailure() // probe failed: reopen

	if b.State() != StateOpen {
		t.Fatalf("state after failed probe = %s, want OPEN", b.State())
	}
	// One probe cycle is not enough to escalate: the timeout is unchanged.
	if got := b.RetryAfter(); got != want {
		t.Errorf("RetryAfter() = %v, want %v (one probe cycle must not escalate)", got, want)
	}

	// Three failed probe cycles double the reset timeout.
	for range 3 {
		clock = clock.Add(10 * time.Second)
		b.Allow()
		b.RecordFailure()
	}
	if got := b.RetryAfter(); got != 2*want {
		t.Errorf("RetryAfter() = %v, want %v (three probe cycles must double)", got, 2*want)
	}
}

func TestBreakerBackoffDoublesPerFailedProbeCycle(t *testing.T) {
	// With the defaults, EVERY failed OPEN→HALF_OPEN→OPEN probe cycle doubles
	// the reset timeout, capped at 8x (FASE 5 task doc: maxBackoffMultiplier
	// default 8). The explicit-options test above pins the escalation-count
	// mechanism; this one pins the shipped defaults.
	ResetAllBreakers()
	base := time.Now()
	clock := base
	b := BreakerFor("test-percycle", BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Second})
	b.mu.Lock()
	b.now = func() time.Time { return clock }
	b.mu.Unlock()

	b.RecordFailure() // OPEN with cycle count 0
	if got := b.RetryAfter(); got != time.Second {
		t.Fatalf("initial RetryAfter() = %v, want 1s", got)
	}

	// Clock advances past each timeout; after each failed probe the timeout
	// doubles: 1s, 2s, 4s, 8s, then the 8x cap holds.
	steps := []time.Duration{2, 4, 8, 16, 32}
	want := []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second,
		8 * time.Second, 8 * time.Second,
	}
	for i := range steps {
		clock = base.Add(steps[i] * time.Second)
		if !b.Allow() {
			t.Fatalf("probe %d was not admitted after its backed-off timeout", i+1)
		}
		b.RecordFailure()
		if got := b.RetryAfter(); got != want[i] {
			t.Errorf("after probe %d RetryAfter() = %v, want %v", i+1, got, want[i])
		}
	}

	if st := b.Status(); st.OpenCycleCount != len(steps) {
		t.Errorf("Status().OpenCycleCount = %d, want %d", st.OpenCycleCount, len(steps))
	}
	b.Reset()
	if st := b.Status(); st.OpenCycleCount != 0 {
		t.Errorf("Status().OpenCycleCount after Reset = %d, want 0", st.OpenCycleCount)
	}
}

func TestBreakerHalfOpenDoesNotStallOnUnreportedProbe(t *testing.T) {
	ResetAllBreakers()
	// One clock variable for the whole test: the installed closure reads it on
	// every call, so advancing it moves the breaker's notion of now. Installing
	// a fresh closure per step instead captures two different snapshots and the
	// stale one keeps answering.
	base := time.Now()
	clock := base
	b := BreakerFor("test-stall", BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Second})
	b.mu.Lock()
	b.now = func() time.Time { return clock }
	b.mu.Unlock()
	b.RecordFailure()

	clock = base.Add(2 * time.Second)
	if !b.Allow() {
		t.Fatal("breaker did not admit the first probe")
	}
	// The caller never reports an outcome. A second Allow after the timeout has
	// elapsed must reopen rather than admit nothing forever.
	clock = base.Add(4 * time.Second)
	if b.Allow() {
		t.Error("breaker admitted a second probe while the first was unreported")
	}
	if b.State() != StateOpen {
		t.Errorf("state = %s, want OPEN (unreported probe must reopen)", b.State())
	}
}

func TestBreakerResetCloses(t *testing.T) {
	ResetAllBreakers()
	b := BreakerFor("test-reset", BreakerOptions{FailureThreshold: 1})
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("state = %s, want OPEN", b.State())
	}
	b.Reset()
	if b.State() != StateClosed {
		t.Errorf("state after Reset = %s, want CLOSED", b.State())
	}
	if b.RetryAfter() != 0 {
		t.Errorf("RetryAfter() = %v, want 0 when CLOSED", b.RetryAfter())
	}
}

func TestBreakerForReturnsSameInstance(t *testing.T) {
	ResetAllBreakers()
	a := BreakerFor("test-same", BreakerOptions{FailureThreshold: 2})
	b := BreakerFor("test-same", BreakerOptions{FailureThreshold: 99})
	if a != b {
		t.Error("BreakerFor returned two breakers for the same provider")
	}
	// A later call must not change an existing breaker's options, so a
	// provider's threshold stays stable once it has state.
	if got := a.opts.FailureThreshold; got != 2 {
		t.Errorf("FailureThreshold = %d, want 2 (later options must not apply)", got)
	}
}

func TestBreakerForIsConcurrencySafe(t *testing.T) {
	ResetAllBreakers()
	const n = 50
	seen := make([]*Breaker, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			seen[idx] = BreakerFor("test-concurrent", BreakerOptions{FailureThreshold: 3})
		}(i)
	}
	wg.Wait()
	for i := range seen[1:] {
		if seen[i+1] != seen[0] {
			t.Fatalf("concurrent BreakerFor produced two instances at index %d", i+1)
		}
	}
}

func TestAllBreakerStatuses(t *testing.T) {
	ResetAllBreakers()
	BreakerFor("test-status-a", BreakerOptions{FailureThreshold: 1}).RecordFailure()
	BreakerFor("test-status-b", BreakerOptions{FailureThreshold: 1})

	// The registry is process-wide, so select this test's breakers by name
	// rather than assuming the snapshot holds only them.
	stats := AllBreakerStatuses()
	byName := make(map[string]BreakerStatus, len(stats))
	for _, s := range stats {
		byName[s.Name] = s
	}
	if _, ok := byName["test-status-a"]; !ok {
		t.Fatal("AllBreakerStatuses() is missing test-status-a")
	}
	if got := byName["test-status-a"].State; got != StateOpen {
		t.Errorf("test-status-a state = %s, want OPEN", got)
	}
	if got := byName["test-status-a"].RetryAfterMs; got <= 0 {
		t.Errorf("test-status-a RetryAfterMs = %d, want > 0", got)
	}
	if got := byName["test-status-b"].State; got != StateClosed {
		t.Errorf("test-status-b state = %s, want CLOSED", got)
	}
}
