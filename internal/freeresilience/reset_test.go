// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import (
	"net/http"
	"testing"
	"time"
)

func TestResetForRegime_Daily(t *testing.T) {
	// 14:32 UTC on a Tuesday: the next midnight is Wednesday 00:00 UTC.
	eventAt := time.Date(2026, 11, 10, 14, 32, 0, 0, time.UTC)
	reset, ok := ResetForRegime(RegimeRecurringDaily, eventAt, nil, 1)
	if !ok {
		t.Fatal("daily regime must auto-recover")
	}
	want := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	if !reset.Equal(want) {
		t.Errorf("daily reset = %v, want %v", reset, want)
	}

	// Exactly midnight: the reset must be the NEXT midnight, not now.
	atMidnight := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	reset, _ = ResetForRegime(RegimeRecurringDaily, atMidnight, nil, 1)
	if !reset.Equal(time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("midnight event reset = %v, want next midnight", reset)
	}
}

func TestResetForRegime_Monthly(t *testing.T) {
	eventAt := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	reset, ok := ResetForRegime(RegimeRecurringMonthly, eventAt, nil, 1)
	if !ok {
		t.Fatal("monthly regime must auto-recover")
	}
	want := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !reset.Equal(want) {
		t.Errorf("monthly reset = %v, want %v", reset, want)
	}

	// December folds over the year boundary.
	dec := time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)
	reset, _ = ResetForRegime(RegimeRecurringMonthly, dec, nil, 1)
	if !reset.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("december reset = %v, want 2027-01-01", reset)
	}
}

func TestResetForRegime_KeylessBackoff(t *testing.T) {
	eventAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, w := range want {
		reset, ok := ResetForRegime(RegimeKeyless, eventAt, nil, i+1)
		if !ok {
			t.Fatalf("keyless attempt %d must auto-recover", i+1)
		}
		if got := reset.Sub(eventAt); got != w {
			t.Errorf("keyless attempt %d cooldown = %v, want %v", i+1, got, w)
		}
	}
	// Uncapped tiers share the short-cooldown family.
	reset, _ := ResetForRegime(RegimeRecurringUncapped, eventAt, nil, 1)
	if got := reset.Sub(eventAt); got != 5*time.Minute {
		t.Errorf("uncapped cooldown = %v, want 5m", got)
	}
}

func TestResetForRegime_CreditNeverAutoRecovers(t *testing.T) {
	eventAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, regime := range []string{RegimeRecurringCredit, RegimeOneTimeInitial} {
		reset, ok := ResetForRegime(regime, eventAt, nil, 1)
		if ok {
			t.Errorf("%s must not auto-recover, got reset %v", regime, reset)
		}
		if !reset.IsZero() {
			t.Errorf("%s reset should be zero, got %v", regime, reset)
		}
	}
}

func TestResetForRegime_HeaderOverride(t *testing.T) {
	eventAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	// Retry-After in seconds wins over the midnight default.
	header := http.Header{"Retry-After": []string{"120"}}
	reset, ok := ResetForRegime(RegimeRecurringDaily, eventAt, header, 1)
	if !ok || !reset.Equal(eventAt.Add(2*time.Minute)) {
		t.Errorf("retry-after 120s reset = %v (ok=%v), want %v", reset, ok, eventAt.Add(2*time.Minute))
	}

	// x-ratelimit-reset also read.
	header = http.Header{"X-RateLimit-Reset": []string{"600"}}
	reset, _ = ResetForRegime(RegimeRecurringDaily, eventAt, header, 1)
	if !reset.Equal(eventAt.Add(10*time.Minute)) {
		t.Errorf("x-ratelimit-reset 600s reset = %v, want %v", reset, eventAt.Add(10*time.Minute))
	}

	// HTTP-date spelling.
	header = http.Header{"Retry-After": []string{"Tue, 10 Nov 2026 12:05:00 GMT"}}
	reset, _ = ResetForRegime(RegimeKeyless, eventAt, header, 1)
	if !reset.Equal(eventAt.Add(5 * time.Minute)) {
		t.Errorf("http-date reset = %v, want %v", reset, eventAt.Add(5*time.Minute))
	}

	// A header cannot park a daily tier past its horizon (25h).
	header = http.Header{"Retry-After": []string{"900000"}}
	reset, _ = ResetForRegime(RegimeRecurringDaily, eventAt, header, 1)
	if got := reset.Sub(eventAt); got > 25*time.Hour {
		t.Errorf("daily horizon clamp: reset after %v, want <= 25h", got)
	}

	// A sub-minute header is lifted to the floor: the per-connection backoff
	// owns the sub-minute space.
	header = http.Header{"Retry-After": []string{"5"}}
	reset, _ = ResetForRegime(RegimeKeyless, eventAt, header, 1)
	if got := reset.Sub(eventAt); got != time.Minute {
		t.Errorf("sub-minute floor: reset after %v, want 1m", got)
	}

	// An unparseable header falls through to the regime default.
	header = http.Header{"Retry-After": []string{"soon"}}
	reset, ok = ResetForRegime(RegimeRecurringDaily, eventAt, header, 1)
	if !ok || !reset.Equal(time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("unparseable header should fall back to midnight, got %v (ok=%v)", reset, ok)
	}
}

func TestResetForRegime_UnknownRegimeFailsShort(t *testing.T) {
	eventAt := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	reset, ok := ResetForRegime("some-new-regime", eventAt, nil, 1)
	if !ok {
		t.Fatal("unknown regime must still recover (fail short)")
	}
	if got := reset.Sub(eventAt); got != 5*time.Minute {
		t.Errorf("unknown regime cooldown = %v, want 5m", got)
	}
}
