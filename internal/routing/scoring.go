// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package routing implements FASE 5 routing strategies: the `auto` candidate
// score, cache-optimized sticky selection via rendezvous hashing, quota-weighted
// random selection, and reset-aware ordering. The strategies are pure functions
// over caller-supplied state so the request path can feed them whatever
// telemetry a provider exposes and tests can pin every input.
package routing

import "math"

// The `auto` score narrows OmniRoute's 16-factor ranking to the 5 factors the
// FASE 5 task doc deliberately keeps: health, quota, cost, latency and
// cache-affinity. The dropped factors (region, context fit, provider tier, and
// the rest) either have no telemetry in this gateway or are already enforced by
// the fallback chain, so a 5-factor score is all the signal there is.

// Factor weights. They sum to 1.0 so the score stays in [0,1] without a final
// normalization step.
const (
	weightHealth        = 0.25
	weightQuota         = 0.20
	weightCostInv       = 0.20
	weightLatencyInv    = 0.15
	weightCacheAffinity = 0.20
)

// ScoreInputs is everything the auto score knows about one candidate. Every
// numeric field is optional telemetry: a zero or invalid value folds to the
// factor documented on the matching field, never to NaN.
type ScoreInputs struct {
	// BreakerState is the candidate provider's resilience breaker state
	// ("CLOSED", "DEGRADED", "HALF_OPEN", "OPEN"). Empty or unknown strings
	// count as healthy: the breaker only downgrades a provider on evidence.
	BreakerState string

	// QuotaRemaining and QuotaTotal are the candidate's remaining and total
	// quota in any consistent unit. QuotaTotal <= 0 (no quota telemetry) maps
	// the factor to 0, per the task doc's "invalid input maps to 0".
	QuotaRemaining float64
	QuotaTotal     float64

	// CostPer1M is the candidate's price per million tokens; MaxCostPer1M is
	// the priciest candidate in the pool. MaxCostPer1M <= 0 (pool-wide cost
	// data missing) maps the factor to 1 so nobody is penalized for it.
	CostPer1M    float64
	MaxCostPer1M float64

	// P95LatencyMs is the candidate's p95 latency; MaxLatencyMs is the slowest
	// candidate in the pool. MaxLatencyMs <= 0 maps the factor to 1.
	P95LatencyMs float64
	MaxLatencyMs float64

	// StickyAccount is the account the prompt's cache key currently maps to
	// (empty when there is none); CandidateAccount is the account being scored.
	StickyAccount    string
	CandidateAccount string
}

// ScoreCandidate returns the candidate's weighted score in [0,1]. Each factor
// is clamped to [0,1] before weighting; a NaN or invalid input folds its factor
// to 0, so the result is never NaN.
func ScoreCandidate(in ScoreInputs) float64 {
	score := weightHealth*healthFactor(in.BreakerState) +
		weightQuota*quotaFactor(in.QuotaRemaining, in.QuotaTotal) +
		weightCostInv*costFactor(in.CostPer1M, in.MaxCostPer1M) +
		weightLatencyInv*latencyFactor(in.P95LatencyMs, in.MaxLatencyMs) +
		weightCacheAffinity*cacheAffinityFactor(in.StickyAccount, in.CandidateAccount)
	return clamp01(score)
}

// healthFactor maps the breaker state: CLOSED admits everything (1.0),
// HALF_OPEN is the probe window (0.5), OPEN admits nothing (0.0). DEGRADED and
// unknown states also map to 1.0 because they admit every request — the
// breaker, not the score, is what refuses traffic.
func healthFactor(state string) float64 {
	switch state {
	case "OPEN":
		return 0.0
	case "HALF_OPEN":
		return 0.5
	default:
		return 1.0
	}
}

// quotaFactor is remaining/total clamped. A non-positive total means the
// provider exposes no quota and the factor scores 0 rather than guessing.
func quotaFactor(remaining, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return clamp01(remaining / total)
}

// costFactor is 1 - cost/maxCost over the pool's priciest candidate: cheaper
// candidates score higher. Without pool-wide cost data the factor is neutral 1.
func costFactor(cost, maxCost float64) float64 {
	if maxCost <= 0 {
		return 1
	}
	if math.IsNaN(cost) || cost < 0 {
		return 0
	}
	return clamp01(1 - cost/maxCost)
}

// latencyFactor is 1 - p95/maxLatency over the pool's slowest candidate.
// Without pool-wide latency data the factor is neutral 1.
func latencyFactor(p95Ms, maxLatencyMs float64) float64 {
	if maxLatencyMs <= 0 {
		return 1
	}
	if math.IsNaN(p95Ms) || p95Ms < 0 {
		return 0
	}
	return clamp01(1 - p95Ms/maxLatencyMs)
}

// cacheAffinityFactor is 1.0 when the candidate is the account the prompt's
// cache key already maps to — reusing it keeps the provider's prompt cache
// warm — and 0.0 otherwise. No sticky account means no affinity anywhere.
func cacheAffinityFactor(sticky, candidate string) float64 {
	if sticky == "" || sticky != candidate {
		return 0
	}
	return 1
}

// clamp01 folds v into [0,1], mapping NaN to 0 per the task doc's rule that
// invalid input scores 0.
func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
