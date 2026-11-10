// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/resilience"
)

// B6 wiring: the zero-retry dedup and the attempt cap ride the request
// context the way the fallback attempt recorder does. Both are armed once at
// the root combo entry and inherited by every fallback-combo recursion, so a
// multi-combo walk shares one registry and one counter — the storm the
// features bound spans fallback combos, not just one combo's seats.
//
// The two guards live in the COMBO walks only. The single-provider account
// loop (handleAccountFallback) deliberately stays unwired: its account walk
// is the A2 multi-key fallback, and marking a body after the first account
// would turn account fallback into a single-account lane — the opposite of
// what that path exists to do.

type routeDedupContextKey struct{}

// attachRouteDedup arms the request's zero-retry dedup when the operator has
// enabled it. A disabled switch or an unreadable settings row leaves the
// context bare: a nil registry skips nothing, which is the behaviour every
// install had before the setting existed. Safe to call on every entry — a
// context that already carries a registry is returned untouched, so the
// root arms it and the recursion inherits it.
func (h *ChatHandler) attachRouteDedup(ctx context.Context) context.Context {
	if routeDedupFrom(ctx) != nil {
		return ctx
	}
	if h == nil || h.Repo == nil {
		return ctx
	}
	settings, err := h.Repo.GetSettings()
	if err != nil || settings == nil || !settings.RetryDedupEnabled {
		return ctx
	}
	return context.WithValue(ctx, routeDedupContextKey{}, resilience.NewRetryDedup())
}

// routeDedupFrom returns the request's dedup registry, or nil when the dedup
// is not armed for this request.
func routeDedupFrom(ctx context.Context) *resilience.RetryDedup {
	if ctx == nil {
		return nil
	}
	d, _ := ctx.Value(routeDedupContextKey{}).(*resilience.RetryDedup)
	return d
}

// attemptCapReached reports whether dispatching one more request to
// provider/model would exceed the configured attempt ceiling. The settings
// cap converts to the resilience type at this boundary; an absent or empty
// cap short-circuits to false without counting anything. The settings read
// goes through the repo cache, so a walk pays the parse once no matter how
// many entries consult the cap.
func (h *ChatHandler) attemptCapReached(ctx context.Context, provider, model string) bool {
	if h == nil || h.Repo == nil {
		return false
	}
	settings, err := h.Repo.GetSettings()
	if err != nil || settings == nil {
		return false
	}
	cap := routeAttemptCap(settings)
	if cap.Empty() {
		return false
	}
	rec := FallbackAttemptRecorderFrom(ctx)
	if rec == nil {
		return cap.Reached(provider, model, 0)
	}
	var total, perProvider, perModel int
	for _, a := range rec.Snapshot() {
		total++
		if a.Provider == provider {
			perProvider++
			if a.Model == model {
				perModel++
			}
		}
	}
	return cap.ReachedScoped(provider, model, total, perProvider, perModel)
}

// routeAttemptCap converts the settings attempt-cap tiers into the resilience
// cap. A zero-value settings cap converts to an empty cap: unlimited, which
// is what every install resolves to without the setting.
func routeAttemptCap(settings *db.SettingsData) resilience.AttemptCap {
	if settings == nil {
		return resilience.AttemptCap{}
	}
	cap := resilience.AttemptCap{Global: settings.AttemptCap.Global}
	if len(settings.AttemptCap.Providers) > 0 {
		cap.Providers = make(map[string]int, len(settings.AttemptCap.Providers))
		for k, v := range settings.AttemptCap.Providers {
			cap.Providers[k] = v
		}
	}
	if len(settings.AttemptCap.Models) > 0 {
		cap.Models = make(map[string]int, len(settings.AttemptCap.Models))
		for k, v := range settings.AttemptCap.Models {
			cap.Models[k] = v
		}
	}
	return cap
}

// seatRetryDedupSkip reports whether this walk already dispatched the exact
// (body, provider, model) seat. The body handed in is the raw request body
// the walk started from — the mark happens once per combo entry before its
// connection loop, so every account the entry tries shares the entry's
// identity and the walk-level dedup never fights the multi-account fallback
// inside an entry.
func seatRetryDedupSkip(ctx context.Context, body []byte, provider, model string) bool {
	d := routeDedupFrom(ctx)
	if d == nil {
		return false
	}
	return d.ShouldSkipSeat(body, provider, model)
}

// markRetryDedupSeat records one combo entry as dispatched. Calling it on an
// un-armed context (dedup disabled) is a no-op.
func markRetryDedupSeat(ctx context.Context, body []byte, provider, model string) {
	if d := routeDedupFrom(ctx); d != nil {
		d.MarkSeat(body, provider, model)
	}
}
