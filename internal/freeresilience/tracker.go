// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import (
	"log"
	"net/http"
	"sort"
	"sync"
	"time"
)

// HealthWindow is the window the health score looks back over.
const HealthWindow = 24 * time.Hour

// maxOutcomesPerProvider bounds the per-provider outcome ring. At one entry
// per request this is far more than a free tier turns over in 24h, and it
// keeps the persisted JSON blob small.
const maxOutcomesPerProvider = 400

// Outcome is one recorded request result for a provider.
type Outcome struct {
	At        time.Time `json:"at"`
	Success   bool      `json:"ok"`
	Status    int       `json:"status,omitempty"`
	LatencyMs int64     `json:"ms"`
}

// ProviderHealth is the health snapshot the dashboard endpoint and the
// fallback chain's default ordering both read.
type ProviderHealth struct {
	Provider       string  `json:"provider"`
	Status         string  `json:"status"` // healthy | flaky | cooling
	CoolingUntil   string  `json:"coolingUntil,omitempty"`
	CoolingOpen    bool    `json:"coolingOpenEnded,omitempty"` // credit/one-time: no clock, success only
	SuccessRate    float64 `json:"successRate"`                // -1 = no data in window
	Samples        int     `json:"samples"`                    // outcomes in window
	RecentRate429  int     `json:"recent429"`                  // in window
	Recent403      int     `json:"recent403"`                  // in window
	P95LatencyMs   float64 `json:"p95LatencyMs"`
	Regime         string  `json:"regime,omitempty"`
	SuccessRatePct float64 `json:"successRatePct"` // convenience for the UI
}

// RecoveryStore is the persistence the tracker flushes through. The SQLite
// repo implements it; tests use in-memory fakes.
type RecoveryStore interface {
	RecordFreeTierLimitEvent(provider, model, kind, regime string, resetAt *time.Time, lastError string, now time.Time) error
	ActiveFreeTierCooldowns(now time.Time) ([]FreeTierCooldown, error)
	ClearFreeTierCooldowns(provider string, now time.Time, reason string) error
	ExpireFreeTierCooldowns(now time.Time) ([]string, error)
	SaveFreeProviderHealth(provider string, outcomes []Outcome, now time.Time) error
	LoadFreeProviderHealth() (map[string][]Outcome, error)
}

// FreeTierCooldown is one active provider-level wait.
type FreeTierCooldown struct {
	Provider  string
	Model     string
	Kind      string
	Regime    string
	EventAt   time.Time
	ResetAt   time.Time // zero = open-ended (credit/one-time)
	OpenEnded bool
	LastError string
}

// RegimeFunc resolves the documented free regime for a provider/model pair.
// Wired to providers.FreeRegimeFor at construction, so this package does not
// depend on the providers package.
type RegimeFunc func(provider, model string) string

// Tracker holds the free-tier resilience state: per-provider health outcomes
// and the active cooldown map the fallback chain consults. All methods are
// safe for concurrent use; the request path calls RecordOutcome on every
// free-provider attempt.
type Tracker struct {
	mu      sync.Mutex
	cfg     FallbackConfig
	store   RecoveryStore
	regimes RegimeFunc
	now     func() time.Time

	health   map[string][]Outcome
	cooling  map[string]time.Time // provider -> reset instant
	openEnd  map[string]bool      // provider -> cooling with no clock (credit)
	attempts map[string]int       // provider -> consecutive limit events
}

// NewTracker builds a tracker. store may be nil (state stays in memory); the
// regime callback may be nil (resets fall back to the short-cooldown family).
func NewTracker(cfg FallbackConfig, store RecoveryStore, regimes RegimeFunc) *Tracker {
	t := &Tracker{
		cfg:      cfg,
		store:    store,
		regimes:  regimes,
		now:      time.Now,
		health:   map[string][]Outcome{},
		cooling:  map[string]time.Time{},
		openEnd:  map[string]bool{},
		attempts: map[string]int{},
	}
	if store != nil {
		t.loadFromStore()
	}
	return t
}

