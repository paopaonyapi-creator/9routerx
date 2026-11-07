// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package tlsstealth

import (
	"context"
	"fmt"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/log"
)

// AlertEventType is the webhook event the daily alert dispatches. It is not
// one of webhooks' built-in event constants on purpose: the dispatcher
// matches filters with "*" or an explicit list, so an operator who wants
// this alert lists this string in filterEvents. The string lives here rather
// than in webhooks because the seam is the NotifyFunc callback — this
// package never imports webhooks, so neither can grow a dependency on the
// other.
const AlertEventType = "tls.stealth.enabled"

// LegalNotice is the compliance text every alert and the dashboard banner
// carry, worded after the stealth guide's Legal and Ethical Notice. It must
// stay accurate: the feature reshapes requests to look like a browser the
// user is not running, and the only thing that makes that acceptable is the
// user's own standing with the upstream — so the text says exactly that,
// every day, wherever the status or an alert is shown.
const LegalNotice = "TLS stealth presents a browser fingerprint to upstream sites. " +
	"It is a compatibility layer between this router and your own official accounts, " +
	"not a means of evading provider Terms of Service, fraud detection, or credential sharing. " +
	"You remain bound by the upstream terms you accepted for each account."

// alertTitle is the feed entry's headline. The body carries the legal notice.
const alertTitle = "TLS stealth is enabled"

// The feed vocabulary: "security" because the entry is a compliance notice
// about how requests leave the router, "warn" because it must read as a
// yellow banner, not an FYI.
const (
	alertNotificationType = "security"
	alertSeverity         = "warn"
)

// defaultAlertInterval is the mandated cadence: one alert per day while the
// flag is on. Daily, not on-enable-once: a banner the operator saw once in
// March is not consent in November.
const defaultAlertInterval = 24 * time.Hour

// alertDedupeKeyPrefix anchors the day-scoped key. The suffix is the UTC
// date, so the key is stable within a day (the feed's unique index collapses
// restarts and double fires onto one row) and new every day (a fresh row
// recurs instead of silently refreshing a stale one forever).
const alertDedupeKeyPrefix = "tls-stealth-enabled-24h"

// alertDedupeKey builds the day-scoped dedupe key for one instant. UTC, not
// local: a server whose timezone moves (DST, relocation) must not shift the
// alert's cadence.
func alertDedupeKey(now time.Time) string {
	return fmt.Sprintf("%s:%s", alertDedupeKeyPrefix, now.UTC().Format("2006-01-02"))
}

// alertBody is the feed entry's text for one profile.
func alertBody(profile string) string {
	return fmt.Sprintf(
		"Requests on the web-executor lane carry the %s browser TLS fingerprint. %s This notice repeats daily while the flag is on.",
		profile, LegalNotice)
}

// NotifyFunc is the seam to the webhook fan-out. Central wiring connects it
// to webhooks.Dispatcher.DispatchAll (building the webhooks.Event from these
// fields); this package passes only strings so it cannot grow a dependency
// on the dispatcher's types.
type NotifyFunc func(eventType, title, body, severity string)

// loopConfig carries the loop's injectables: the cadence and the clock the
// dedupe key and timestamps are taken from. Production leaves both alone.
type loopConfig struct {
	interval time.Duration
	now      func() time.Time
}

// LoopOption adjusts one StartAlertLoop run.
type LoopOption func(*loopConfig)

// WithInterval overrides the 24h cadence. Tests only in practice; an
// operator who wants a faster cadence should say so in a change, not in a
// flag, because faster alerts stop being a banner and become noise.
func WithInterval(d time.Duration) LoopOption {
	return func(c *loopConfig) {
		if d > 0 {
			c.interval = d
		}
	}
}

// WithClock injects the clock the dedupe key and notification timestamps are
// read from. Tests only: production alerts must use the wall clock.
func WithClock(now func() time.Time) LoopOption {
	return func(c *loopConfig) {
		if now != nil {
			c.now = now
		}
	}
}

// StartAlertLoop runs the mandatory daily alert for as long as the context
// lives: one notification-feed entry (day-scoped dedupe key, warn severity,
// legal notice in the body) plus one NotifyFunc dispatch per interval.
//
// A disabled flag is a no-op — the loop does not start, nothing is written,
// and AlertLoopRunning stays false. If the flag is turned off after the loop
// started, the loop keeps running but every pass rechecks the flag and stays
// silent, so re-enabling resumes the alerts without a restart. The first
// alert fires immediately: an operator who just enabled the feature should
// see the notice now, not tomorrow.
func (s *Stealth) StartAlertLoop(ctx context.Context, repo *db.Repo, notify NotifyFunc, opts ...LoopOption) {
	if !s.Enabled() {
		return
	}
	cfg := loopConfig{interval: defaultAlertInterval, now: s.clock}
	for _, opt := range opts {
		opt(&cfg)
	}
	s.alertLoopRunning.Store(true)
	go func() {
		defer s.alertLoopRunning.Store(false)
		ticker := time.NewTicker(cfg.interval)
		defer ticker.Stop()
		for {
			s.alertOnce(ctx, repo, notify, cfg.now)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// alertOnce performs one alert pass. The Enabled recheck lives here, not only
// in StartAlertLoop, so a loop that outlived a disable-then-re-enable cycle
// cannot be tricked into alerting while off by a long tick gap.
func (s *Stealth) alertOnce(ctx context.Context, repo *db.Repo, notify NotifyFunc, now func() time.Time) {
	if !s.Enabled() || repo == nil {
		return
	}
	at := now().UTC()
	title := alertTitle
	body := alertBody(s.ActiveProfile())
	if _, err := repo.Notify(ctx, "", at.Format(time.RFC3339), alertDedupeKey(at),
		alertNotificationType, alertSeverity, title, body); err != nil {
		// The feed write failing is logged, never propagated: an alert must
		// not take down the caller that started the loop.
		log.Warn("tlsstealth", "daily stealth alert notification failed", "error", err)
		return
	}
	if notify != nil {
		notify(AlertEventType, title, body, alertSeverity)
	}
}
