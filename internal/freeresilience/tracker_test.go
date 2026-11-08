// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory RecoveryStore for tracker tests.
type fakeStore struct {
	mu       sync.Mutex
	events   []FreeTierCooldown
	cleared  []string
	expired  []string
	health   map[string][]Outcome
	failNext error
}

func (f *fakeStore) RecordFreeTierLimitEvent(provider, model, kind, regime string, resetAt *time.Time, lastError string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cd := FreeTierCooldown{Provider: provider, Model: model, Kind: kind, Regime: regime, EventAt: now}
	if resetAt != nil {
		cd.ResetAt = *resetAt
	} else {
		cd.OpenEnded = true
	}
	// Bump semantics like the repo: one row per provider.
	for i := range f.events {
		if f.events[i].Provider == provider {
			f.events[i] = cd
			return f.failNext
		}
	}
	f.events = append(f.events, cd)
	return f.failNext
}

func (f *fakeStore) ActiveFreeTierCooldowns(now time.Time) ([]FreeTierCooldown, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FreeTierCooldown
	for _, cd := range f.events {
		if cd.OpenEnded || cd.ResetAt.After(now) {
			out = append(out, cd)
		}
	}
	return out, nil
}

func (f *fakeStore) ClearFreeTierCooldowns(provider string, now time.Time, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, provider)
	var kept []FreeTierCooldown
	for _, cd := range f.events {
		if cd.Provider != provider {
			kept = append(kept, cd)
		}
	}
	f.events = kept
	return nil
}

func (f *fakeStore) ExpireFreeTierCooldowns(now time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rejoined []string
	var kept []FreeTierCooldown
	for _, cd := range f.events {
		if !cd.OpenEnded && !cd.ResetAt.IsZero() && !cd.ResetAt.After(now) {
			rejoined = append(rejoined, cd.Provider)
			continue
		}
		kept = append(kept, cd)
	}
	f.events = kept
	f.expired = rejoined
	return rejoined, nil
}

func (f *fakeStore) SaveFreeProviderHealth(provider string, outcomes []Outcome, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.health == nil {
		f.health = map[string][]Outcome{}
	}
	f.health[provider] = outcomes
	return nil
}

func (f *fakeStore) LoadFreeProviderHealth() (map[string][]Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string][]Outcome{}
	for p, rows := range f.health {
		out[p] = rows
	}
	return out, nil
}

func newTestTracker(store *fakeStore, start time.Time) (*Tracker, func(time.Duration)) {
	current := start
	clock := func() time.Time { return current }
	advance := func(d time.Duration) { current = current.Add(d) }
	t := NewTracker(DefaultFallbackConfig(), store, func(provider, model string) string {
		switch provider {
		case "gemini":
			return RegimeRecurringDaily
		case "keylessgate":
			return RegimeKeyless
		case "credittier":
			return RegimeRecurringCredit
		default:
			return RegimeKeyless
		}
	})
	t.SetClock(clock)
	return t, advance
}

func TestTracker_CoolingOpensAndExpires(t *testing.T) {
	start := time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	tracker, advance := newTestTracker(store, start)

	// A 429 on a daily-regime provider parks it until midnight.
	tracker.RecordOutcome("gemini", "gemini-2.5-flash", 429, []byte(`rate limit`), 150, nil)
	if cooling, until := tracker.IsCooling("gemini"); !cooling {
		t.Fatal("gemini must be cooling after a 429")
	} else if !until.Equal(time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("cooling until %v, want midnight", until)
	}

	// Before the reset: still cooling.
	advance(1 * time.Hour)
	tracker.ExpireCooldowns()
	if cooling, _ := tracker.IsCooling("gemini"); !cooling {
		t.Fatal("gemini must still be cooling 1h in")
	}

	// Past the reset (injectable clock, no sleeping): the provider rejoins.
	advance(2 * time.Hour)
	if rejoined := tracker.ExpireCooldowns(); len(rejoined) != 1 || rejoined[0] != "gemini" {
		t.Errorf("rejoined = %v, want [gemini]", rejoined)
	}
	if cooling, _ := tracker.IsCooling("gemini"); cooling {
		t.Fatal("gemini must have rejoined rotation after its reset passed")
	}
}

