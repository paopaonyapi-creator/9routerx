// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package routing

import "math"

// QuotaWeightedPick implements the `quota-weighted` strategy: spread load over
// candidates in proportion to the quota they have left, pacing down candidates
// that already hold requests.
//
// The pool splits into two tiers: tier A holds candidates still above their
// configured floor (RemainingPct > 0 and > FloorPct), tier B holds the rest.
// Tier A is preferred whenever it is non-empty, so a candidate's floor is only
// eaten into when nothing else has quota. Within the chosen tier each
// candidate's weight is max(0, RemainingPct)/(1+inflight): its remaining quota
// divided by one more than its in-flight requests, so doubling the load on a
// candidate halves its share.
//
// rand01 is a uniform sample in [0,1) supplied by the caller — production
// draws it from crypto/rand, tests pass fixed values. The pick walks the
// candidates' cumulative weights as half-open intervals [lo,hi): a candidate
// with zero weight owns an empty interval and can never be picked.
//
// Returns the winning index into candidates, or -1 when no candidate has
// positive weight (nothing is eligible — the caller should fall through to the
// next connection or the fallback chain).
func QuotaWeightedPick(candidates []QuotaCandidate, inflight []int, rand01 float64) int {
	if len(candidates) == 0 {
		return -1
	}
	preferAboveFloor := false
	for _, c := range candidates {
		if aboveFloor(c) {
			preferAboveFloor = true
			break
		}
	}

	pool := make([]int, 0, len(candidates))
	weights := make([]float64, 0, len(candidates))
	total := 0.0
	for i, c := range candidates {
		if aboveFloor(c) != preferAboveFloor {
			continue
		}
		w := quotaWeight(c, inflightAt(inflight, i))
		if w <= 0 {
			continue
		}
		pool = append(pool, i)
		weights = append(weights, w)
		total += w
	}
	if len(pool) == 0 {
		return -1
	}

	threshold := clampUnit(rand01) * total
	acc := 0.0
	for j, idx := range pool {
		acc += weights[j]
		if threshold < acc {
			return idx
		}
	}
	// Float rounding can deny the final interval its strict comparison; the
	// last eligible candidate is then the sample.
	return pool[len(pool)-1]
}

// QuotaCandidate is one candidate's quota telemetry, in percent.
type QuotaCandidate struct {
	RemainingPct float64
	// FloorPct is the reserve this candidate must keep: a candidate at or
	// below its floor only serves traffic when nothing is above its floor.
	FloorPct float64
}

// aboveFloor reports whether the candidate is in tier A: quota left and still
// above its reserve. NaN remaining is not above the floor.
func aboveFloor(c QuotaCandidate) bool {
	return c.RemainingPct > 0 && c.RemainingPct > c.FloorPct
}

// quotaWeight is the candidate's sampling weight: remaining quota paced down
// by one more than its in-flight requests.
func quotaWeight(c QuotaCandidate, inFlight int) float64 {
	w := c.RemainingPct
	if math.IsNaN(w) || w < 0 {
		w = 0
	}
	if inFlight < 0 {
		inFlight = 0
	}
	return w / float64(1+inFlight)
}

// inflightAt reads the in-flight count for a candidate, defaulting to 0 when
// the caller passed a shorter slice.
func inflightAt(inflight []int, i int) int {
	if i < len(inflight) {
		return inflight[i]
	}
	return 0
}

// clampUnit folds rand01 into [0,1): NaN and negatives map to 0, and values
// at or above 1 map just below 1 so the sample stays inside the last
// candidate's half-open interval.
func clampUnit(rand01 float64) float64 {
	if math.IsNaN(rand01) || rand01 < 0 {
		return 0
	}
	if rand01 >= 1 {
		return math.Nextafter(1, 0)
	}
	return rand01
}
