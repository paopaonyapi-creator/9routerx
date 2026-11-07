// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package routing

import (
	"math"
	"testing"
)

func TestQuotaWeightedPick(t *testing.T) {
	tests := []struct {
		name       string
		candidates []QuotaCandidate
		inflight   []int
		rand01     float64
		want       int
	}{
		{
			name:       "no candidates means nothing to pick",
			candidates: nil,
			rand01:     0.5,
			want:       -1,
		},
		{
			name: "tier A is preferred even when a floor-bound candidate has more quota",
			// cand 0: 12% left against floor 10 (tier A, weight 12);
			// cand 1: 90% left but floor 100 (tier B, weight 90).
			candidates: []QuotaCandidate{
				{RemainingPct: 12, FloorPct: 10},
				{RemainingPct: 90, FloorPct: 100},
			},
			rand01: 0.99,
			want:   0,
		},
		{
			name: "tier B only serves when nothing is above its floor",
			candidates: []QuotaCandidate{
				{RemainingPct: 5, FloorPct: 10}, // tier B, weight 5
				{RemainingPct: 0, FloorPct: 0},  // tier B, weight 0 (excluded)
			},
			rand01: 0.0,
			want:   0,
		},
		{
			name: "zero-weight candidates own an empty interval and are never picked",
			candidates: []QuotaCandidate{
				{RemainingPct: 0, FloorPct: 0},
				{RemainingPct: 100, FloorPct: 0},
			},
			rand01: 0.0,
			want:   1,
		},
		{
			name: "candidates with negative remaining are excluded",
			candidates: []QuotaCandidate{
				{RemainingPct: -1, FloorPct: 0},
				{RemainingPct: 40, FloorPct: 0},
			},
			rand01: 0.999,
			want:   1,
		},
		{
			name: "candidates with NaN remaining are excluded",
			candidates: []QuotaCandidate{
				{RemainingPct: math.NaN(), FloorPct: 0},
				{RemainingPct: 40, FloorPct: 0},
			},
			rand01: 0.999,
			want:   1,
		},
		{
			name: "no positive weight anywhere means nothing is eligible",
			candidates: []QuotaCandidate{
				{RemainingPct: 0, FloorPct: 0},
				{RemainingPct: -5, FloorPct: 0},
			},
			rand01: 0.5,
			want:   -1,
		},
		{
			name: "the cumulative interval is half-open",
			// Weights 75/25: rand01 0.75 maps to threshold 75, which is not
			// inside [0,75) and must fall into the second interval.
			candidates: []QuotaCandidate{
				{RemainingPct: 75, FloorPct: 0},
				{RemainingPct: 25, FloorPct: 0},
			},
			rand01: 0.75,
			want:   1,
		},
		{
			name:       "rand01 just below the boundary stays in the first interval",
			candidates: []QuotaCandidate{{RemainingPct: 75}, {RemainingPct: 25}},
			rand01:     0.749999,
			want:       0,
		},
		{
			name: "inflight pacing shifts the sample into the idle candidate",
			// Weights 50/(1+0) and 50/(1+9) = 5 of total 55.
			candidates: []QuotaCandidate{{RemainingPct: 50}, {RemainingPct: 50}},
			inflight:   []int{0, 9},
			rand01:     0.5, // threshold 27.5 < 50
			want:       0,
		},
		{
			name:       "inflight pacing lets a busy candidate lose its lead",
			candidates: []QuotaCandidate{{RemainingPct: 50}, {RemainingPct: 50}},
			inflight:   []int{0, 9},
			rand01:     0.95, // threshold 52.25, inside [50,55)
			want:       1,
		},
		{
			name:       "a shorter inflight slice counts missing entries as idle",
			candidates: []QuotaCandidate{{RemainingPct: 50}, {RemainingPct: 50}},
			inflight:   []int{9},
			rand01:     0.5, // weights 5 and 50: threshold 27.5 lands on cand 1
			want:       1,
		},
		{
			name:       "a negative inflight count counts as idle",
			candidates: []QuotaCandidate{{RemainingPct: 50}, {RemainingPct: 50}},
			inflight:   []int{-3, 0},
			rand01:     0.4,
			want:       0,
		},
		{
			name:       "rand01 at or above 1 picks the last eligible candidate",
			candidates: []QuotaCandidate{{RemainingPct: 50}, {RemainingPct: 50}},
			rand01:     1.5,
			want:       1,
		},
		{
			name:       "a NaN rand01 samples from the start",
			candidates: []QuotaCandidate{{RemainingPct: 50}, {RemainingPct: 50}},
			rand01:     math.NaN(),
			want:       0,
		},
		{
			name:       "a negative rand01 samples from the start",
			candidates: []QuotaCandidate{{RemainingPct: 50}, {RemainingPct: 50}},
			rand01:     -0.5,
			want:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := QuotaWeightedPick(tt.candidates, tt.inflight, tt.rand01)
			if got != tt.want {
				t.Errorf("QuotaWeightedPick() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestQuotaWeightedPickRespectsWeights(t *testing.T) {
	// With weights 90/10, sampling 100 fixed points across [0,1) must give
	// candidate 0 roughly 90 picks — enough to catch a weight inversion.
	candidates := []QuotaCandidate{{RemainingPct: 90}, {RemainingPct: 10}}
	picks := 0
	for i := range 100 {
		if QuotaWeightedPick(candidates, nil, (float64(i)+0.5)/100) == 0 {
			picks++
		}
	}
	if picks != 90 {
		t.Errorf("candidate 0 won %d of 100 samples, want 90", picks)
	}
}
