// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package schedule

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Codex quota auto-ping constants matching upstream QUOTA_AUTOPING_CONFIG (src/shared/constants/config.js:33-45).
const (
	DefaultCodexResetAtDrift     = 30 * time.Second
	DefaultCodexMinPingInterval  = 10 * time.Minute
	DefaultCodexFailureCooldown  = 15 * time.Minute
	CodexSessionQuotaKey         = "session"
)

// QuotaEntry represents one quota bucket reported by Codex usage API.
type QuotaEntry struct {
	Used      *float64 `json:"used,omitempty"`
	Total     *float64 `json:"total,omitempty"`
	Remaining *float64 `json:"remaining,omitempty"`
	ResetAt   string   `json:"resetAt,omitempty"`
	Unlimited bool     `json:"unlimited,omitempty"`
}

// CodexUsageReport holds quota buckets returned by Codex usage endpoint.
type CodexUsageReport struct {
	Quotas map[string]QuotaEntry `json:"quotas"`
}

// CodexAccount carries the connection facts required for auto-pinging.
type CodexAccount struct {
	ID                 string
	Provider           string
	AuthType           string // "oauth"
	AccessToken        string
	IsActive           bool
	LastPingAt         time.Time
	LastPingedResetAt  string
	LastPingedResetKey string
}

// CodexPingUpdater records successful ping timestamps back to persistence.
type CodexPingUpdater func(ctx context.Context, id string, lastPingAt time.Time, resetAt, resetKey string) error

// CodexUsageFetcher reads live usage quota from Codex.
type CodexUsageFetcher func(ctx context.Context, account CodexAccount) (*CodexUsageReport, error)

// CodexPingSender issues the tiny ping to warm the reset window.
type CodexPingSender func(ctx context.Context, account CodexAccount) error

// CodexReactiveScheduler executes reactive quota pings for Codex accounts based on sliding resetAt windows.
// Parity with serenhope/9router quotaAutoPing.js.
type CodexReactiveScheduler struct {
	mu           sync.Mutex
	running      bool
	now          func() time.Time
	resetCache   map[string]string    // connID -> resetAt
	failureCache map[string]time.Time // connID -> failedAt

	DriftThreshold  time.Duration
	MinPingInterval time.Duration
	FailureCooldown time.Duration
}

// NewCodexReactiveScheduler creates a new reactive scheduler.
func NewCodexReactiveScheduler() *CodexReactiveScheduler {
	return &CodexReactiveScheduler{
		now:             time.Now,
		resetCache:      make(map[string]string),
		failureCache:    make(map[string]time.Time),
		DriftThreshold:  DefaultCodexResetAtDrift,
		MinPingInterval: DefaultCodexMinPingInterval,
		FailureCooldown: DefaultCodexFailureCooldown,
	}
}

// SetClock allows tests to inject simulated time.
func (s *CodexReactiveScheduler) SetClock(nowFn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = nowFn
}

// ResetCache returns the internal cache of observed resetAt stamps (for inspection/testing).
func (s *CodexReactiveScheduler) ResetCache() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.resetCache))
	for k, v := range s.resetCache {
		copied[k] = v
	}
	return copied
}

// SetResetCache populates the resetCache (for test seeding).
func (s *CodexReactiveScheduler) SetResetCache(cache map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetCache = make(map[string]string, len(cache))
	for k, v := range cache {
		s.resetCache[k] = v
	}
}

// HasAnyCodexAutoPingEnabled reports whether any account is actively opted-in.
func HasAnyCodexAutoPingEnabled(settings map[string]any) bool {
	if settings == nil {
		return false
	}
	cfg, ok := settings[SettingsKey("codex")].(map[string]any)
	if !ok {
		return false
	}
	conns, ok := cfg["connections"].(map[string]any)
	if !ok {
		return false
	}
	for _, v := range conns {
		if b, ok := v.(bool); ok && b {
			return true
		}
	}
	return false
}

func normalizeResetKey(resetAt string) string {
	t, err := time.Parse(time.RFC3339, resetAt)
	if err != nil {
		return resetAt
	}
	return t.Truncate(time.Minute).UTC().Format(time.RFC3339)
}

func isQuotaExhausted(q QuotaEntry) bool {
	if q.Unlimited {
		return false
	}
	if q.Remaining != nil {
		return *q.Remaining <= 0
	}
	if q.Total != nil && *q.Total > 0 && q.Used != nil {
		return *q.Used >= *q.Total
	}
	return false
}

