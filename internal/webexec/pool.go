// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webexec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Pool defaults: a bounded set of tracked browser sessions, a 10-minute idle
// TTL after which an unused slot is reclaimed, and the back-off a provider
// enters after one of its sessions fails validation.
const (
	DefaultMaxSessions = 16
	DefaultIdleTTL     = 10 * time.Minute
	DefaultCooldown    = 15 * time.Minute
)

// Sentinel errors callers branch on with errors.Is. Everything else is
// wrapped with an op prefix.
var (
	// ErrRateLimited means the account exhausted its requests-per-minute window.
	ErrRateLimited = errors.New("webexec: account rate limit exceeded")
	// ErrPoolFull means every tracked session slot is live and fresh.
	ErrPoolFull = errors.New("webexec: session pool is full")
	// ErrCooldown means the provider is backing off after a failed session check.
	ErrCooldown = errors.New("webexec: provider cooling down after failed session check")
)

// ValidateFunc decides whether one account's stored session still works. The
// health job and the dashboard's test endpoint inject it, so the pool never
// needs to know how any provider authenticates.
type ValidateFunc func(ctx context.Context, provider, accountID string) error

// SessionSlot is the handle one Acquire hands back and Release takes in.
// There is no browser behind it: the slot is the pool's bookkeeping for one
// live request against one provider account.
type SessionSlot struct {
	Provider   string
	AccountID  string
	AcquiredAt time.Time
}

// sessionState is the pool's per-account record. inUse counts concurrent
// holders, so one account can serve several requests at once while the slot
// stays a single entry.
type sessionState struct {
	provider    string
	accountID   string
	inUse       int
	lastUsed    time.Time
	lastCheckAt time.Time
	checkOK     bool
}

// Pool tracks the web executors' browser sessions: which accounts are live,
// whether their last validation passed, and which provider is cooling down.
// Slots idle past the TTL are reclaimed on the next touch; there is no real
// browser to close.
type Pool struct {
	mu          sync.Mutex
	slots       map[string]*sessionState
	cooldowns   map[string]time.Time
	limiter     *RateLimiter
	maxSessions int
	idleTTL     time.Duration
	cooldown    time.Duration
	now         func() time.Time
}

// PoolOption customises a Pool at construction.
type PoolOption func(*Pool)

// WithMaxSessions caps the tracked session slots (default 16).
func WithMaxSessions(n int) PoolOption {
	return func(p *Pool) {
		if n > 0 {
			p.maxSessions = n
		}
	}
}

// WithIdleTTL overrides the 10-minute idle reclamation bound.
func WithIdleTTL(d time.Duration) PoolOption {
	return func(p *Pool) {
		if d > 0 {
			p.idleTTL = d
		}
	}
}

// WithMaxReqPerMin overrides the per-account rate ceiling (default 20).
func WithMaxReqPerMin(n int) PoolOption {
	return func(p *Pool) {
		if n > 0 {
			p.limiter.SetMax(n)
		}
	}
}

// WithClock injects the clock. Tests pass a fixed func to make TTL and
// cooldown behaviour deterministic.
func WithClock(now func() time.Time) PoolOption {
	return func(p *Pool) {
		if now != nil {
			p.now = now
		}
	}
}

