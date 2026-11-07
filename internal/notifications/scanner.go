// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package notifications

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/log"
	"9routerx/proxy/internal/models"
	"9routerx/proxy/internal/updater"
	"9routerx/proxy/internal/webhooks"
)

// fanout sends one freshly written feed entry to the operator's webhook
// targets. A package variable so the offline suite can record events instead
// of posting: the real path carries the SSRF guard, which refuses the loopback
// capture servers the test sandbox runs on. Production leaves it on the
// dispatcher, whose rate limiter dedupes on its side — the scanner adds none.
var fanout = func(ctx context.Context, repo *db.Repo, event webhooks.Event) {
	webhooks.NewDispatcher(repo).DispatchAll(ctx, event)
}

// notify writes one feed entry and, when the insert (or a dedupe refresh that
// re-alerts) actually landed, fans the same content out to the webhook
// targets. This is the single choke point every scanner condition writes
// through, so the fan-out needs exactly one hook. Event type is
// "notification.<category>" so a target can subscribe per category
// (notification.quota, notification.security, ...).
func (s *ConditionScanner) notify(ctx context.Context, at, dedupeKey, typ, severity, title, body string) (string, error) {
	id, err := s.repo.Notify(ctx, "", at, dedupeKey, typ, severity, title, body)
	if err != nil || id == "" {
		return id, err
	}
	fanout(ctx, s.repo, webhooks.Event{
		Type:     "notification." + typ,
		Title:    title,
		Body:     body,
		Severity: severity,
	})
	return id, nil
}

// ConditionScanner inspects system and key health to populate the notification feed.
type ConditionScanner struct {
	repo   *db.Repo
	now    func() time.Time
	stopCh chan struct{}
	doneCh chan struct{}
	mu     sync.Mutex
}

// NewScanner constructs a condition scanner.
func NewScanner(repo *db.Repo) *ConditionScanner {
	return &ConditionScanner{
		repo:   repo,
		now:    time.Now,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// SetClock allows tests to inject a simulated clock.
func (s *ConditionScanner) SetClock(nowFn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = nowFn
}

// Start launches a periodic background scan with a 5-minute interval plus random jitter.
func (s *ConditionScanner) Start() {
	go func() {
		defer close(s.doneCh)
		// Initial scan shortly after boot
		time.Sleep(time.Duration(1+rand.Intn(5)) * time.Second)
		_, _ = s.Scan(context.Background())

		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				// Apply up to 30s jitter
				jitter := time.Duration(rand.Intn(30)) * time.Second
				select {
				case <-s.stopCh:
					return
				case <-time.After(jitter):
				}
				_, _ = s.Scan(context.Background())
			}
		}
	}()
}

// Stop shuts down the background scanner.
func (s *ConditionScanner) Stop() {
	close(s.stopCh)
	<-s.doneCh
}

// ScanResult summarizes findings from a single scan pass.
type ScanResult struct {
	Created []string  `json:"created"`
	At      time.Time `json:"at"`
}

// Scan performs an on-demand pass over all condition sources.
func (s *ConditionScanner) Scan(ctx context.Context) (*ScanResult, error) {
	if s.repo == nil {
		return &ScanResult{At: s.now()}, nil
	}

	res := &ScanResult{Created: make([]string, 0), At: s.now()}

	// 1. Check API Key Expiry and Quota Pressure
	if err := s.scanKeys(ctx, res); err != nil {
		log.Warn("notifications", "key scan failed", "error", err)
	}

	// 2. Check Provider / Error Spike
	if err := s.scanErrorSpikes(ctx, res); err != nil {
		log.Warn("notifications", "error spike scan failed", "error", err)
	}

	// 3. Check for Pending Application Updates
	if err := s.scanUpdates(ctx, res); err != nil {
		log.Warn("notifications", "update scan failed", "error", err)
	}

	// 4. Check for Critical Security Logs
	if err := s.scanSecurityLogs(ctx, res); err != nil {
		log.Warn("notifications", "security log scan failed", "error", err)
	}

	return res, nil
}

