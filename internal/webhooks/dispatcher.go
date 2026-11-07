// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import (
	"context"
	"sync"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/log"
)

// rateLimitWindow is the dedupe window: at most one notification per event
// type per minute, shared by every target. N destinations repeating the same
// alert is exactly the spam the window exists to stop — the dedupeKey pattern
// internal/db/notifications.go applies to the bell feed, applied in memory
// because a webhook send is not state worth a table row.
const rateLimitWindow = time.Minute

var (
	rateMu     sync.Mutex
	lastSentAt = map[string]time.Time{}
)

// allowSend reports — and records — whether eventType may notify at now.
func allowSend(eventType string, now time.Time) bool {
	rateMu.Lock()
	defer rateMu.Unlock()
	if last, ok := lastSentAt[eventType]; ok && now.Sub(last) < rateLimitWindow {
		return false
	}
	lastSentAt[eventType] = now
	return true
}

// resetRateLimit clears the dedupe window. Tests only: production rate limits
// are meant to survive across events.
func resetRateLimit() {
	rateMu.Lock()
	defer rateMu.Unlock()
	lastSentAt = map[string]time.Time{}
}

// nowRFC3339 stamps a generic envelope. A variable-free helper because the
// timestamp is informational; the dispatcher carries the injectable clock.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// Dispatcher fans events out to the webhookTargets rows. It holds the rate
// limiter's clock so the offline suite can advance time instead of sleeping.
type Dispatcher struct {
	repo *db.Repo
	// now is the rate limiter's clock. Production leaves it on time.Now.
	now func() time.Time
}

// NewDispatcher builds the fan-out over a repository.
func NewDispatcher(repo *db.Repo) *Dispatcher {
	return &Dispatcher{repo: repo, now: time.Now}
}

// DispatchAll sends event to every enabled target subscribed to it and returns
// immediately: each send runs in its own goroutine bounded by the adapter's
// 10s timeout, so a slow destination never holds the caller. One notification
// per event type per minute goes out; the rest of the window's events are
// dropped, which is the point. A dispatch failure is logged, never propagated.
//
// The event filter (and only the filter) is applied synchronously — a target
// that did not subscribe must not even spawn a goroutine.
func (d *Dispatcher) DispatchAll(ctx context.Context, event Event) {
	targets, err := d.repo.ListWebhookTargets(ctx)
	if err != nil {
		log.Warn("webhooks", "list webhook targets failed; event dropped", "error", err, "event", event.Type)
		return
	}

	pending := make([]Target, 0, len(targets))
	for _, row := range targets {
		if !row.Enabled || !MatchesFilter(row.FilterEvents, event.Type) {
			continue
		}
		t := NewTarget(row.Type, row.URL)
		if t == nil || !t.Enabled() {
			continue
		}
		pending = append(pending, t)
	}
	if len(pending) == 0 {
		return
	}
	// The rate slot is consumed only when a destination actually matched, so
	// an install whose only target is disabled never swallows an alert.
	if !allowSend(event.Type, d.now()) {
		return
	}
	for _, t := range pending {
		go sendWithTimeout(t, event)
	}
}

// sendWithTimeout runs one adapter send in its own lifecycle: the adapter
// bounds itself at 10s, and a panicking adapter is contained here because a
// fire-and-forget goroutine must never take the process down.
func sendWithTimeout(t Target, event Event) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error("webhooks", "webhook adapter panicked", "target", t.Type(), "panic", rec)
		}
	}()
	if err := t.Send(event); err != nil {
		log.Warn("webhooks", "webhook delivery failed", "target", t.Type(), "event", event.Type, "error", err)
	}
}