func hasExhaustedBlockingQuota(quotas map[string]QuotaEntry, sessionKey string) bool {
	for name, q := range quotas {
		if name == sessionKey || strings.Contains(strings.ToLower(name), "session") {
			continue
		}
		if isQuotaExhausted(q) {
			return true
		}
	}
	return false
}

// RunTick evaluates all Codex accounts and fires pings if sliding resetAt conditions are met.
func (s *CodexReactiveScheduler) RunTick(
	ctx context.Context,
	settings map[string]any,
	accounts []CodexAccount,
	fetchUsage CodexUsageFetcher,
	sendPing CodexPingSender,
	updateAccount CodexPingUpdater,
) []error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	now := s.now().UTC()
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	// 1. Check if codexAutoPing setting is present and has any opted-in connection
	if settings == nil {
		return nil
	}
	rawCfg, ok := settings[SettingsKey("codex")].(map[string]any)
	if !ok || rawCfg == nil {
		return nil
	}
	enabledMap, _ := rawCfg["connections"].(map[string]any)
	if len(enabledMap) == 0 {
		return nil
	}

	var errs []error
	for _, acc := range accounts {
		// Only active OAuth accounts that have opted in
		if !acc.IsActive || acc.AuthType != "oauth" {
			continue
		}
		optedIn, _ := enabledMap[acc.ID].(bool)
		if !optedIn {
			continue
		}

		s.mu.Lock()
		failedAt, hasFailed := s.failureCache[acc.ID]
		s.mu.Unlock()
		if hasFailed && now.Sub(failedAt) < s.FailureCooldown {
			continue
		}

		if fetchUsage == nil {
			continue
		}

		usage, err := fetchUsage(ctx, acc)
		if err != nil {
			s.mu.Lock()
			s.failureCache[acc.ID] = now
			s.mu.Unlock()
			errs = append(errs, fmt.Errorf("fetch usage %s: %w", acc.ID, err))
			continue
		}

		quotas := usage.Quotas
		sessionQuota, hasSession := quotas[CodexSessionQuotaKey]
		if !hasSession || sessionQuota.ResetAt == "" {
			continue
		}

		resetAt := sessionQuota.ResetAt
		resetAtTime, parseErr := time.Parse(time.RFC3339, resetAt)
		if parseErr != nil {
			continue
		}

		s.mu.Lock()
		cachedReset, hasCached := s.resetCache[acc.ID]
		s.resetCache[acc.ID] = resetAt
		s.mu.Unlock()

		// Rule 4: Do not ping on the first resetAt observation
		if !hasCached || cachedReset == "" {
			continue
		}

		// Rule 7: Do not ping if usage is zero
		if sessionQuota.Used == nil || *sessionQuota.Used <= 0 {
			continue
		}

		// Rule 8: Do not ping when weekly/blocking quota is exhausted
		if hasExhaustedBlockingQuota(quotas, CodexSessionQuotaKey) {
			continue
		}

		// Rule 5 & 6: Ping only when resetAt slides (drifts forward by >= DriftThreshold)
		cachedResetTime, _ := time.Parse(time.RFC3339, cachedReset)
		drift := resetAtTime.Sub(cachedResetTime)
		if drift < s.DriftThreshold {
			// Stable or insufficient drift -> do not ping
			continue
		}

		// Rule 6: Do not repeat inside minimum ping interval
		if !acc.LastPingAt.IsZero() && now.Sub(acc.LastPingAt) < s.MinPingInterval {
			continue
		}

		// Avoid duplicate ping for the exact same reset window
		resetKey := normalizeResetKey(resetAt)
		lastResetKey := acc.LastPingedResetKey
		if lastResetKey == "" && acc.LastPingedResetAt != "" {
			lastResetKey = normalizeResetKey(acc.LastPingedResetAt)
		}
		if lastResetKey == resetKey {
			continue
		}

		// All rules passed: fire ping!
		if sendPing != nil {
			if pErr := sendPing(ctx, acc); pErr != nil {
				s.mu.Lock()
				s.failureCache[acc.ID] = now
				s.mu.Unlock()
				errs = append(errs, fmt.Errorf("ping %s: %w", acc.ID, pErr))
				continue
			}
		}

		s.mu.Lock()
		delete(s.failureCache, acc.ID)
		s.mu.Unlock()

		if updateAccount != nil {
			if uErr := updateAccount(ctx, acc.ID, now, resetAt, resetKey); uErr != nil {
				errs = append(errs, fmt.Errorf("update account %s: %w", acc.ID, uErr))
			}
		}
	}

	return errs
}
