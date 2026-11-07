// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package routing

import (
	"math"
	"testing"
)

// scoreEpsilon absorbs float rounding in the weighted sum; the weights are
// decimal fractions so exact binary representation is not expected.
const scoreEpsilon = 1e-9

// Weight reference: health 0.25, quota 0.20, costInv 0.20, latencyInv 0.15,
// cacheAffinity 0.20. An unset QuotaTotal/MaxCostPer1M/MaxLatencyMs and an
// unset sticky account each have a documented neutral-or-zero fold (see
// scoring.go), which the expectations below account for.
func TestScoreCandidate(t *testing.T) {
	tests := []struct {
		name string
		in   ScoreInputs
		want float64
	}{
		{
			name: "all factors optimal scores 1",
			in: ScoreInputs{
				BreakerState:   "CLOSED",
				QuotaRemaining: 80, QuotaTotal: 80,
				CostPer1M: 0, MaxCostPer1M: 1000,
				P95LatencyMs: 0, MaxLatencyMs: 10000,
				StickyAccount: "acct-1", CandidateAccount: "acct-1",
			},
			want: 1,
		},
		{
			name: "all factors worst scores 0",
			in: ScoreInputs{
				BreakerState:   "OPEN",
				QuotaRemaining: 0, QuotaTotal: 100,
				CostPer1M: 1000, MaxCostPer1M: 1000,
				P95LatencyMs: 10000, MaxLatencyMs: 10000,
				StickyAccount: "acct-1", CandidateAccount: "acct-2",
			},
			want: 0,
		},
		{
			name: "mixed factors hand-computed",
			// 0.25*0.5 + 0.20*0.4 + 0.20*0.8 + 0.15*0.6 + 0.20*1 = 0.655
			in: ScoreInputs{
				BreakerState:   "HALF_OPEN",
				QuotaRemaining: 40, QuotaTotal: 100,
				CostPer1M: 200, MaxCostPer1M: 1000,
				P95LatencyMs: 4000, MaxLatencyMs: 10000,
				StickyAccount: "acct-1", CandidateAccount: "acct-1",
			},
			want: 0.655,
		},
		{
			name: "OPEN breaker zeroes the health factor",
			// 0.20*1 + 0.20*1 + 0.15*1 = 0.55, affinity 0 (no sticky)
			in: ScoreInputs{
				BreakerState:   "OPEN",
				QuotaRemaining: 1, QuotaTotal: 1,
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.55,
		},
		{
			name: "HALF_OPEN halves the health factor",
			// 0.25*0.5 + 0.20 + 0.20 + 0.15 = 0.675
			in: ScoreInputs{
				BreakerState:   "HALF_OPEN",
				QuotaRemaining: 1, QuotaTotal: 1,
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.675,
		},
		{
			name: "DEGRADED breaker still counts as healthy",
			// 0.25 + 0.20 + 0.20 + 0.15 = 0.80
			in: ScoreInputs{
				BreakerState:   "DEGRADED",
				QuotaRemaining: 1, QuotaTotal: 1,
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.80,
		},
		{
			name: "empty breaker state counts as healthy",
			in: ScoreInputs{
				QuotaRemaining: 1, QuotaTotal: 1,
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.80,
		},
		{
			name: "quota over total clamps to 1",
			in: ScoreInputs{
				QuotaRemaining: 150, QuotaTotal: 100,
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.80,
		},
		{
			name: "negative quota clamps to 0",
			in: ScoreInputs{
				QuotaRemaining: -5, QuotaTotal: 100,
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.60,
		},
		{
			name: "missing quota telemetry maps the quota factor to 0",
			// 0.25 (health) + 0.20 (cost) + 0.15 (latency) = 0.60
			in: ScoreInputs{
				QuotaTotal:   0,
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.60,
		},
		{
			name: "cost above pool max clamps the cost factor to 0",
			// 0.25 (health) + 0.15 (latency, p95 0 of 1000) = 0.40
			in: ScoreInputs{
				CostPer1M: 1500, MaxCostPer1M: 1000,
				MaxLatencyMs: 1000,
			},
			want: 0.40,
		},
		{
			name: "missing pool cost data makes the cost factor neutral 1",
			// 0.25 + 0.20 (cost) + 0.15 (latency) = 0.60
			in: ScoreInputs{
				MaxCostPer1M: 0,
				MaxLatencyMs: 1000,
			},
			want: 0.60,
		},
		{
			name: "missing pool latency data makes the latency factor neutral 1",
			// 0.25 + 0.20 (cost) + 0.15 (latency) = 0.60
			in: ScoreInputs{
				MaxCostPer1M: 1000,
				MaxLatencyMs: 0,
			},
			want: 0.60,
		},
		{
			name: "empty sticky account means no cache affinity for anyone",
			// 0.25 + 0.20 (cost) + 0.15 (latency) = 0.60
			in: ScoreInputs{
				StickyAccount: "", CandidateAccount: "acct-1",
				MaxCostPer1M: 1000, MaxLatencyMs: 1000,
			},
			want: 0.60,
		},
		{
			name: "NaN inputs fold to 0 instead of poisoning the score",
			// Only health (CLOSED) survives: 0.25
			in: ScoreInputs{
				BreakerState:   "CLOSED",
				QuotaRemaining: math.NaN(), QuotaTotal: 100,
				CostPer1M: math.NaN(), MaxCostPer1M: 1000,
				P95LatencyMs: math.NaN(), MaxLatencyMs: 1000,
			},
			want: 0.25,
		},
		{
			name: "completely empty input scores 0.60 (neutral cost+latency, healthy)",
			in:   ScoreInputs{},
			want: 0.60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreCandidate(tt.in)
			if math.Abs(got-tt.want) > scoreEpsilon {
				t.Errorf("ScoreCandidate() = %v, want %v", got, tt.want)
			}
			if math.IsNaN(got) {
				t.Errorf("ScoreCandidate() = NaN, want a number in [0,1]")
			}
		})
	}
}

func TestScoreCandidateWeightsSumToOne(t *testing.T) {
	// The score is only meaningful in [0,1] if the five weights sum to 1.0;
	// a future edit that re-balances one weight must rebalance the others.
	sum := weightHealth + weightQuota + weightCostInv + weightLatencyInv + weightCacheAffinity
	if math.Abs(sum-1.0) > scoreEpsilon {
		t.Errorf("factor weights sum to %v, want 1.0", sum)
	}
}

func TestScoreCandidateStaysInRange(t *testing.T) {
	// Extreme and adversarial inputs must never leave [0,1] or produce NaN.
	extremes := []float64{math.Inf(1), math.Inf(-1), math.NaN(), 1e308, -1e308}
	for _, x := range extremes {
		in := ScoreInputs{
			BreakerState:   "CLOSED",
			QuotaRemaining: x, QuotaTotal: x,
			CostPer1M: x, MaxCostPer1M: x,
			P95LatencyMs: x, MaxLatencyMs: x,
			StickyAccount: "a", CandidateAccount: "a",
		}
		got := ScoreCandidate(in)
		if math.IsNaN(got) || got < 0 || got > 1 {
			t.Errorf("ScoreCandidate(%v...) = %v, want within [0,1]", x, got)
		}
	}
}
