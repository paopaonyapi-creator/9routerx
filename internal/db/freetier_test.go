// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"testing"
	"time"
)

// TestFreeTierBudget_RoundTrip pins the setter/getter/increment/reset cycle on
// one provider row.
func TestFreeTierBudget_RoundTrip(t *testing.T) {
	repo := openEphemeralSchemaDB(t)

	if err := repo.SetFreeTierBudget("groq", 120_000_000, 1); err != nil {
		t.Fatalf("SetFreeTierBudget: %v", err)
	}
	budgets, err := repo.GetFreeTierBudgets()
	if err != nil {
		t.Fatalf("GetFreeTierBudgets: %v", err)
	}
	if len(budgets) != 1 {
		t.Fatalf("budget count = %d, want 1", len(budgets))
	}
	b := budgets[0]
	if b.Provider != "groq" || b.MonthlyQuota != 120_000_000 || b.ResetDay != 1 || b.UsedThisPeriod != 0 {
		t.Errorf("row = %+v", b)
	}

	// SetFreeTierBudget upserts: a second call rewrites the policy columns and
	// must not resurrect a spent counter.
	if err := repo.IncrementFreeTierUsage("groq", 500); err != nil {
		t.Fatalf("IncrementFreeTierUsage: %v", err)
	}
	if err := repo.SetFreeTierBudget("groq", 200_000_000, 5); err != nil {
		t.Fatalf("SetFreeTierBudget (update): %v", err)
	}
	budgets, _ = repo.GetFreeTierBudgets()
	if budgets[0].MonthlyQuota != 200_000_000 || budgets[0].ResetDay != 5 {
		t.Errorf("upsert did not rewrite the policy columns: %+v", budgets[0])
	}
	if budgets[0].UsedThisPeriod != 500 {
		t.Errorf("upsert reset the counter: %+v", budgets[0])
	}

	// Usage accumulates; zero and negative bookings are no-ops.
	if err := repo.IncrementFreeTierUsage("groq", 700); err != nil {
		t.Fatalf("IncrementFreeTierUsage (2): %v", err)
	}
	if err := repo.IncrementFreeTierUsage("groq", 0); err != nil {
		t.Fatalf("IncrementFreeTierUsage (zero): %v", err)
	}
	if err := repo.IncrementFreeTierUsage("groq", -5); err != nil {
		t.Fatalf("IncrementFreeTierUsage (negative): %v", err)
	}
	budgets, _ = repo.GetFreeTierBudgets()
	if budgets[0].UsedThisPeriod != 1200 {
		t.Errorf("used = %d, want 1200", budgets[0].UsedThisPeriod)
	}
	if budgets[0].LastUpdated == "" {
		t.Error("increment left lastUpdated empty")
	}

	// An unknown provider is a no-op, never an error: the usage writer calls
	// this after every ledger insert.
	if err := repo.IncrementFreeTierUsage("untracked", 1000); err != nil {
		t.Errorf("increment on untracked provider: %v", err)
	}

	// The explicit reset zeroes the counter.
	if err := repo.ResetFreeTierPeriod("groq"); err != nil {
		t.Fatalf("ResetFreeTierPeriod: %v", err)
	}
	budgets, _ = repo.GetFreeTierBudgets()
	if budgets[0].UsedThisPeriod != 0 {
		t.Errorf("used after reset = %d, want 0", budgets[0].UsedThisPeriod)
	}
}