func (s *ConditionScanner) scanKeys(ctx context.Context, res *ScanResult) error {
	keys, err := s.repo.GetApiKeys()
	if err != nil {
		return err
	}
	now := s.now().UTC()
	today := now.Format("2006-01-02")

	for _, k := range keys {
		if k == nil || k.IsActive != 1 {
			continue
		}
		label := keyLabel(k)

		// 1a. Expiry
		if k.ExpiresAt != nil && *k.ExpiresAt != "" {
			if exp, err := time.Parse(time.RFC3339, *k.ExpiresAt); err == nil && now.After(exp) {
				dedupeKey := fmt.Sprintf("key_expired:%s", k.ID)
				id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "key", "warn",
					fmt.Sprintf("API key expired: %s", label),
					fmt.Sprintf("Key expired at %s and will refuse incoming requests with 401.", *k.ExpiresAt),
				)
				if err == nil && id != "" {
					res.Created = append(res.Created, dedupeKey)
				}
			}
		}

		// 1b. Token Quota
		if k.TokenQuota != nil && *k.TokenQuota > 0 {
			quota := *k.TokenQuota
			used, _ := s.repo.GetApiKeyTokenUsage(k.Key)
			ratio := float64(used) / float64(quota)
			warnThreshold := 0.90
			if k.AlertPct > 0 {
				warnThreshold = float64(k.AlertPct) / 100.0
			}

			if ratio >= 1.0 {
				dedupeKey := fmt.Sprintf("quota:%s:tokens:critical", k.ID)
				id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "quota", "critical",
					fmt.Sprintf("Token quota exhausted: %s", label),
					fmt.Sprintf("Key used %d of %d tokens (100%%). Requests are refused with 403.", used, quota),
				)
				if err == nil && id != "" {
					res.Created = append(res.Created, dedupeKey)
				}
			} else if ratio >= warnThreshold {
				dedupeKey := fmt.Sprintf("quota:%s:tokens:warning", k.ID)
				id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "quota", "warn",
					fmt.Sprintf("Token quota warning: %s", label),
					fmt.Sprintf("Key has consumed %.1f%% of its token quota (%d of %d).", ratio*100, used, quota),
				)
				if err == nil && id != "" {
					res.Created = append(res.Created, dedupeKey)
				}
			}
		}

		// 1c. Daily Cost Limit
		if k.DailyCostLimit > 0 {
			spent := 0.0
			if k.CostDay == today {
				spent = k.CostSpent
			}
			ratio := spent / k.DailyCostLimit
			if ratio >= 1.0 {
				dedupeKey := fmt.Sprintf("quota:%s:cost:critical", k.ID)
				id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "quota", "critical",
					fmt.Sprintf("Daily cost limit exceeded: %s", label),
					fmt.Sprintf("Key spent $%.2f of $%.2f daily limit. Requests are refused with 429.", spent, k.DailyCostLimit),
				)
				if err == nil && id != "" {
					res.Created = append(res.Created, dedupeKey)
				}
			} else if ratio >= 0.90 {
				dedupeKey := fmt.Sprintf("quota:%s:cost:warning", k.ID)
				id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "quota", "warn",
					fmt.Sprintf("Daily cost warning: %s", label),
					fmt.Sprintf("Key has spent $%.2f of $%.2f daily budget (%.1f%%).", spent, k.DailyCostLimit, ratio*100),
				)
				if err == nil && id != "" {
					res.Created = append(res.Created, dedupeKey)
				}
			}
		}

		// 1d. Request Limit
		if k.RequestLimit > 0 {
			used := k.UsedRequests
			ratio := float64(used) / float64(k.RequestLimit)
			if ratio >= 1.0 {
				dedupeKey := fmt.Sprintf("quota:%s:requests:critical", k.ID)
				id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "quota", "critical",
					fmt.Sprintf("Request limit exceeded: %s", label),
					fmt.Sprintf("Key used %d of %d requests. Requests are refused with 429.", used, k.RequestLimit),
				)
				if err == nil && id != "" {
					res.Created = append(res.Created, dedupeKey)
				}
			} else if ratio >= 0.90 {
				dedupeKey := fmt.Sprintf("quota:%s:requests:warning", k.ID)
				id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "quota", "warn",
					fmt.Sprintf("Request limit warning: %s", label),
					fmt.Sprintf("Key has reached %d of %d requests (%.1f%%).", used, k.RequestLimit, ratio*100),
				)
				if err == nil && id != "" {
					res.Created = append(res.Created, dedupeKey)
				}
			}
		}
	}
	return nil
}

func (s *ConditionScanner) scanErrorSpikes(ctx context.Context, res *ScanResult) error {
	now := s.now().UTC()
	since := now.Add(-1 * time.Hour).Format("2006-01-02 15:04:05")

	rates, err := s.repo.GetProviderHourlyErrorRates(ctx, since)
	if err != nil {
		return err
	}

	for _, r := range rates {
		if r.Errors >= 10 {
			dedupeKey := fmt.Sprintf("spike:errors:%s:%s", r.Provider, r.Hour)
			title := fmt.Sprintf("High error rate for %s: %.1f%%", r.Provider, r.ErrorRate*100)
			body := fmt.Sprintf("%d out of %d requests failed in hour %s.", r.Errors, r.Total, r.Hour)
			id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "provider", "warn", title, body)
			if err == nil && id != "" {
				res.Created = append(res.Created, dedupeKey)
			}
		}
	}
	return nil
}

func (s *ConditionScanner) scanUpdates(ctx context.Context, res *ScanResult) error {
	info := updater.GetCachedInfo()
	if info != nil && info.HasUpdate {
		now := s.now().UTC().Format(time.RFC3339)
		dedupeKey := fmt.Sprintf("update:%s", info.LatestVersion)
		body := strings.TrimSpace(info.ReleaseNotes)
		if body == "" {
			body = fmt.Sprintf("Version %s is available for update.", info.LatestVersion)
		}
		id, err := s.notify(ctx, now, dedupeKey, "update", "info",
			fmt.Sprintf("Update available: %s", info.LatestVersion),
			body,
		)
		if err == nil && id != "" {
			res.Created = append(res.Created, dedupeKey)
		}
	}
	return nil
}

func (s *ConditionScanner) scanSecurityLogs(ctx context.Context, res *ScanResult) error {
	now := s.now().UTC()
	since := now.Add(-10 * time.Minute).Format(time.RFC3339)

	rows, err := s.repo.GetSecurityLogs(ctx, 10, "critical")
	if err != nil {
		return err
	}

	for _, entry := range rows {
		if entry.At < since {
			continue
		}
		dedupeKey := fmt.Sprintf("security:critical:%d", entry.ID)
		id, err := s.notify(ctx, now.Format(time.RFC3339), dedupeKey, "security", "critical",
			fmt.Sprintf("Security alert: %s", entry.Type),
			fmt.Sprintf("%s from IP %s", entry.Detail, entry.IP),
		)
		if err == nil && id != "" {
			res.Created = append(res.Created, dedupeKey)
		}
	}
	return nil
}

func keyLabel(k *models.APIKey) string {
	if k.Name != nil && strings.TrimSpace(*k.Name) != "" {
		return *k.Name
	}
	if k.KeyDisplay != nil && strings.TrimSpace(*k.KeyDisplay) != "" {
		return *k.KeyDisplay
	}
	return k.ID
}
