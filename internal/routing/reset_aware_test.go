// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package routing

import (
	"math"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// scoreEpsilon is reused from scoring_test.go for float comparisons.

func TestSessionWeeklyScore(t *testing.T) {
	tests := []struct {
		name             string
		sessionRemaining float64
		weeklyRemaining  float64
		sessionResetIn   time.Duration
		weeklyResetIn    time.Duration
		want             float64
	}{
		{
			name: "full windows with a full window until reset",
			// session: 0.45*1 + 0.55*0 = 0.45; weekly: 0.25*1 + 0.75*0 = 0.25
			// score: 0.35*0.45 + 0.65*0.25 = 0.32
			sessionRemaining: 1, weeklyRemaining: 1,
			sessionResetIn: 5 * time.Hour, weeklyResetIn: 7 * 24 * time.Hour,
			want: 0.32,
		},
		{
			name: "spent windows that already passed score low but not zero",
			// pressure 1 both: raw 0.35*0.55 + 0.65*0.75 = 0.68, then the
			// exhaustion guard (session 0 < 0.10) scales by 0.05.
			sessionRemaining: 0, weeklyRemaining: 0,
			want: 0.034,
		},
		{
			name: "nearly spent session window is scaled down linearly",
			// session: 0.45*0.05 + 0.55*0.5 = 0.2975; weekly 0.25
			// raw: 0.35*0.2975 + 0.65*0.25 = 0.266625; guard factor 0.5
			sessionRemaining: 0.05, weeklyRemaining: 1,
			sessionResetIn: 150 * time.Minute, // half the 5h window: pressure 0.5
			weeklyResetIn:  7 * 24 * time.Hour,
			want:           0.1333125,
		},
		{
			name: "windows that just reset score 1",
			// resetIn 0 is "past": pressure 1 for both windows.
			sessionRemaining: 1, weeklyRemaining: 1,
			want: 1,
		},
		{
			name: "a reset beyond the window is pressure 0, same as a full window",
			// session reset in 10h of a 5h window: pressure clamp01(1-2) = 0
			sessionRemaining: 1, weeklyRemaining: 1,
			sessionResetIn: 10 * time.Hour, weeklyResetIn: 14 * 24 * time.Hour,
			want: 0.32,
		},
		{
			name: "exactly 10 percent session remaining does not trigger the guard",
			// session: 0.45*0.10 + 0.55*1 = 0.595; weekly: 0.25*1 + 0.75*1 = 1
			// raw: 0.35*0.595 + 0.65*1 = 0.85825
			sessionRemaining: 0.10, weeklyRemaining: 1,
			want: 0.85825,
		},
		{
			name: "nine percent session remaining scales the score by 0.9",
			// session: 0.45*0.09 + 0.55*1 = 0.5905; weekly: 0.25*1 + 0.75*1 = 1
			// raw: 0.35*0.5905 + 0.65*1 = 0.856675; guard factor 0.09/0.10
			sessionRemaining: 0.09, weeklyRemaining: 1,
			want: 0.7710075,
		},
		{
			name: "negative remaining clamps to 0 and the guard floor applies",
			// session: 0.55 (pressure 1); weekly: 1.0; raw 0.35*0.55 + 0.65 = 0.8425
			// guard factor: max(0.05, -1/0.10) = 0.05
			sessionRemaining: -1, weeklyRemaining: 1,
			want: 0.042125,
		},
		{
			name:             "remaining above 1 clamps to 1",
			sessionRemaining: 5, weeklyRemaining: 5,
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SessionWeeklyScore(tt.sessionRemaining, tt.weeklyRemaining, tt.sessionResetIn, tt.weeklyResetIn, fixedNow)
			if math.Abs(got-tt.want) > scoreEpsilon {
				t.Errorf("SessionWeeklyScore() = %v, want %v", got, tt.want)
			}
			if math.IsNaN(got) || got < 0 || got > 1 {
				t.Errorf("SessionWeeklyScore() = %v, want within [0,1]", got)
			}
		})
	}
}

func TestSessionWeeklyScoreIgnoresNow(t *testing.T) {
	// The score is relative to the reset durations, so pinning a different
	// evaluation instant must not change it.
	a := SessionWeeklyScore(0.5, 0.5, 2*time.Hour, 3*24*time.Hour, fixedNow)
	b := SessionWeeklyScore(0.5, 0.5, 2*time.Hour, 3*24*time.Hour, fixedNow.Add(48*time.Hour))
	if math.Abs(a-b) > scoreEpsilon {
		t.Errorf("score moved with now: %v vs %v", a, b)
	}
}