// NewPool builds a session pool with the repo defaults.
func NewPool(opts ...PoolOption) *Pool {
	p := &Pool{
		slots:       make(map[string]*sessionState),
		cooldowns:   make(map[string]time.Time),
		limiter:     NewRateLimiter(DefaultMaxReqPerMin, DefaultRateWindow),
		maxSessions: DefaultMaxSessions,
		idleTTL:     DefaultIdleTTL,
		cooldown:    DefaultCooldown,
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Acquire takes one request slot for the provider account. The account's rate
// window is charged even when the slot already exists: the limit is per
// request, not per session.
func (p *Pool) Acquire(provider, accountID string) (*SessionSlot, error) {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepIdleLocked(now)
	if until, cooling := p.cooldowns[provider]; cooling && now.Before(until) {
		return nil, fmt.Errorf("acquire %s/%s: %w", provider, accountID, ErrCooldown)
	}
	if !p.limiter.Allow(accountID, now) {
		return nil, fmt.Errorf("acquire %s/%s: %w", provider, accountID, ErrRateLimited)
	}
	key := slotKey(provider, accountID)
	st := p.slots[key]
	if st == nil {
		if len(p.slots) >= p.maxSessions {
			return nil, fmt.Errorf("acquire %s/%s: %w", provider, accountID, ErrPoolFull)
		}
		st = &sessionState{provider: provider, accountID: accountID}
		p.slots[key] = st
	}
	st.inUse++
	st.lastUsed = now
	return &SessionSlot{Provider: provider, AccountID: accountID, AcquiredAt: now}, nil
}

// Release returns a slot acquired earlier. Unknown or double releases are
// ignored: the caller's end state — one fewer holder — already holds.
func (p *Pool) Release(slot *SessionSlot) {
	if slot == nil {
		return
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.slots[slotKey(slot.Provider, slot.AccountID)]
	if st == nil || st.inUse == 0 {
		return
	}
	st.inUse--
	st.lastUsed = now
}

// HealthCheck runs the injected validation for one account and records the
// verdict. A failure cools the whole provider down; a success clears that
// cooldown, so the first account to validate again reopens the provider. The
// record exists even for an account that never acquired.
func (p *Pool) HealthCheck(ctx context.Context, provider, accountID string, validate ValidateFunc) error {
	if validate == nil {
		return fmt.Errorf("healthCheck %s/%s: no validate function registered", provider, accountID)
	}
	err := validate(ctx, provider, accountID)
	now := p.now()

	p.mu.Lock()
	defer p.mu.Unlock()
	key := slotKey(provider, accountID)
	st := p.slots[key]
	if st == nil {
		// A health record for an existing account is not a new browser session;
		// it may push the map past maxSessions, which only Acquire enforces.
		st = &sessionState{provider: provider, accountID: accountID}
		p.slots[key] = st
	}
	st.lastCheckAt = now
	if err != nil {
		st.checkOK = false
		p.cooldowns[provider] = now.Add(p.cooldown)
		return fmt.Errorf("healthCheck %s/%s: %w", provider, accountID, err)
	}
	st.checkOK = true
	st.lastUsed = now
	delete(p.cooldowns, provider)
	return nil
}

// CooldownActive reports whether the provider is inside its post-failure
// back-off.
func (p *Pool) CooldownActive(provider string) bool {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	until, cooling := p.cooldowns[provider]
	return cooling && now.Before(until)
}

// ResetRate clears one account's rate window, the retry path after a session
// refresh.
func (p *Pool) ResetRate(accountID string) { p.limiter.Reset(accountID) }

// SetMaxReqPerMin updates the per-account rate ceiling live.
func (p *Pool) SetMaxReqPerMin(n int) { p.limiter.SetMax(n) }

// SweepIdle reclaims slots idle past the TTL. The health job calls it once
// per pass; Acquire also sweeps on every call, so a pool that only serves
// traffic stays bounded without the job.
func (p *Pool) SweepIdle() {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepIdleLocked(now)
}

// PoolSnapshot is one provider's pool state for the dashboard.
type PoolSnapshot struct {
	Provider           string
	Sessions           int
	Active             int
	LastUsed           time.Time
	SessionValid       bool
	RateLimitRemaining int
	RateLimitMax       int
	CooldownActive     bool
}

// Snapshot reports one provider's pool state. rateLimitRemaining is the
// worst case across the provider's accounts, so a panel reading it knows the
// tightest budget any of its sessions is working under.
func (p *Pool) Snapshot(provider string) PoolSnapshot {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	snap := PoolSnapshot{
		Provider:           provider,
		RateLimitRemaining: p.limiter.Max(),
		RateLimitMax:       p.limiter.Max(),
		CooldownActive:     p.cooldownActiveLocked(provider, now),
	}
	var lastUsed time.Time
	for _, st := range p.slots {
		if st.provider != provider {
			continue
		}
		snap.Sessions++
		snap.Active += st.inUse
		if st.lastUsed.After(lastUsed) {
			lastUsed = st.lastUsed
		}
		if st.checkOK && now.Sub(st.lastCheckAt) < p.idleTTL {
			snap.SessionValid = true
		}
		if r := p.limiter.Remaining(st.accountID, now); r < snap.RateLimitRemaining {
			snap.RateLimitRemaining = r
		}
	}
	snap.LastUsed = lastUsed
	return snap
}

// sweepIdleLocked reclaims idle slots and expired cooldowns. Callers hold mu.
func (p *Pool) sweepIdleLocked(now time.Time) {
	for key, st := range p.slots {
		if st.inUse == 0 && now.Sub(st.lastUsed) > p.idleTTL {
			delete(p.slots, key)
		}
	}
	for provider, until := range p.cooldowns {
		if !now.Before(until) {
			delete(p.cooldowns, provider)
		}
	}
}

func (p *Pool) cooldownActiveLocked(provider string, now time.Time) bool {
	until, cooling := p.cooldowns[provider]
	return cooling && now.Before(until)
}

// slotKey namespaces one account inside one provider. A NUL separator cannot
// appear in a provider id or an account e-mail, so the key is unambiguous.
func slotKey(provider, accountID string) string {
	return provider + "\x00" + accountID
}
