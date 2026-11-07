// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webexec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
)

// errExpired is the failure every "session is dead" validator returns, so the
// tests can pin that HealthCheck wraps it with errors.Is-visible context.
var errExpired = errors.New("session expired")

// newTestPool builds a pool on an injected clock the test advances by
// reassigning *now.
func newTestPool(t *testing.T, now *time.Time, opts ...PoolOption) *Pool {
	t.Helper()
	return NewPool(append([]PoolOption{WithClock(func() time.Time { return *now })}, opts...)...)
}

func TestPoolAcquireRelease(t *testing.T) {
	now := base
	pool := newTestPool(t, &now)

	slot, err := pool.Acquire(ProviderDeepSeek, "a@x.com")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if slot.Provider != ProviderDeepSeek || slot.AccountID != "a@x.com" || !slot.AcquiredAt.Equal(base) {
		t.Fatalf("slot = %+v, want the deepseek account acquired at base", slot)
	}
	// A second acquire of the same account reuses the one slot.
	if _, err := pool.Acquire(ProviderDeepSeek, "a@x.com"); err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	snap := pool.Snapshot(ProviderDeepSeek)
	if snap.Sessions != 1 || snap.Active != 2 {
		t.Fatalf("Snapshot = %d sessions / %d active, want 1 / 2", snap.Sessions, snap.Active)
	}

	pool.Release(slot)
	pool.Release(slot) // double release is ignored
	snap = pool.Snapshot(ProviderDeepSeek)
	if snap.Active != 0 {
		t.Fatalf("Active after releases = %d, want 0", snap.Active)
	}
	// Releasing a nil or foreign slot must not panic or corrupt state.
	pool.Release(nil)
	pool.Release(&SessionSlot{Provider: "ghost", AccountID: "ghost"})
}

func TestPoolAcquireSeparatesProviders(t *testing.T) {
	now := base
	pool := newTestPool(t, &now)
	for _, provider := range []string{ProviderDeepSeek, ProviderGemini} {
		if _, err := pool.Acquire(provider, "a@x.com"); err != nil {
			t.Fatalf("Acquire %s: %v", provider, err)
		}
	}
	if got := pool.Snapshot(ProviderDeepSeek).Sessions; got != 1 {
		t.Fatalf("deepseek sessions = %d, want 1 (providers must not share slots)", got)
	}
	if got := pool.Snapshot(ProviderGemini).Sessions; got != 1 {
		t.Fatalf("gemini sessions = %d, want 1", got)
	}
}

func TestPoolAcquireRateLimited(t *testing.T) {
	now := base
	pool := newTestPool(t, &now, WithMaxReqPerMin(2))

	for i := 0; i < 2; i++ {
		if _, err := pool.Acquire(ProviderDeepSeek, "a@x.com"); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	_, err := pool.Acquire(ProviderDeepSeek, "a@x.com")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("third acquire error = %v, want ErrRateLimited", err)
	}
	// A different account keeps its own budget.
	if _, err := pool.Acquire(ProviderDeepSeek, "b@x.com"); err != nil {
		t.Fatalf("other account acquire: %v", err)
	}
	// Reset clears the window.
	pool.ResetRate("a@x.com")
	if _, err := pool.Acquire(ProviderDeepSeek, "a@x.com"); err != nil {
		t.Fatalf("acquire after ResetRate: %v", err)
	}
}

func TestPoolFull(t *testing.T) {
	now := base
	pool := newTestPool(t, &now, WithMaxSessions(2))

	for _, account := range []string{"a@x.com", "b@x.com"} {
		if _, err := pool.Acquire(ProviderDeepSeek, account); err != nil {
			t.Fatalf("Acquire %s: %v", account, err)
		}
	}
	if _, err := pool.Acquire(ProviderDeepSeek, "c@x.com"); !errors.Is(err, ErrPoolFull) {
		t.Fatalf("third account error = %v, want ErrPoolFull", err)
	}
}