func TestOrderResetAware(t *testing.T) {
	tests := []struct {
		name       string
		candidates []ResetAwareCandidate
		want       []int
	}{
		{
			name:       "no candidates order nothing",
			candidates: nil,
			want:       []int{},
		},
		{
			name: "distinct scores order best first",
			// 0: full windows just reset -> 1.0
			// 1: half-spent windows just reset -> 0.35*0.775 + 0.65*0.875 = 0.84
			// 2: fully spent, guard-floored -> 0.034
			candidates: []ResetAwareCandidate{
				{SessionRemaining: 1, WeeklyRemaining: 1},
				{SessionRemaining: 0.5, WeeklyRemaining: 0.5},
				{SessionRemaining: 0, WeeklyRemaining: 0},
			},
			want: []int{0, 1, 2},
		},
		{
			name: "a soon-resetting window beats a larger but distant quota",
			// 0: 40% session remaining resetting in 1h of 5h:
			//    session 0.45*0.4 + 0.55*0.8 = 0.62; weekly full just reset 1
			//    score 0.35*0.62 + 0.65*1 = 0.867
			// 1: 90% session remaining resetting far out (pressure 0):
			//    session 0.45*0.9 = 0.405; weekly 0.25
			//    score 0.35*0.405 + 0.65*0.25 = 0.304250
			candidates: []ResetAwareCandidate{
				{SessionRemaining: 0.4, WeeklyRemaining: 1, SessionResetIn: time.Hour},
				{SessionRemaining: 0.9, WeeklyRemaining: 1, SessionResetIn: 5 * time.Hour, WeeklyResetIn: 7 * 24 * time.Hour},
			},
			want: []int{0, 1},
		},
		{
			name: "the input slice is not modified",
			candidates: []ResetAwareCandidate{
				{SessionRemaining: 0, WeeklyRemaining: 0},
				{SessionRemaining: 1, WeeklyRemaining: 1},
			},
			want: []int{1, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pin the rotation counter so tie-band rotation cannot reorder a
			// table case that has no tie band.
			tieRotation.Store(0)
			original := append([]ResetAwareCandidate(nil), tt.candidates...)
			got := OrderResetAware(tt.candidates, fixedNow)
			if len(got) != len(tt.want) {
				t.Fatalf("OrderResetAware() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("OrderResetAware() = %v, want %v", got, tt.want)
					break
				}
			}
			for i := range original {
				if original[i] != tt.candidates[i] {
					t.Fatalf("OrderResetAware modified the input slice")
				}
			}
		})
	}
}

func TestOrderResetAwareRotatesLeadingTieBand(t *testing.T) {
	// Candidates 0 and 1 score identically (1.0); candidate 2 is far below the
	// 5% band. The band must rotate its leader on every call while the
	// low scorer stays last.
	tieRotation.Store(0)
	candidates := []ResetAwareCandidate{
		{SessionRemaining: 1, WeeklyRemaining: 1},
		{SessionRemaining: 1, WeeklyRemaining: 1},
		{SessionRemaining: 0, WeeklyRemaining: 0},
	}

	first := OrderResetAware(candidates, fixedNow)
	if first[2] != 2 {
		t.Fatalf("OrderResetAware() = %v, want the low scorer last", first)
	}
	second := OrderResetAware(candidates, fixedNow)
	if second[2] != 2 {
		t.Fatalf("OrderResetAware() = %v, want the low scorer last", second)
	}
	if first[0] == second[0] {
		t.Errorf("tied leaders did not rotate: lead was %d on both calls", first[0])
	}
	if first[0]+first[1] != 1 || second[0]+second[1] != 1 {
		t.Errorf("band rotation lost a member: %v then %v", first, second)
	}
}

func TestOrderResetAwareTieBandBoundary(t *testing.T) {
	// A candidate within 5% of the leader joins the band; one just outside
	// does not. Leader scores 1.0; candidate 1 scores 0.9615 (inside);
	// candidate 2 scores 0.75625 (outside).
	inside := ResetAwareCandidate{SessionRemaining: 1, SessionResetIn: time.Hour, WeeklyRemaining: 1}
	outside := ResetAwareCandidate{SessionRemaining: 1, SessionResetIn: 5 * time.Hour, WeeklyRemaining: 1, WeeklyResetIn: 7 * 24 * time.Hour}
	if s := SessionWeeklyScore(inside.SessionRemaining, inside.WeeklyRemaining, inside.SessionResetIn, inside.WeeklyResetIn, fixedNow); s < 0.95 {
		t.Fatalf("test setup: inside candidate scores %v, want >= 0.95", s)
	}
	if s := SessionWeeklyScore(outside.SessionRemaining, outside.WeeklyRemaining, outside.SessionResetIn, outside.WeeklyResetIn, fixedNow); s >= 0.95 {
		t.Fatalf("test setup: outside candidate scores %v, want < 0.95", s)
	}

	tieRotation.Store(0)
	candidates := []ResetAwareCandidate{
		{SessionRemaining: 1, WeeklyRemaining: 1}, // leader 1.0
		inside,
		outside,
	}
	first := OrderResetAware(candidates, fixedNow)
	second := OrderResetAware(candidates, fixedNow)
	if first[2] != 2 || second[2] != 2 {
		t.Errorf("outside-band candidate moved: %v then %v", first, second)
	}
	// The band is {0, 1}: consecutive calls must alternate the leader between
	// them and never pull the outside candidate forward.
	if first[0] == second[0] {
		t.Errorf("inside-band candidates did not rotate: %v then %v", first, second)
	}
}
