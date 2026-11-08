// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Regime names, mirroring the shipped free-model catalogue
// (internal/providers/freemodels.go). The router is the only place that knows
// WHEN a provider's quota comes back — the catalogue documents the regime, so
// the recovery scheduler reads its reset expectation from the same data.
const (
	RegimeRecurringDaily    = "recurring-daily"
	RegimeRecurringMonthly  = "recurring-monthly"
	RegimeRecurringCredit   = "recurring-credit"
	RegimeRecurringUncapped = "recurring-uncapped"
	RegimeOneTimeInitial    = "one-time-initial"
	RegimeKeyless           = "keyless"
	RegimeUnknown           = ""
)

// Cooldown bounds for the short-cooldown family (keyless and uncapped
// providers). A keyless gate's refusal is an egress condition that clears in
// minutes, so the cooldown starts at KeylessCooldownBase and doubles per
// consecutive event up to KeylessCooldownMax. Beyond that the provider would
// be parked for longer than its whole tier is worth.
const (
	KeylessCooldownBase = 5 * time.Minute
	KeylessCooldownMax  = 15 * time.Minute
)

// ResetForRegime computes when a provider that just hit its limit is expected
// to serve again, from the regime the catalogue documents for it.
//
// Provider-supplied scheduling headers win over the regime default whenever
// they are present and parseable — the provider knows its own window better
// than the catalogue's assumption. now is injected so tests (and a future
// probe lane) can drive the clock instead of sleeping.
//
// The second return is false for regimes with no automatic recovery at all:
// a spent one-time credit does not come back on any clock, and the operator
// has to top it up or move on. Those providers are recorded as cooling with
// an open-ended reset (resetAt NULL) so the dashboard can show the condition
// without ever silently re-arming them.
func ResetForRegime(regime string, eventAt time.Time, header http.Header, attempts int) (time.Time, bool) {
	eventAt = eventAt.UTC()

	// The provider's own answer about when to come back outranks every
	// default. Bounded below by a minute (a sub-minute retry is what the
	// per-connection backoff already does) and above by the regime's horizon
	// (a bogus header cannot park a daily tier for a month).
	if d, ok := ResetHeaderDuration(header, eventAt); ok {
		horizon := regimeHorizon(regime)
		if d < time.Minute {
			d = time.Minute
		}
		if horizon > 0 && d > horizon {
			d = horizon
		}
		return eventAt.Add(d), true
	}

	switch regime {
	case RegimeRecurringDaily:
		// Next local midnight UTC. The catalogue's daily tiers publish their
		// own reset clocks in the header when they have one; without one,
		// midnight is the conservative reading of "recurring daily".
		return nextMidnight(eventAt), true
	case RegimeRecurringMonthly:
		// The 1st of the next month, 00:00 UTC.
		return nextMonthStart(eventAt), true
	case RegimeRecurringUncapped:
		// Permanently free but rate/concurrency limited: the same short
		// backoff family a keyless gate gets. A midnight park for a
		// per-minute throttle would idle a working provider.
		return eventAt.Add(shortCooldown(attempts)), true
	case RegimeKeyless:
		return eventAt.Add(shortCooldown(attempts)), true
	case RegimeRecurringCredit, RegimeOneTimeInitial:
		// A spent credit does not heal on a clock. No automatic recovery:
		// the event is stored with an open-ended reset so the dashboard can
		// show the remaining-credit condition; only a real success (the
		// operator topped up, the pool refilled) clears it early.
		return time.Time{}, false
	default:
		// Unknown regime: fail toward the short cooldown, not the day-long
		// one. Over-cooling a working provider costs more than one extra
		// failed attempt.
		return eventAt.Add(shortCooldown(attempts)), true
	}
}

// regimeHorizon bounds how far a provider-supplied header may park the
// provider, per regime. Zero means unbounded (not used today).
func regimeHorizon(regime string) time.Duration {
	switch regime {
	case RegimeRecurringDaily:
		// A daily tier cannot legitimately ask for more than a day plus
		// slack.
		return 25 * time.Hour
	case RegimeRecurringMonthly:
		// A monthly tier cannot legitimately ask for more than ~a month.
		return 45 * 24 * time.Hour
	case RegimeRecurringUncapped, RegimeKeyless:
		// The short-cooldown family: a header asking for hours is a spend
		// signal, not a throttle. Cap at an hour; the scheduler's probe and
		// any real success lift it earlier.
		return time.Hour
	default:
		return 24 * time.Hour
	}
}

// shortCooldown is the keyless/uncapped backoff curve: base doubled per
// consecutive event, capped.
func shortCooldown(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := float64(KeylessCooldownBase) * math.Pow(2, float64(attempts-1))
	if d > float64(KeylessCooldownMax) {
		return KeylessCooldownMax
	}
	return time.Duration(d)
}

// nextMidnight returns the next 00:00 UTC strictly after t.
func nextMidnight(t time.Time) time.Time {
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if !midnight.After(t) {
		midnight = midnight.AddDate(0, 0, 1)
	}
	return midnight
}

// nextMonthStart returns the 1st of the next month, 00:00 UTC, strictly after
// t.
func nextMonthStart(t time.Time) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	if !first.After(t) {
		first = first.AddDate(0, 1, 0)
	}
	return first
}

// ResetHeaderDuration reads the scheduling headers an upstream may set on a
// limit answer — Retry-After and x-ratelimit-reset — into a duration.
//
// Both spellings of the reset header are read, and delta-seconds, HTTP-date
// and ISO-timestamp values are all accepted, because the wild sends all
// three. Date forms are measured against now (the event instant), not the
// wall clock, so the answer is deterministic for a backdated event. A value
// that parses to nothing reports false, and the caller falls back to the
// regime default.
func ResetHeaderDuration(header http.Header, now time.Time) (time.Duration, bool) {
	if header == nil {
		return 0, false
	}
	// Scan rather than Get: upstream headers arrive canonized, but an
	// upstreamError built in-process can carry any spelling, and the
	// canonical form of x-ratelimit-reset is "X-Ratelimit-Reset" — a Get
	// with the common "X-RateLimit-Reset" spelling silently misses it.
	for key, values := range header {
		switch strings.ToLower(key) {
		case "retry-after", "x-ratelimit-reset":
		default:
			continue
		}
		if len(values) == 0 {
			continue
		}
		v := strings.TrimSpace(values[0])
		if v == "" {
			continue
		}
		// Delta seconds: "60", "1.5".
		if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second)), true
		}
		// HTTP date.
		if when, err := http.ParseTime(v); err == nil {
			d := when.Sub(now.UTC())
			if d > 0 {
				return d, true
			}
			// A past date means "now": a zero wait is not a schedule.
			return time.Minute, true
		}
		// ISO8601 timestamp.
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
			if when, err := time.Parse(layout, v); err == nil {
				d := when.Sub(now.UTC())
				if d > 0 {
					return d, true
				}
				return time.Minute, true
			}
		}
	}
	return 0, false
}
