// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package resilience

import (
	"sync"
	"testing"
	"time"
)

// lockClock installs an injectable clock on the lockout registry and restores
// the real one when the test finishes, following the Breaker test pattern.
func lockClock(t *testing.T, clock *time.Time) {
	t.Helper()
	original := modelLocks.now
	modelLocks.now = func() time.Time { return *clock }
	t.Cleanup(func() { modelLocks.now = original })
}

func TestModelLockKey(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		connectionID string
		model        string
		want         string
	}{
		{
			name:         "joins the three parts",
			provider:     "openai",
			connectionID: "conn-1",
			model:        "gpt-4o",
			want:         "openai:conn-1:gpt-4o",
		},
		{
			name:         "empty parts keep their positions",
			provider:     "openai",
			connectionID: "",
			model:        "gpt-4o",
			want:         "openai::gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModelLockKey(tt.provider, tt.connectionID, tt.model); got != tt.want {
				t.Errorf("ModelLockKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModelLockout(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	lockClock(t, &clock)
	reset := func() {
		ResetModelLockouts()
		clock = base
	}

	tests := []struct {
		name       string
		status     int
		retryAfter time.Duration
		// wantUntil is how long after the failure the tuple must stay locked.
		wantUntil time.Duration
		wantLock  bool
	}{
		{
			name:       "429 with a Retry-After locks for that duration",
			status:     429,
			retryAfter: 2 * time.Minute,
			wantUntil:  2 * time.Minute,
			wantLock:   true,
		},
		{
			name:       "a Retry-After beyond 1h is capped",
			status:     429,
			retryAfter: 6 * time.Hour,
			wantUntil:  time.Hour,
			wantLock:   true,
		},
		{
			name:       "429 without a Retry-After takes the base cooldown",
			status:     429,
			retryAfter: 0,
			wantUntil:  modelLockBase,
			wantLock:   true,
		},
		{
			name:       "a negative Retry-After counts as absent",
			status:     429,
			retryAfter: -time.Minute,
			wantUntil:  modelLockBase,
			wantLock:   true,
		},
		{
			name:      "a 500 locks the base cooldown",
			status:    500,
			wantUntil: modelLockBase,
			wantLock:  true,
		},
		{
			name:      "a 503 locks the base cooldown",
			status:    503,
			wantUntil: modelLockBase,
			wantLock:  true,
		},
		{
			name:     "a 400 locks nothing",
			status:   400,
			wantLock: false,
		},
		{
			name:     "a 401 locks nothing",
			status:   401,
			wantLock: false,
		},
		{
			name:     "a 408 locks nothing here (the provider breaker owns it)",
			status:   408,
			wantLock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset()
			RecordModelFailure("openai", "conn-1", "gpt-4o", tt.status, tt.retryAfter)

			locked, until := ModelLocked("openai", "conn-1", "gpt-4o")
			if locked != tt.wantLock {
				t.Fatalf("ModelLocked() locked = %v, want %v", locked, tt.wantLock)
			}
			if !tt.wantLock {
				return
			}
			if want := base.Add(tt.wantUntil); !until.Equal(want) {
				t.Errorf("ModelLocked() until = %v, want %v", until, want)
			}
			// The lock must still hold just before it expires.
			clock = base.Add(tt.wantUntil - time.Nanosecond)
			if locked, _ := ModelLocked("openai", "conn-1", "gpt-4o"); !locked {
				t.Error("lock expired before its deadline")
			}
			// And release at the exact expiry instant.
			clock = base.Add(tt.wantUntil)
			if locked, _ := ModelLocked("openai", "conn-1", "gpt-4o"); locked {
				t.Error("lock still held at its expiry instant")
			}
		})
	}
}

func TestModelLockout5xxEscalates(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	lockClock(t, &clock)
	ResetModelLockouts()

	// Consecutive 502s double the cooldown: 60s, 120s, 240s, 480s, 600s (cap).
	// Every failure is recorded at the same pinned instant, so the expected
	// lock deadline is simply base + that failure's cooldown — the deadline
	// only ever extends, never shortens.
	want := []time.Duration{
		60 * time.Second, 120 * time.Second, 240 * time.Second,
		480 * time.Second, 600 * time.Second, 600 * time.Second,
	}
	for i, cooldown := range want {
		RecordModelFailure("openai", "conn-1", "gpt-4o", 502, 0)
		_, until := ModelLocked("openai", "conn-1", "gpt-4o")
		if expected := base.Add(cooldown); !until.Equal(expected) {
			t.Errorf("failure %d locks until %v, want %v", i+1, until, expected)
		}
	}
}

func TestModelLockoutHeaderless429EscalatesToHourCap(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	lockClock(t, &clock)
	ResetModelLockouts()

	// Headerless 429s escalate like 5xx but cap at 1h, not 10min.
	want := []time.Duration{
		60 * time.Second, 120 * time.Second, 240 * time.Second,
		480 * time.Second, 960 * time.Second, 1920 * time.Second,
		3600 * time.Second, 3600 * time.Second,
	}
	for i, cooldown := range want {
		RecordModelFailure("openai", "conn-1", "gpt-4o", 429, 0)
		_, until := ModelLocked("openai", "conn-1", "gpt-4o")
		if expected := base.Add(cooldown); !until.Equal(expected) {
			t.Errorf("failure %d locks until %v, want %v", i+1, until, expected)
		}
	}
}

func TestModelLockoutSuccessClears(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	lockClock(t, &clock)
	ResetModelLockouts()

	RecordModelFailure("openai", "conn-1", "gpt-4o", 502, 0)
	if locked, _ := ModelLocked("openai", "conn-1", "gpt-4o"); !locked {
		t.Fatal("tuple not locked after a 502")
	}
	RecordModelSuccess("openai", "conn-1", "gpt-4o")
	if locked, until := ModelLocked("openai", "conn-1", "gpt-4o"); locked {
		t.Errorf("tuple still locked after a success, until %v", until)
	}

	// The failure streak restarts from the base cooldown, not from where the
	// cleared streak left off.
	RecordModelFailure("openai", "conn-1", "gpt-4o", 502, 0)
	_, until := ModelLocked("openai", "conn-1", "gpt-4o")
	if expected := base.Add(modelLockBase); !until.Equal(expected) {
		t.Errorf("streak restarted at %v, want the base cooldown %v", until, expected)
	}
}

func TestModelLockoutIsolatesTuples(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	lockClock(t, &clock)
	ResetModelLockouts()

	RecordModelFailure("openai", "conn-1", "gpt-4o", 502, 0)

	// The same model on another connection, another model on the same
	// connection, and another provider must all stay unlocked.
	cases := []struct {
		provider     string
		connectionID string
		model        string
	}{
		{"openai", "conn-2", "gpt-4o"},
		{"openai", "conn-1", "gpt-4o-mini"},
		{"anthropic", "conn-1", "gpt-4o"},
	}
	for _, tc := range cases {
		if locked, _ := ModelLocked(tc.provider, tc.connectionID, tc.model); locked {
			t.Errorf("ModelLocked(%s, %s, %s) = true, want isolated from the locked tuple",
				tc.provider, tc.connectionID, tc.model)
		}
	}
}

func TestModelLockoutExtendsWhileLocked(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	lockClock(t, &clock)
	ResetModelLockouts()

	RecordModelFailure("openai", "conn-1", "gpt-4o", 429, 10*time.Minute)
	// A second 429 with a shorter header must not shorten the lock.
	clock = base.Add(time.Minute)
	RecordModelFailure("openai", "conn-1", "gpt-4o", 429, 2*time.Minute)
	_, until := ModelLocked("openai", "conn-1", "gpt-4o")
	if expected := base.Add(10 * time.Minute); !until.Equal(expected) {
		t.Errorf("lock shortened to %v, want the longer %v", until, expected)
	}
}

func TestModelLockoutConcurrencySafe(t *testing.T) {
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	clock := base
	lockClock(t, &clock)
	ResetModelLockouts()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			RecordModelFailure("openai", "conn-1", "gpt-4o", 502, 0)
			ModelLocked("openai", "conn-1", "gpt-4o")
			if n%2 == 0 {
				RecordModelSuccess("openai", "conn-1", "gpt-4o")
			}
		}(i)
	}
	wg.Wait()
}