func TestPoolIdleSlotReclaimed(t *testing.T) {
	now := base
	pool := newTestPool(t, &now, WithMaxSessions(1))

	slot, err := pool.Acquire(ProviderDeepSeek, "a@x.com")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	pool.Release(slot)
	now = base.Add(DefaultIdleTTL + time.Minute)
	pool.SweepIdle()
	if got := pool.Snapshot(ProviderDeepSeek).Sessions; got != 0 {
		t.Fatalf("sessions after idle TTL = %d, want 0 (slot reclaimed)", got)
	}
	// The reclaimed capacity is usable again.
	if _, err := pool.Acquire(ProviderDeepSeek, "b@x.com"); err != nil {
		t.Fatalf("Acquire after reclaim: %v", err)
	}
}

func TestPoolInUseSlotNotReclaimed(t *testing.T) {
	now := base
	pool := newTestPool(t, &now, WithMaxSessions(1))

	if _, err := pool.Acquire(ProviderDeepSeek, "a@x.com"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	now = base.Add(10 * DefaultIdleTTL)
	pool.SweepIdle()
	snap := pool.Snapshot(ProviderDeepSeek)
	if snap.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1: a slot still in use is never reclaimed", snap.Sessions)
	}
	if snap.SessionValid {
		t.Fatalf("SessionValid must be false before any health check passed")
	}
}

func TestPoolHealthCheckRecordsVerdict(t *testing.T) {
	now := base
	pool := newTestPool(t, &now)
	valid := func(context.Context, string, string) error { return nil }
	fail := func(context.Context, string, string) error { return errExpired }

	if err := pool.HealthCheck(context.Background(), ProviderDeepSeek, "a@x.com", valid); err != nil {
		t.Fatalf("HealthCheck success: %v", err)
	}
	snap := pool.Snapshot(ProviderDeepSeek)
	if !snap.SessionValid {
		t.Fatalf("SessionValid must be true after a passing check")
	}
	if snap.CooldownActive {
		t.Fatalf("a passing check must not cool the provider down")
	}

	err := pool.HealthCheck(context.Background(), ProviderDeepSeek, "a@x.com", fail)
	if !errors.Is(err, errExpired) {
		t.Fatalf("HealthCheck failure must wrap the validate error, got %v", err)
	}
	snap = pool.Snapshot(ProviderDeepSeek)
	if snap.SessionValid {
		t.Fatalf("SessionValid must be false after a failing check")
	}
	if !snap.CooldownActive {
		t.Fatalf("a failed check must cool the provider down")
	}
	if _, err := pool.Acquire(ProviderDeepSeek, "a@x.com"); !errors.Is(err, ErrCooldown) {
		t.Fatalf("acquire during cooldown error = %v, want ErrCooldown", err)
	}
	// A different provider is not cooled down by deepseek's failure.
	if _, err := pool.Acquire(ProviderGemini, "a@x.com"); err != nil {
		t.Fatalf("other provider acquire during cooldown: %v", err)
	}
}

func TestPoolSuccessfulCheckClearsCooldown(t *testing.T) {
	now := base
	pool := newTestPool(t, &now)
	fail := func(context.Context, string, string) error { return errors.New("down") }
	valid := func(context.Context, string, string) error { return nil }
	ctx := context.Background()

	if err := pool.HealthCheck(ctx, ProviderGemini, "a@x.com", fail); err == nil {
		t.Fatalf("expected the failing check to error")
	}
	// The next check succeeds: the provider reopens immediately.
	if err := pool.HealthCheck(ctx, ProviderGemini, "a@x.com", valid); err != nil {
		t.Fatalf("HealthCheck recovery: %v", err)
	}
	if pool.CooldownActive(ProviderGemini) {
		t.Fatalf("a successful check must clear the provider cooldown")
	}
	if _, err := pool.Acquire(ProviderGemini, "a@x.com"); err != nil {
		t.Fatalf("acquire after recovery: %v", err)
	}
}