func TestTracker_EarlyRecoveryOnSuccess(t *testing.T) {
	start := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	tracker, advance := newTestTracker(store, start)

	tracker.RecordOutcome("keylessgate", "m-free", 429, []byte(`too many requests`), 100, nil)
	if cooling, _ := tracker.IsCooling("keylessgate"); !cooling {
		t.Fatal("keylessgate must be cooling after a 429")
	}

	// The provider answers again well before the 5-minute estimate.
	advance(30 * time.Second)
	tracker.RecordOutcome("keylessgate", "m-free", 200, nil, 90, nil)
	if cooling, _ := tracker.IsCooling("keylessgate"); cooling {
		t.Fatal("a success must lift the cooldown early")
	}
	if len(store.cleared) != 1 || store.cleared[0] != "keylessgate" {
		t.Errorf("store cleared = %v, want [keylessgate]", store.cleared)
	}
}

func TestTracker_CreditCooldownHasNoClock(t *testing.T) {
	start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	tracker, advance := newTestTracker(store, start)

	tracker.RecordOutcome("credittier", "big-model", 403, []byte(`FreeTierError: credits exhausted`), 80, nil)
	cooling, until := tracker.IsCooling("credittier")
	if !cooling {
		t.Fatal("credittier must be cooling")
	}
	if !until.IsZero() {
		t.Errorf("credit cooldown must be open-ended, got %v", until)
	}
	// Days later it is STILL cooling — no auto-recovery for a spent credit.
	advance(72 * time.Hour)
	tracker.ExpireCooldowns()
	if cooling, _ := tracker.IsCooling("credittier"); !cooling {
		t.Error("credit cooldown must not expire on any clock")
	}
	// Only a real success lifts it.
	tracker.RecordOutcome("credittier", "big-model", 200, nil, 100, nil)
	if cooling, _ := tracker.IsCooling("credittier"); cooling {
		t.Error("a success must lift even an open-ended cooldown")
	}
}

func TestTracker_HealthMath(t *testing.T) {
	start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tracker, _ := newTestTracker(&fakeStore{}, start)

	// Ten clean successes: healthy.
	for i := 0; i < 10; i++ {
		tracker.RecordOutcome("healthy", "m", 200, nil, int64(100+i), nil)
	}

	// 8 successes plus a 429 and a 403: 80% success rate, and the 429/403
	// opened a (keyless, 5-minute) cooldown — the row reports cooling with
	// both recent counts.
	for i := 0; i < 8; i++ {
		tracker.RecordOutcome("mixed", "m", 200, nil, 100, nil)
	}
	tracker.RecordOutcome("mixed", "m", 429, []byte(`rate limit`), 200, nil)
	tracker.RecordOutcome("mixed", "m", 403, []byte(`forbidden`), 200, nil)

	// A provider whose success rate sinks below 70% with enough samples is
	// flaky. 500s are not free-tier signals, so no cooldown opens.
	for i := 0; i < 6; i++ {
		tracker.RecordOutcome("flaky", "m", 500, []byte(`boom`), 50, nil)
	}
	tracker.RecordOutcome("flaky", "m", 200, nil, 50, nil)

	snap := tracker.HealthSnapshot(nil, nil)
	byName := map[string]ProviderHealth{}
	for _, row := range snap {
		byName[row.Provider] = row
	}
	if h := byName["healthy"]; h.Status != "healthy" || h.Samples != 10 || h.SuccessRatePct != 100 {
		t.Errorf("healthy = %+v, want healthy 10 samples 100%%", h)
	}
	if h := byName["mixed"]; h.Samples != 10 || h.SuccessRatePct != 80 {
		t.Errorf("mixed = %+v, want 10 samples at 80%%", h)
	}
	if h := byName["mixed"]; h.Status != "cooling" || h.CoolingUntil == "" {
		t.Errorf("mixed status = %s, want cooling with a timestamp", h.Status)
	}
	if h := byName["mixed"]; h.RecentRate429 != 1 || h.Recent403 != 1 {
		t.Errorf("mixed 429=%d 403=%d, want 1/1", h.RecentRate429, h.Recent403)
	}
	if h := byName["flaky"]; h.Status != "flaky" {
		t.Errorf("flaky status = %s, want flaky", h.Status)
	}
}