// SetClock replaces the clock. Test-only.
func (t *Tracker) SetClock(now func() time.Time) {
	if now != nil {
		t.mu.Lock()
		t.now = now
		t.mu.Unlock()
	}
}

// loadFromStore seeds the in-memory state from what the previous run
// persisted: health outcomes and any cooldown that has not expired yet.
func (t *Tracker) loadFromStore() {
	now := t.now().UTC()
	if outcomes, err := t.store.LoadFreeProviderHealth(); err == nil && outcomes != nil {
		for provider, rows := range outcomes {
			t.health[provider] = pruneOutcomes(rows, now)
		}
	} else if err != nil {
		log.Printf("[freeresilience] load health failed: %v", err)
	}
	if active, err := t.store.ActiveFreeTierCooldowns(now); err == nil {
		for _, cd := range active {
			if cd.OpenEnded || cd.ResetAt.IsZero() {
				t.openEnd[cd.Provider] = true
			} else if cd.ResetAt.After(now) {
				t.cooling[cd.Provider] = cd.ResetAt
			}
		}
	} else {
		log.Printf("[freeresilience] load cooldowns failed: %v", err)
	}
}

// FallbackEnabled reports whether the fallback chain is on.
func (t *Tracker) FallbackEnabled() bool {
	if t == nil {
		return false
	}
	return t.cfg.Enabled
}

// FallbackConfig returns the tracker's chain configuration (for logging).
func (t *Tracker) FallbackConfig() FallbackConfig {
	return t.cfg
}

// RecordOutcome records one request result. A success counts toward health
// and lifts any active cooldown early (smart recovery). A failure counts
// toward health and, when the classifier calls it a free-tier limit, opens or
// extends a cooldown.
func (t *Tracker) RecordOutcome(provider, model string, status int, body []byte, latencyMs int64, header http.Header) {
	if t == nil || provider == "" {
		return
	}
	now := t.now().UTC()
	success := status >= 200 && status < 300 && status != 0

	t.mu.Lock()
	ring := append(pruneOutcomes(t.health[provider], now), Outcome{At: now, Success: success, Status: status, LatencyMs: latencyMs})
	if len(ring) > maxOutcomesPerProvider {
		ring = ring[len(ring)-maxOutcomesPerProvider:]
	}
	t.health[provider] = ring

	if success {
		t.attempts[provider] = 0
		cooling := len(t.cooling) > 0 && t.cooling[provider].After(now)
		open := t.openEnd[provider]
		if cooling || open {
			delete(t.cooling, provider)
			delete(t.openEnd, provider)
			t.mu.Unlock()
			// Smart recovery: the provider answered again before its
			// estimated reset, so the penalty lifts now, not at midnight.
			if t.store != nil {
				if err := t.store.ClearFreeTierCooldowns(provider, now, "success"); err != nil {
					log.Printf("[freeresilience] clear cooldown %s failed: %v", provider, err)
				}
			}
			log.Printf("[freeresilience] %s recovered early (success before reset)", provider)
		} else {
			t.mu.Unlock()
		}
		return
	}
	t.mu.Unlock()

	// Failure path: classify outside the lock, then record the event.
	kind := ClassifyFreeTierError(status, body)
	if kind == KindNone {
		return
	}
	t.recordLimitEvent(provider, model, string(kind), status, body, header, now)
}

// recordLimitEvent opens or extends a provider-level cooldown.
func (t *Tracker) recordLimitEvent(provider, model, kind string, status int, body []byte, header map[string][]string, now time.Time) {
	regime := RegimeUnknown
	if t.regimes != nil {
		regime = t.regimes(provider, model)
	}
	t.mu.Lock()
	t.attempts[provider]++
	attempts := t.attempts[provider]
	resetAt, auto := ResetForRegime(regime, now, header, attempts)
	if auto {
		// Do not shorten an already-longer cooldown when a later event's
		// header asks for less: the first estimate stands until it passes.
		if existing, ok := t.cooling[provider]; ok && existing.After(resetAt) && !existing.IsZero() {
			resetAt = existing
		}
		t.cooling[provider] = resetAt
		delete(t.openEnd, provider)
	} else {
		t.openEnd[provider] = true
		delete(t.cooling, provider)
	}
	t.mu.Unlock()

	if t.store != nil {
		// The store upserts: an active row for the same provider bumps its
		// attempts counter instead of stacking a second wait.
		if err := t.store.RecordFreeTierLimitEvent(provider, model, kind, regime, ptrTime(resetAt, auto), truncError(body), now); err != nil {
			log.Printf("[freeresilience] record limit event %s failed: %v", provider, err)
		}
	}
	log.Printf("[freeresilience] %s (%s) hit %s; %s", provider, regimeOr(regime), kind,
		map[bool]string{true: "cooling until " + resetAt.Format(time.RFC3339), false: "cooling until success (no auto reset)"}[auto])
}