func TestPoolCooldownExpiresByTime(t *testing.T) {
	now := base
	pool := newTestPool(t, &now)
	fail := func(context.Context, string, string) error { return errors.New("down") }

	if err := pool.HealthCheck(context.Background(), ProviderMuse, "a@x.com", fail); err == nil {
		t.Fatalf("expected the failing check to error")
	}
	now = base.Add(DefaultCooldown + time.Second)
	if pool.CooldownActive(ProviderMuse) {
		t.Fatalf("cooldown must expire after %s", DefaultCooldown)
	}
}

func TestPoolNilValidateRefused(t *testing.T) {
	now := base
	pool := newTestPool(t, &now)
	if err := pool.HealthCheck(context.Background(), ProviderDeepSeek, "a@x.com", nil); err == nil {
		t.Fatalf("HealthCheck without a validate function must error")
	}
}

func TestPoolSnapshotRateLimitWorstCase(t *testing.T) {
	now := base
	pool := newTestPool(t, &now, WithMaxReqPerMin(5))

	// Exhaust account a, leave account b fresh.
	for i := 0; i < 5; i++ {
		if _, err := pool.Acquire(ProviderMuse, "a@x.com"); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if _, err := pool.Acquire(ProviderMuse, "b@x.com"); err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	snap := pool.Snapshot(ProviderMuse)
	if snap.Sessions != 2 {
		t.Fatalf("sessions = %d, want 2", snap.Sessions)
	}
	if snap.RateLimitRemaining != 0 || snap.RateLimitMax != 5 {
		t.Fatalf("rate = %d/%d, want 0/5 (worst case across accounts)",
			snap.RateLimitRemaining, snap.RateLimitMax)
	}
	if got := pool.Snapshot(ProviderChatGPT).RateLimitRemaining; got != 5 {
		t.Fatalf("untouched provider remaining = %d, want 5", got)
	}
}

func TestPoolConcurrentAcquireRelease(t *testing.T) {
	now := base
	pool := newTestPool(t, &now, WithMaxReqPerMin(1000))

	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				slot, err := pool.Acquire(ProviderDeepSeek, "a@x.com")
				if err != nil {
					t.Errorf("Acquire: %v", err)
					return
				}
				pool.Release(slot)
			}
		}()
	}
	wg.Wait()

	if got := pool.Snapshot(ProviderDeepSeek).Active; got != 0 {
		t.Fatalf("Active after concurrent acquire/release = %d, want 0", got)
	}
}

func TestBuildStatus(t *testing.T) {
	now := base
	pool := newTestPool(t, &now)
	settings := &db.SettingsData{WebExecDeepSeekEnabled: true}

	t.Run("nil pool reports the settings switch with defaults", func(t *testing.T) {
		st := BuildStatus(ProviderDeepSeek, settings, nil)
		if !st.Enabled {
			t.Fatalf("Enabled = false, want true from the settings row")
		}
		if st.SessionValid {
			t.Fatalf("SessionValid must be false without a pool")
		}
		if st.RateLimitRemaining != DefaultMaxReqPerMin {
			t.Fatalf("RateLimitRemaining = %d, want %d", st.RateLimitRemaining, DefaultMaxReqPerMin)
		}
		if st.LastUsed != "" {
			t.Fatalf("LastUsed = %q, want empty", st.LastUsed)
		}
	})

	t.Run("pool snapshot fills the health fields", func(t *testing.T) {
		slot, err := pool.Acquire(ProviderDeepSeek, "a@x.com")
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		pool.Release(slot)
		st := BuildStatus(ProviderDeepSeek, settings, pool)
		if st.LastUsed == "" {
			t.Fatalf("LastUsed must carry the release time")
		}
		if ts, err := time.Parse(time.RFC3339, st.LastUsed); err != nil || !ts.Equal(base) {
			t.Fatalf("LastUsed = %q, want the RFC3339 release time %s", st.LastUsed, base.Format(time.RFC3339))
		}
		// The provider is off in settings, so its status reads disabled.
		off := BuildStatus(ProviderMuse, settings, pool)
		if off.Enabled {
			t.Fatalf("muse Enabled = true, want false")
		}
	})
}
