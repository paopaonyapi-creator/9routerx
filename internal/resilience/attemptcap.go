// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package resilience

// AttemptCap is the per-request dispatch ceiling an operator can set per
// model, per provider, or globally.
//
// One client request can be dispatched more than once: a combo walks its
// seats, every seat walks that provider's connections, and a failed pass may
// repeat after a Retry-After wait. Each dispatch spends the upstream account's
// quota, so an unbounded walk is a way to burn a day of quota on one prompt
// that no provider will accept. The cap bounds that walk.
//
// Resolution is most-specific-wins: a model entry beats a provider entry, and
// a provider entry beats the global value. A tier that is configured with 0
// disables the cap for everything it covers, including the tiers above it, so
// "this one model may retry freely" is expressible without turning the cap off
// everywhere. A tier that is not configured at all falls through to the next
// one, and an entirely unconfigured cap means "no limit" — the same behaviour
// an install had before this setting existed.
type AttemptCap struct {
	Global    int            `json:"global,omitempty"`
	Providers map[string]int `json:"providers,omitempty"`
	// Models accepts both the fully qualified "provider/model" id a combo seat
	// carries and the bare model id a direct request names.
	Models map[string]int `json:"models,omitempty"`
}

// Limit returns the dispatch ceiling that applies to one provider/model pair.
// Zero means unlimited.
func (c AttemptCap) Limit(provider, model string) int {
	if n, ok := c.Models[qualifiedModelKey(provider, model)]; ok {
		return clampCap(n)
	}
	if n, ok := c.Models[model]; ok {
		return clampCap(n)
	}
	if n, ok := c.Providers[provider]; ok {
		return clampCap(n)
	}
	return clampCap(c.Global)
}

// Reached reports whether a walk that has already issued `attempts` dispatches
// may issue one more. attempts counts dispatches made, not dispatches
// remaining: the check runs before the next dispatch, so the first attempt of
// a request passes against a cap of 1 and the second one does not.
func (c AttemptCap) Reached(provider, model string, attempts int) bool {
	limit := c.Limit(provider, model)
	return limit > 0 && attempts >= limit
}

// Empty reports whether the cap carries no configuration at all, which lets
// callers skip building it on the hot path.
func (c AttemptCap) Empty() bool {
	return c.Global == 0 && len(c.Providers) == 0 && len(c.Models) == 0
}

func clampCap(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func qualifiedModelKey(provider, model string) string {
	if provider == "" || model == "" {
		return ""
	}
	return provider + "/" + model
}

// ReachedScoped is the counting form the combo walk uses. The walk counts
// attempts from the request's fallback recorder, which already records every
// dispatch with its provider and model, so recomputing the three tier counts
// from that one list is cheaper than maintaining three separate counters —
// and it stays exact when a fallback combo recursion joins the same request.
//
// The tiers short-circuit exactly as Limit resolves them: a reached model
// tier is answered without consulting the provider or global tiers, and a
// tier set to 0 disables itself (Limit clamps 0 to unlimited, so the tier
// falls through to the next one by simply never reporting reached).
func (c AttemptCap) ReachedScoped(provider, model string, total, perProvider, perModel int) bool {
	if n, ok := c.Models[qualifiedModelKey(provider, model)]; ok {
		return clampCap(n) > 0 && perModel >= clampCap(n)
	}
	if n, ok := c.Models[model]; ok {
		return clampCap(n) > 0 && perModel >= clampCap(n)
	}
	if n, ok := c.Providers[provider]; ok {
		return clampCap(n) > 0 && perProvider >= clampCap(n)
	}
	n := clampCap(c.Global)
	return n > 0 && total >= n
}