func TestTracker_HealthScoreFeedsChainOrder(t *testing.T) {
	start := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tracker, _ := newTestTracker(&fakeStore{}, start)

	// good: all successes. bad: mostly failures. cooling: parked.
	for i := 0; i < 5; i++ {
		tracker.RecordOutcome("good", "m", 200, nil, 50, nil)
		tracker.RecordOutcome("bad", "m", 500, []byte(`boom`), 50, nil)
	}
	tracker.RecordOutcome("cooling", "m", 429, []byte(`rate limit`), 0, nil)

	scores := map[string]float64{}
	for _, p := range []string{"good", "bad", "cooling", "unknown"} {
		scores[p] = tracker.HealthScore(p)
	}
	if scores["good"] <= scores["bad"] {
		t.Errorf("good (%v) must outrank bad (%v)", scores["good"], scores["bad"])
	}
	if scores["cooling"] != -1 {
		t.Errorf("cooling score = %v, want -1", scores["cooling"])
	}
	if scores["unknown"] != 0.5 {
		t.Errorf("unknown score = %v, want 0.5 (neutral)", scores["unknown"])
	}

	ordered := OrderCandidates(nil, []string{"cooling", "bad", "good", "unknown"}, tracker.HealthScore)
	if ordered[0] != "good" || ordered[len(ordered)-1] != "cooling" {
		t.Errorf("chain order = %v, want good first, cooling last", ordered)
	}
}

func TestTracker_PersistsAndReloads(t *testing.T) {
	start := time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC)
	store := &fakeStore{}
	tracker, _ := newTestTracker(store, start)

	tracker.RecordOutcome("gemini", "m", 200, nil, 120, nil)
	tracker.RecordOutcome("keylessgate", "m", 429, []byte(`rate limit`), 0, nil)
	tracker.FlushHealth()
	if len(store.health["gemini"]) != 1 {
		t.Fatalf("flush must persist the outcome ring, got %d rows", len(store.health["gemini"]))
	}

	// A NEW tracker over the same store (a restart) reloads both the health
	// ring and the active cooldown.
	reborn := NewTracker(DefaultFallbackConfig(), store, tracker.regimes)
	reborn.SetClock(func() time.Time { return start })
	if cooling, _ := reborn.IsCooling("keylessgate"); !cooling {
		t.Error("restarted tracker must still see the active cooldown")
	}
	snap := reborn.HealthSnapshot(nil, nil)
	byName := map[string]ProviderHealth{}
	for _, row := range snap {
		byName[row.Provider] = row
	}
	if byName["gemini"].Samples != 1 {
		t.Errorf("restarted health ring = %d samples, want 1", byName["gemini"].Samples)
	}
}

func TestTracker_NilSafe(t *testing.T) {
	var tracker *Tracker
	if tracker.FallbackEnabled() {
		t.Error("nil tracker must not enable the chain")
	}
	if cooling, _ := tracker.IsCooling("x"); cooling {
		t.Error("nil tracker must not report cooling")
	}
	tracker.RecordOutcome("x", "m", 429, nil, 0, nil) // must not panic
	if got := tracker.HealthScore("x"); got != 0 {
		t.Errorf("nil tracker score = %v, want 0", got)
	}
	tracker.ExpireCooldowns()
	tracker.FlushHealth()
	tracker.ClearAllCooldowns("test")
}

func TestResetHeaderDuration_NilHeader(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if _, ok := ResetHeaderDuration(nil, now); ok {
		t.Error("nil header must report no duration")
	}
	if _, ok := ResetHeaderDuration(http.Header{}, now); ok {
		t.Error("empty header must report no duration")
	}
}
