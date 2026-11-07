// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package webexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/log"
	"9routerx/proxy/internal/sessionvault"
)

// HealthInterval is how often the health job validates every enabled
// provider's stored sessions. The provider ids and their settings switches
// live in status.go; this file runs the job over them.
const HealthInterval = 15 * time.Minute

// The notification shape a failed session check raises. The dedupe key folds
// every 15-minute re-detection into the one open notification.
const (
	sessionExpiredType     = "provider"
	SessionExpiredSeverity = "warn"
)

// HealthRunner validates every enabled provider's stored sessions on a fixed
// interval and raises a deduped warning for each account whose session has
// expired. The validation function is injected: the runner knows when and
// who to ask, never how to probe a provider.
type HealthRunner struct {
	// Pool is the session pool the verdicts are recorded in. The dashboard's
	// status endpoint reads the same instance.
	Pool *Pool
	// Vault is the encrypted session storage the accounts are listed from.
	Vault *sessionvault.Store
	// Repo raises the session-expired notifications.
	Repo *db.Repo
	// Validate decides whether one account's session still works. Never nil:
	// NewHealthRunner substitutes the stored-session presence check below, so
	// the infrastructure is exercised end to end until an executor registers
	// its real per-provider probe.
	Validate ValidateFunc

	interval time.Duration
}

// NewHealthRunner builds a runner over the repo. A nil validate selects the
// default validator (a stored session must exist and be non-empty).
func NewHealthRunner(repo *db.Repo, validate ValidateFunc) *HealthRunner {
	store := sessionvault.New(repo)
	if validate == nil {
		validate = presenceValidator(store)
	}
	return &HealthRunner{
		Pool:     NewPool(WithMaxReqPerMin(maxReqPerMinFromSettings(repo))),
		Vault:    store,
		Repo:     repo,
		Validate: validate,
		interval: HealthInterval,
	}
}

// maxReqPerMinFromSettings reads the operator's rate ceiling at boot; the
// loop re-reads it every pass so a settings change applies without a restart.
func maxReqPerMinFromSettings(repo *db.Repo) int {
	s, err := repo.GetSettings()
	if err != nil || s == nil || s.WebExecMaxReqPerMin <= 0 {
		return DefaultMaxReqPerMin
	}
	return s.WebExecMaxReqPerMin
}

// presenceValidator is the default validation: the account must have a
// stored session. It is the honest check the shared infrastructure can make
// without reaching into a provider's protocol.
func presenceValidator(store *sessionvault.Store) ValidateFunc {
	return func(ctx context.Context, provider, accountID string) error {
		data, err := store.Load(ctx, provider, accountID)
		if err != nil {
			return fmt.Errorf("load stored session: %w", err)
		}
		if strings.TrimSpace(data) == "" {
			return errors.New("stored session is empty")
		}
		return nil
	}
}

// Run loops the health pass every interval until ctx is done. The first pass
// waits out the interval: an operator enabling a provider should not get
// upstream probes the moment the process boots.
func (r *HealthRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.CheckAll(ctx)
		}
	}
}

// CheckAll runs one pass over every enabled provider, refreshing the rate
// ceiling from the settings row first so a saved change applies on the next
// pass without a restart.
func (r *HealthRunner) CheckAll(ctx context.Context) {
	settings, err := r.Repo.GetSettings()
	if err != nil {
		log.Warn("webexec", "health pass skipped; settings unreadable", "error", err)
		return
	}
	if settings.WebExecMaxReqPerMin > 0 {
		r.Pool.SetMaxReqPerMin(settings.WebExecMaxReqPerMin)
	}
	for _, provider := range Providers {
		if ctx.Err() != nil {
			return
		}
		if !ProviderEnabled(settings, provider) {
			continue
		}
		r.CheckProvider(ctx, provider)
	}
}

// AccountCheck is one account's outcome in a health pass.
type AccountCheck struct {
	AccountID string `json:"accountId"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// CheckResult is what a pass over one provider produced.
type CheckResult struct {
	Provider string         `json:"provider"`
	Accounts int            `json:"accounts"`
	OK       bool           `json:"ok"`
	Failures []AccountCheck `json:"failures,omitempty"`
}

// CheckProvider validates every stored session of one provider. A failed
// account cools the provider down, raises the deduped warning, and is
// reported — it does not abort the pass, so one dead cookie does not hide
// the state of the others.
func (r *HealthRunner) CheckProvider(ctx context.Context, provider string) CheckResult {
	entries, err := r.Vault.ListByProvider(ctx, provider)
	if err != nil {
		log.Warn("webexec", "could not list stored sessions", "provider", provider, "error", err)
		return CheckResult{Provider: provider}
	}
	result := CheckResult{Provider: provider, Accounts: len(entries), OK: true}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return result
		}
		if err := r.Pool.HealthCheck(ctx, provider, entry.AccountID, r.Validate); err != nil {
			result.OK = false
			result.Failures = append(result.Failures, AccountCheck{AccountID: entry.AccountID, Error: err.Error()})
			r.notifySessionExpired(ctx, provider, entry.AccountID, err)
			continue
		}
		if err := r.Vault.MarkValidated(ctx, provider, entry.AccountID); err != nil {
			log.Warn("webexec", "could not stamp lastValidated", "provider", provider,
				"account", entry.AccountID, "error", err)
		}
	}
	return result
}

// notifySessionExpired raises (or refreshes) the deduped warning notification
// for one expired account session.
func (r *HealthRunner) notifySessionExpired(ctx context.Context, provider, accountID string, checkErr error) {
	if r.Repo == nil {
		return
	}
	_, err := r.Repo.Notify(ctx, "", "", SessionExpiredDedupeKey(provider, accountID),
		sessionExpiredType, SessionExpiredSeverity,
		"Web executor session expired",
		fmt.Sprintf("The %s session for account %q failed validation and the provider is cooling down: %v",
			provider, accountID, checkErr))
	if err != nil {
		log.Warn("webexec", "could not record session-expired notification",
			"provider", provider, "account", accountID, "error", err)
	}
}

// StartHealthJob builds the process-wide runner over repo, publishes it for
// the dashboard, and starts its loop on ctx.
func StartHealthJob(ctx context.Context, repo *db.Repo, validate ValidateFunc) *HealthRunner {
	runner := NewHealthRunner(repo, validate)
	PublishRunner(runner)
	go runner.Run(ctx)
	return runner
}

// The runner is started by the app package, which owns the shutdown context,
// but the dashboard handler reports on it. It is published here instead of
// threaded through the router constructors — the same process-wide shape
// modelhealth's loop uses.
var (
	runnerMu     sync.RWMutex
	activeRunner *HealthRunner
)

// PublishRunner records the running health job. Called once at boot.
func PublishRunner(runner *HealthRunner) {
	runnerMu.Lock()
	defer runnerMu.Unlock()
	activeRunner = runner
}

// ActiveRunner returns the running health job, or nil when the feature was
// never started (a test boot, or a process built without the scheduler).
func ActiveRunner() *HealthRunner {
	runnerMu.RLock()
	defer runnerMu.RUnlock()
	return activeRunner
}