// TestResetFreeTierPeriodsDue exercises the rollover with an injected clock:
// a stale period resets, the current one does not, and the pass is idempotent.
func TestResetFreeTierPeriodsDue(t *testing.T) {
	repo := openEphemeralSchemaDB(t)

	// Provider A: reset day 1, stamped inside the previous period.
	// Provider B: reset day 1, stamped in the current period.
	// Provider C: reset day 0 (one-off credit) — never rolls.
	// Provider D: reset day 31 on a 30-day month — rolls on the last day.
	if err := repo.SetFreeTierBudget("a", 1000, 1); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := repo.SetFreeTierBudget("b", 1000, 1); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if err := repo.SetFreeTierBudget("c", 1000, 0); err != nil {
		t.Fatalf("seed c: %v", err)
	}
	if err := repo.SetFreeTierBudget("d", 1000, 31); err != nil {
		t.Fatalf("seed d: %v", err)
	}
	stamp := func(provider, ts string) {
		t.Helper()
		if _, err := repo.db.Exec(
			`UPDATE freeTierBudget SET usedThisPeriod = 900, lastUpdated = ? WHERE provider = ?`, ts, provider,
		); err != nil {
			t.Fatalf("stamp %s: %v", provider, err)
		}
	}
	stamp("a", "2026-03-15T12:00:00Z") // before the April 1 boundary
	stamp("b", "2026-04-02T08:00:00Z") // inside the April period
	stamp("c", "2026-03-15T12:00:00Z")
	stamp("d", "2026-04-15T12:00:00Z") // inside April, which has no 31st

	// now = April 20: only "a" is due. "d"'s period opened April 30 of March —
	// i.e. March 31 — and its April 15 stamp belongs to that period, so it is
	// not due either.
	now := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	reset, err := repo.ResetFreeTierPeriodsDue(now)
	if err != nil {
		t.Fatalf("ResetFreeTierPeriodsDue: %v", err)
	}
	if len(reset) != 1 || reset[0] != "a" {
		t.Errorf("reset = %v, want [a]", reset)
	}
	budgets, _ := repo.GetFreeTierBudgets()
	used := map[string]int64{}
	for _, b := range budgets {
		used[b.Provider] = b.UsedThisPeriod
	}
	if used["a"] != 0 {
		t.Errorf("stale provider not reset: used = %d", used["a"])
	}
	if used["b"] != 900 || used["c"] != 900 || used["d"] != 900 {
		t.Errorf("current-period or credit rows were reset: %v", used)
	}

	// Idempotent within the same period: a second pass resets nothing.
	reset, err = repo.ResetFreeTierPeriodsDue(now)
	if err != nil || len(reset) != 0 {
		t.Errorf("second pass reset %v (err %v), want none", reset, err)
	}

	// A month-boundary tick rolls every monthly provider whose period opened
	// before May 1. a and b were re-stamped by the April pass (at the injected
	// clock, April 20), d's April 15 stamp predates its clamped April 30
	// boundary, and c never rolls.
	later := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	reset, err = repo.ResetFreeTierPeriodsDue(later)
	if err != nil {
		t.Fatalf("ResetFreeTierPeriodsDue (May): %v", err)
	}
	if len(reset) != 3 { // a, b, d; c has resetDay 0
		t.Errorf("May pass reset %v, want [a b d]", reset)
	}
}

// TestFreeTierPeriodStart clamps the boundary to the month's length: resetDay
// 31 lands on April 30, and February folds a 31 into the 28th/29th.
func TestFreeTierPeriodStart(t *testing.T) {
	cases := []struct {
		name     string
		now      time.Time
		resetDay int
		want     string
	}{
		{
			name:     "mid-month keeps the current month",
			now:      time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
			resetDay: 1,
			want:     "2026-04-01",
		},
		{
			name:     "boundary day itself starts the period",
			now:      time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			resetDay: 1,
			want:     "2026-04-01",
		},
		{
			name:     "before the boundary rolls to the previous month",
			now:      time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
			resetDay: 25,
			want:     "2026-03-25",
		},
		{
			name:     "resetDay beyond the month clamps to the last day",
			now:      time.Date(2026, 4, 30, 23, 0, 0, 0, time.UTC),
			resetDay: 31,
			want:     "2026-04-30",
		},
		{
			name:     "february folds a 31 into the month end",
			now:      time.Date(2026, 2, 28, 23, 0, 0, 0, time.UTC),
			resetDay: 31,
			want:     "2026-02-28",
		},
		{
			name:     "january rolls back to december",
			now:      time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC),
			resetDay: 15,
			want:     "2025-12-15",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := freeTierPeriodStart(tc.now, tc.resetDay)
			if got.Format("2006-01-02") != tc.want {
				t.Errorf("freeTierPeriodStart = %s, want %s", got.Format("2006-01-02"), tc.want)
			}
		})
	}
}

// TestSeedFreeTierCatalogBudgets pins the boot-time seeding: it creates the
// rows tracking needs and never clobbers an operator override.
func TestSeedFreeTierCatalogBudgets(t *testing.T) {
	repo := openEphemeralSchemaDB(t)

	// An operator raised groq's quota before this boot ran.
	if err := repo.SetFreeTierBudget("groq", 999, 7); err != nil {
		t.Fatalf("operator override: %v", err)
	}
	if err := repo.SeedFreeTierCatalogBudgets(); err != nil {
		t.Fatalf("SeedFreeTierCatalogBudgets: %v", err)
	}
	budgets, err := repo.GetFreeTierBudgets()
	if err != nil {
		t.Fatalf("GetFreeTierBudgets: %v", err)
	}
	byProvider := map[string]FreeTierBudget{}
	for _, b := range budgets {
		byProvider[b.Provider] = b
	}
	if len(budgets) < 10 {
		t.Errorf("seed created %d rows, want at least the 11 catalog entries minus none", len(budgets))
	}
	if byProvider["groq"].MonthlyQuota != 999 || byProvider["groq"].ResetDay != 7 {
		t.Errorf("seed clobbered the operator override: %+v", byProvider["groq"])
	}
	if _, ok := byProvider["gemini"]; !ok {
		t.Error("seed did not create the gemini row")
	}
}
