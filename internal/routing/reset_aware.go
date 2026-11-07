// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package routing

import (
	"sort"
	"sync/atomic"
	"time"
)

// Reset-aware scoring implements the `reset-aware` strategy: prefer candidates
// whose subscription quota windows (a rolling 5h session window and a rolling
// 7d weekly window, as providers report them) still hold quota, weighted by
// how soon each window resets. A window that resets soon is worth spending
// from — its quota expires anyway — while a window that just reset should be
// conserved. This mirrors OmniRoute's reset-aware ranking.

const (
	// Window geometry the providers' session/weekly limits use.
	sessionWindow = 5 * time.Hour
	weeklyWindow  = 7 * 24 * time.Hour

	// The blended score weighs the session window 35% against the weekly 65%:
	// weekly quota is the scarcer resource over a day of traffic.
	sessionScoreWeight = 0.35
	weeklyScoreWeight  = 0.65

	// Inside a window, remaining quota and reset pressure blend per window.
	// The short session window leans on pressure (0.55) because its quota
	// refills soon anyway; the weekly window leans on remaining (0.75 weight
	// on quota, 0.25 on pressure).
	sessionRemainingWeight = 0.45
	weeklyRemainingWeight  = 0.25

	// Exhaustion guard: below 10% session quota the score is scaled down
	// linearly to at most 5% of itself, so a nearly spent candidate is only
	// chosen when nothing healthier exists.
	exhaustionFloor     = 0.10
	exhaustionMinFactor = 0.05

	// tieBandFraction is the leading tie band for OrderResetAware: candidates
	// scoring within 5% of the leader (relative) rotate among themselves so
	// consecutive requests do not hammer the same near-best account.
	tieBandFraction = 0.05
)

// SessionWeeklyScore scores one candidate's two quota windows in [0,1].
//
// Each window blends its remaining quota with its reset pressure:
//
//	windowScore = remainingWeight*remaining + (1-remainingWeight)*resetPressure
//	resetPressure = clamp01(1 - msUntilReset/windowMs), 1 once the window has
//	already passed (resetIn <= 0)
//
// with (0.45 remaining, 0.55 pressure) for the 5h session window and (0.25,
// 0.75) for the 7d weekly window. The two windows combine 35% session + 65%
// weekly. When the session window is nearly spent (< 10% remaining) the whole
// score is scaled by max(0.05, sessionRemaining/0.10).
//
// now pins the evaluation instant so callers and tests can share a fixed
// clock; the pressure math itself is relative to the *_ResetIn durations the
// caller derived from that same instant.
func SessionWeeklyScore(sessionRemaining, weeklyRemaining float64, sessionResetIn, weeklyResetIn time.Duration, now time.Time) float64 {
	session := windowScore(sessionRemaining, sessionWindow, sessionResetIn, sessionRemainingWeight)
	weekly := windowScore(weeklyRemaining, weeklyWindow, weeklyResetIn, weeklyRemainingWeight)
	score := sessionScoreWeight*session + weeklyScoreWeight*weekly
	if sessionRemaining < exhaustionFloor {
		factor := sessionRemaining / exhaustionFloor
		if factor < exhaustionMinFactor {
			factor = exhaustionMinFactor
		}
		score *= factor
	}
	return clamp01(score)
}

// windowScore blends one window's remaining quota with its reset pressure.
func windowScore(remaining float64, window, resetIn time.Duration, remainingWeight float64) float64 {
	return remainingWeight*clamp01(remaining) + (1-remainingWeight)*resetPressure(window, resetIn)
}

// resetPressure rises from 0 (window just reset) to 1 (window about to reset
// or already past). A non-positive window is treated as fully spent.
func resetPressure(window, resetIn time.Duration) float64 {
	if window <= 0 {
		return 1
	}
	return clamp01(1 - float64(resetIn)/float64(window))
}

// ResetAwareCandidate is one candidate's quota telemetry for ordering.
type ResetAwareCandidate struct {
	SessionRemaining float64
	WeeklyRemaining  float64
	SessionResetIn   time.Duration
	WeeklyResetIn    time.Duration
}

// tieRotation rotates the leading tie band across calls. A package-level
// atomic counter keeps the rotation lock-free and shared by every combo.
var tieRotation atomic.Uint64

// OrderResetAware returns the candidates' indices sorted by reset-aware score,
// best first. Candidates within 5% of the leader form the leading tie band and
// rotate on every call, so equally-good accounts share load instead of
// whichever sorted first taking everything; the rest follow in strict score
// order. The input slice is not modified.
func OrderResetAware(candidates []ResetAwareCandidate, now time.Time) []int {
	scores := make([]float64, len(candidates))
	order := make([]int, len(candidates))
	for i, c := range candidates {
		scores[i] = SessionWeeklyScore(c.SessionRemaining, c.WeeklyRemaining, c.SessionResetIn, c.WeeklyResetIn, now)
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return scores[order[a]] > scores[order[b]]
	})

	top := 0.0
	if len(order) > 0 {
		top = scores[order[0]]
	}
	band := 0
	for band < len(order) && scores[order[band]] >= top*(1-tieBandFraction) {
		band++
	}
	if band > 1 {
		rotateLeading(order, band)
	}
	return order
}

// rotateLeading rotates the first band indices by the shared counter, so the
// band's members take turns leading.
func rotateLeading(order []int, band int) {
	offset := int((tieRotation.Add(1) - 1) % uint64(band))
	lead := make([]int, 0, band)
	lead = append(lead, order[offset:band]...)
	lead = append(lead, order[:offset]...)
	copy(order, lead)
}