// IsCooling reports whether the provider is in its wait window and therefore
// skipped by the fallback chain.
func (t *Tracker) IsCooling(provider string) (bool, time.Time) {
	if t == nil {
		return false, time.Time{}
	}
	now := t.now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	if until, ok := t.cooling[provider]; ok && until.After(now) {
		return true, until
	}
	if t.openEnd[provider] {
		return true, time.Time{}
	}
	return false, time.Time{}
}

// HealthSnapshot returns the health of every provider the tracker knows
// about, plus any extra provider named in extra (the caller passes the full
// free-catalogue provider list so untried providers appear too).
func (t *Tracker) HealthSnapshot(extra []string, regimeOf RegimeFunc) []ProviderHealth {
	if t == nil {
		return nil
	}
	now := t.now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()

	seen := map[string]bool{}
	out := make([]ProviderHealth, 0, len(t.health)+len(extra))
	build := func(provider string) {
		if seen[provider] {
			return
		}
		seen[provider] = true
		out = append(out, t.healthOf(provider, now, regimeOf))
	}
	for _, provider := range extra {
		build(provider)
	}
	for provider := range t.health {
		build(provider)
	}
	for provider := range t.cooling {
		build(provider)
	}
	for provider := range t.openEnd {
		build(provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// HealthScore returns a sortable score for the chain's default ordering:
// healthy providers first (higher success rate first), cooling last.
func (t *Tracker) HealthScore(provider string) float64 {
	if t == nil {
		return 0
	}
	now := t.now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.healthOf(provider, now, nil)
	if h.Status == "cooling" {
		return -1
	}
	if h.SuccessRate < 0 {
		// No data: neutral, above a cooling provider, below any proven one.
		return 0.5
	}
	return h.SuccessRate
}

// healthOf computes one provider's health. Caller holds the lock.
func (t *Tracker) healthOf(provider string, now time.Time, regimeOf RegimeFunc) ProviderHealth {
	h := ProviderHealth{Provider: provider, SuccessRate: -1, SuccessRatePct: -1}
	if regime := regimeOrRegimeFunc(regimeOf, provider); regime != "" {
		h.Regime = regime
	}
	if until, ok := t.cooling[provider]; ok && until.After(now) {
		h.Status = "cooling"
		h.CoolingUntil = until.UTC().Format(time.RFC3339)
	} else if t.openEnd[provider] {
		h.Status = "cooling"
		h.CoolingOpen = true
	}
	ring := pruneOutcomes(t.health[provider], now)
	h.Samples = len(ring)
	if len(ring) > 0 {
		var success, r429, r403 int
		latencies := make([]float64, 0, len(ring))
		for _, o := range ring {
			if o.Success {
				success++
			}
			switch o.Status {
			case 429:
				r429++
			case 403:
				r403++
			}
			if o.LatencyMs > 0 {
				latencies = append(latencies, float64(o.LatencyMs))
			}
		}
		h.SuccessRate = float64(success) / float64(len(ring))
		h.SuccessRatePct = math2Round(h.SuccessRate * 100)
		h.RecentRate429 = r429
		h.Recent403 = r403
		h.P95LatencyMs = percentile95(latencies)
		if h.Status == "" && len(ring) >= 5 && h.SuccessRate < 0.7 {
			h.Status = "flaky"
		}
	}
	if h.Status == "" {
		h.Status = "healthy"
	}
	return h
}

// FlushHealth persists the outcome rings. Called by the scheduler; the
// endpoint keeps reading the in-memory state.
func (t *Tracker) FlushHealth() {
	if t == nil || t.store == nil {
		return
	}
	now := t.now().UTC()
	t.mu.Lock()
	defer t.mu.Unlock()
	for provider, ring := range t.health {
		pruned := pruneOutcomes(ring, now)
		t.health[provider] = pruned
		if len(pruned) == 0 {
			continue
		}
		if err := t.store.SaveFreeProviderHealth(provider, pruned, now); err != nil {
			log.Printf("[freeresilience] flush health %s failed: %v", provider, err)
		}
	}
}

// ExpireCooldowns lifts cooldowns whose reset has passed. Returns the
// providers that rejoined rotation. Called by the scheduler; also called by
// tests with an injected clock.
func (t *Tracker) ExpireCooldowns() []string {
	if t == nil {
		return nil
	}
	now := t.now().UTC()
	rejoinedSet := map[string]bool{}
	t.mu.Lock()
	for provider, until := range t.cooling {
		if !until.After(now) {
			delete(t.cooling, provider)
			t.attempts[provider] = 0
			rejoinedSet[provider] = true
		}
	}
	t.mu.Unlock()
	if t.store != nil {
		if expired, err := t.store.ExpireFreeTierCooldowns(now); err != nil {
			log.Printf("[freeresilience] expire cooldowns failed: %v", err)
		} else {
			for _, provider := range expired {
				rejoinedSet[provider] = true
			}
		}
	}
	if len(rejoinedSet) == 0 {
		return nil
	}
	rejoined := make([]string, 0, len(rejoinedSet))
	for provider := range rejoinedSet {
		rejoined = append(rejoined, provider)
	}
	sort.Strings(rejoined)
	return rejoined
}

// ClearAllCooldowns lifts every active cooldown, on explicit operator action
// (the dashboard's reset button). Returns the providers that were parked.
func (t *Tracker) ClearAllCooldowns(reason string) []string {
	if t == nil {
		return nil
	}
	now := t.now().UTC()
	t.mu.Lock()
	var cleared []string
	for provider := range t.cooling {
		cleared = append(cleared, provider)
	}
	for provider := range t.openEnd {
		if !containsString(cleared, provider) {
			cleared = append(cleared, provider)
		}
	}
	t.cooling = map[string]time.Time{}
	t.openEnd = map[string]bool{}
	t.attempts = map[string]int{}
	t.mu.Unlock()
	if t.store != nil {
		for _, provider := range cleared {
			if err := t.store.ClearFreeTierCooldowns(provider, now, reason); err != nil {
				log.Printf("[freeresilience] clear cooldown %s failed: %v", provider, err)
			}
		}
	}
	return cleared
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// pruneOutcomes drops outcomes older than the health window. Caller holds the
// lock or owns the slice.
func pruneOutcomes(ring []Outcome, now time.Time) []Outcome {
	cut := now.Add(-HealthWindow)
	idx := sort.Search(len(ring), func(i int) bool { return ring[i].At.After(cut) })
	if idx == 0 {
		return ring
	}
	return append([]Outcome(nil), ring[idx:]...)
}

// percentile95 is the nearest-rank p95 over latency samples in ms.
func percentile95(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sort.Float64s(samples)
	idx := int(mathCeil(0.95*float64(len(samples)))) - 1
	if idx < 0 {
		idx = 0
	}
	return samples[idx]
}

func truncError(body []byte) string {
	const max = 300
	if len(body) > max {
		return string(body[:max])
	}
	return string(body)
}

func ptrTime(t time.Time, ok bool) *time.Time {
	if !ok || t.IsZero() {
		return nil
	}
	return &t
}

func regimeOr(regime string) string {
	if regime == "" {
		return "unknown regime"
	}
	return regime
}

func regimeOrRegimeFunc(regimeOf RegimeFunc, provider string) string {
	if regimeOf == nil {
		return ""
	}
	return regimeOf(provider, "")
}

func mathCeil(v float64) float64 {
	if v != float64(int(v)) {
		return float64(int(v) + 1)
	}
	return v
}

func math2Round(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
