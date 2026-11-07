// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package resilience

import (
	"sync"
	"time"
)

// ModelLockout is the per-model layer of the FASE 5 breaker stack: a model
// that answers 429 or 5xx is locked out for its "provider:connectionID:model"
// tuple only, so one model's trouble never sinks the whole connection (its
// other models keep serving) or the provider (other connections keep serving).
//
// It complements the two existing layers: the provider breaker refuses
// everything on {408,500,502,503,504} bursts, and the per-account semaphore
// blocks the account after a 429. This layer sits between them — per model.
//
// Lockout rules (FASE 5 task doc):
//
//   - 429 with a Retry-After locks the tuple for that duration, capped at 1h
//     (a hostile or buggy header must not pin a model for hours).
//   - 429 without a usable Retry-After, and any 5xx, locks the tuple for a
//     base 60s that doubles per consecutive failure — 5xx capped at 10min,
//     headerless 429s at 1h.
//   - A success on the same tuple clears everything; any other status (400,
//     401, 403, 408, ...) locks nothing — those say something about the
//     request or the account, not about the model's availability.
//
// State is in-memory and process-local, like the breaker. The map is bounded
// by the distinct provider:connection:model tuples a process has seen — a
// config-driven, finite set on this hardware — and shrinks on every success.
type modelLock struct {
	until       time.Time
	consecutive int
}

const (
	modelLockBase = 60 * time.Second
	// modelRateLimitCap bounds a 429 lock whether it came from a Retry-After
	// header or from the headerless escalation.
	modelRateLimitCap = time.Hour
	// modelFailureCap bounds the 5xx escalation.
	modelFailureCap = 10 * time.Minute
	// maxBackoffShift keeps the doubling shift from overflowing int64.
	maxBackoffShift = 24
)

// modelLockRegistry guards the lockout map. now follows the Breaker pattern:
// an injectable clock so tests advance time without sleeping.
type modelLockRegistry struct {
	mu      sync.Mutex
	entries map[string]*modelLock
	now     func() time.Time
}

var modelLocks = &modelLockRegistry{
	entries: make(map[string]*modelLock),
	now:     time.Now,
}

// ModelLockKey builds the lockout key for one model on one connection.
func ModelLockKey(provider, connectionID, model string) string {
	return provider + ":" + connectionID + ":" + model
}

// RecordModelFailure records an upstream status against the tuple. A 429
// honors retryAfter (the parsed Retry-After header, 0 when absent); a 5xx
// escalates the base cooldown. Other statuses lock nothing.
func RecordModelFailure(provider, connectionID, model string, status int, retryAfter time.Duration) {
	modelLocks.recordFailure(ModelLockKey(provider, connectionID, model), status, retryAfter)
}

func (r *modelLockRegistry) recordFailure(key string, status int, retryAfter time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()

	entry := r.entries[key]
	consecutive := 1
	if entry != nil {
		consecutive = entry.consecutive + 1
	}

	var cooldown time.Duration
	switch {
	case IsRateLimitStatus(status) && retryAfter > 0:
		// The header is authoritative, within the cap.
		cooldown = min(retryAfter, modelRateLimitCap)
	case IsRateLimitStatus(status):
		cooldown = backoffFor(modelLockBase, modelRateLimitCap, consecutive)
	case status >= 500 && status <= 599:
		cooldown = backoffFor(modelLockBase, modelFailureCap, consecutive)
	default:
		return
	}

	until := now.Add(cooldown)
	if entry == nil {
		entry = &modelLock{}
		r.entries[key] = entry
	}
	entry.consecutive = consecutive
	// A second failure while already locked only ever extends the lock.
	if until.After(entry.until) {
		entry.until = until
	}
}

// backoffFor doubles base once per consecutive failure — the 1st failure waits
// base, the 2nd 2*base, the 3rd 4*base — capped at maxCooldown. The shift is
// clamped so the multiplication cannot overflow int64.
func backoffFor(base, maxCooldown time.Duration, consecutive int) time.Duration {
	shift := consecutive - 1
	if shift > maxBackoffShift {
		shift = maxBackoffShift
	}
	if shift < 0 {
		shift = 0
	}
	d := base * (1 << uint(shift))
	if d > maxCooldown {
		return maxCooldown
	}
	return d
}

// RecordModelSuccess clears the tuple's lockout and failure streak.
func RecordModelSuccess(provider, connectionID, model string) {
	modelLocks.recordSuccess(ModelLockKey(provider, connectionID, model))
}

func (r *modelLockRegistry) recordSuccess(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, key)
}

// ModelLocked reports whether the tuple is currently locked out and until
// when. An expired lock stops blocking immediately; the entry's failure
// streak is kept until a success clears it, so a model that keeps failing
// across cooldowns keeps escalating.
func ModelLocked(provider, connectionID, model string) (bool, time.Time) {
	return modelLocks.locked(ModelLockKey(provider, connectionID, model))
}

func (r *modelLockRegistry) locked(key string) (bool, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[key]
	if entry == nil {
		return false, time.Time{}
	}
	if !r.now().Before(entry.until) {
		return false, time.Time{}
	}
	return true, entry.until
}

// ResetModelLockouts clears every model lockout. Used by the dashboard and
// tests, matching ResetAllBreakers and ResetSemaphores.
func ResetModelLockouts() {
	modelLocks.mu.Lock()
	modelLocks.entries = make(map[string]*modelLock)
	modelLocks.mu.Unlock()
}
