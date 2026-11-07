// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"
)

// teams_test.go covers the tenant cost centers (OmniRoute port): CRUD, the
// bitemporal billing attribution, the single-statement usage report, the UTC
// calendar budget windows (judged by an injected clock, never the host's) and
// the deduped budget notification.

// insertTeamUsage writes one ledger row with the attribution and timestamp
// the test chooses, the way the live write path stamps usageHistory from the
// key's current team. It goes through the raw handle because the production
// insert always stamps "now", which a window test cannot steer.
func insertTeamUsage(t *testing.T, repo *Repo, ts, apiKey, billingTeam, status string, prompt, completion int, cost float64) {
	t.Helper()
	_, err := repo.RawDB().Exec(
		`INSERT INTO usageHistory (timestamp, apiKey, billingTeamId, promptTokens, completionTokens, cost, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ts, apiKey, billingTeam, prompt, completion, cost, status,
	)
	if err != nil {
		t.Fatalf("insert usage row %s: %v", ts, err)
	}
}

func createTeamRow(t *testing.T, repo *Repo, id, name string, maxBudget float64, duration string) Team {
	t.Helper()
	team := Team{ID: id, Name: name, Status: TeamStatusActive, MaxBudgetUSD: maxBudget, BudgetDuration: duration}
	if err := repo.CreateTeam(team); err != nil {
		t.Fatalf("CreateTeam(%s): %v", id, err)
	}
	return team
}

func teamCount(t *testing.T, repo *Repo) int {
	t.Helper()
	var n int
	if err := repo.RawDB().QueryRow(`SELECT COUNT(*) FROM apiKeyBillingTeams`).Scan(&n); err != nil {
		t.Fatalf("count billing history: %v", err)
	}
	return n
}

func TestTeamsCRUDRoundTrip(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	createTeamRow(t, repo, "acme", "Acme", 25.5, TeamBudgetMonthly)

	got, err := repo.GetTeam("acme")
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if got.Name != "Acme" || got.Status != TeamStatusActive ||
		got.MaxBudgetUSD != 25.5 || got.BudgetDuration != TeamBudgetMonthly {
		t.Fatalf("GetTeam() = %+v, want the stored Acme row", got)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" || got.ArchivedAt != "" {
		t.Errorf("timestamps = %q/%q/%q, want created+updated set and archived empty", got.CreatedAt, got.UpdatedAt, got.ArchivedAt)
	}

	if _, err := repo.GetTeam("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetTeam(missing) err = %v, want sql.ErrNoRows", err)
	}

	// Partial update keeps the fields it does not touch.
	got.Name = "Acme Corp"
	got.MaxBudgetUSD = 30
	if err := repo.UpdateTeam(got); err != nil {
		t.Fatalf("UpdateTeam: %v", err)
	}
	after, err := repo.GetTeam("acme")
	if err != nil {
		t.Fatalf("GetTeam after update: %v", err)
	}
	if after.Name != "Acme Corp" || after.MaxBudgetUSD != 30 {
		t.Errorf("updated = %s/%.2f, want Acme Corp/30.00", after.Name, after.MaxBudgetUSD)
	}
	if after.BudgetDuration != TeamBudgetMonthly || after.Description != "" {
		t.Errorf("untouched fields drifted: duration=%q description=%q", after.BudgetDuration, after.Description)
	}
	if err := repo.UpdateTeam(Team{ID: "missing", Name: "x"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateTeam(missing) err = %v, want sql.ErrNoRows", err)
	}

	// Status filter: active lists it, archived does not, "all" lists both.
	other := createTeamRow(t, repo, "old", "Old", 0, "")
	if err := repo.ArchiveTeam(other.ID); err != nil {
		t.Fatalf("ArchiveTeam: %v", err)
	}
	archived, err := repo.GetTeam("old")
	if err != nil {
		t.Fatalf("GetTeam after archive: %v", err)
	}
	if archived.Status != TeamStatusArchived || archived.ArchivedAt == "" {
		t.Errorf("archived row = status %q archivedAt %q, want archived with a stamp", archived.Status, archived.ArchivedAt)
	}
	if err := repo.ArchiveTeam("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("ArchiveTeam(missing) err = %v, want sql.ErrNoRows", err)
	}

	active, err := repo.ListTeams(TeamStatusActive)
	if err != nil {
		t.Fatalf("ListTeams(active): %v", err)
	}
	if len(active) != 1 || active[0].ID != "acme" {
		t.Errorf("active list = %+v, want only acme", active)
	}
	all, err := repo.ListTeams("all")
	if err != nil {
		t.Fatalf("ListTeams(all): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("all list = %d rows, want 2", len(all))
	}
	// The seeded "default" plan row must not leak into the teams table.
	everything, err := repo.ListTeams("")
	if err != nil {
		t.Fatalf("ListTeams(): %v", err)
	}
	if len(everything) != 2 {
		t.Errorf("unfiltered list = %d rows, want 2", len(everything))
	}
}

// TestBillingAttributionHistory proves the bitemporal behavior: the apiKeys
// column always names the CURRENT team, while the interval history keeps the
// closed rows that let a past window report its own attribution.
func TestBillingAttributionHistory(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	createTeamRow(t, repo, "alpha", "Alpha", 0, "")
	createTeamRow(t, repo, "beta", "Beta", 0, "")
	// The attribution column lives on the apiKeys row, so the key must exist —
	// the db layer's UPDATE is a bare write, exactly like SetApiKeyPlan.
	if _, err := repo.RawDB().Exec(
		`INSERT INTO apiKeys (id, key, createdAt) VALUES ('key-1', 'sk-key-1', '2026-11-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	if err := repo.SetApiKeyBillingTeam("key-1", "alpha"); err != nil {
		t.Fatalf("SetApiKeyBillingTeam(alpha): %v", err)
	}
	if got := repo.BillingTeamIDForKey("key-1"); got != "alpha" {
		t.Fatalf("BillingTeamIDForKey = %q, want alpha", got)
	}
	// Moving the key closes alpha's interval and opens beta's.
	if err := repo.SetApiKeyBillingTeam("key-1", "beta"); err != nil {
		t.Fatalf("SetApiKeyBillingTeam(beta): %v", err)
	}
	if got := repo.BillingTeamIDForKey("key-1"); got != "beta" {
		t.Fatalf("after move BillingTeamIDForKey = %q, want beta", got)
	}

	var closedTo, openFrom, openTo string
	var openTeam string
	if err := repo.RawDB().QueryRow(
		`SELECT teamId, validTo FROM apiKeyBillingTeams WHERE apiKeyId = ? AND validTo != ''`, "key-1",
	).Scan(&openTeam, &closedTo); err != nil {
		t.Fatalf("read closed interval: %v", err)
	}
	if openTeam != "alpha" || closedTo == "" {
		t.Errorf("closed interval = (%s, validTo %q), want alpha with a stamp", openTeam, closedTo)
	}
	if err := repo.RawDB().QueryRow(
		`SELECT teamId, validFrom, validTo FROM apiKeyBillingTeams WHERE apiKeyId = ? AND validTo = ''`, "key-1",
	).Scan(&openTeam, &openFrom, &openTo); err != nil {
		t.Fatalf("read open interval: %v", err)
	}
	if openTeam != "beta" || openFrom == "" || openTo != "" {
		t.Errorf("open interval = (%s, %q, %q), want beta open", openTeam, openFrom, openTo)
	}
	if n := teamCount(t, repo); n != 2 {
		t.Errorf("history rows = %d, want 2 (one closed, one open)", n)
	}

	// Clearing the assignment closes beta's interval and empties the column.
	if err := repo.SetApiKeyBillingTeam("key-1", ""); err != nil {
		t.Fatalf("SetApiKeyBillingTeam(clear): %v", err)
	}
	if got := repo.BillingTeamIDForKey("key-1"); got != "" {
		t.Errorf("after clear BillingTeamIDForKey = %q, want empty", got)
	}
	if n := teamCount(t, repo); n != 2 {
		t.Errorf("history rows after clear = %d, want 2 (a clear opens no interval)", n)
	}
	// Unknown and unassigned keys read as unattributed, not as errors.
	if got := repo.BillingTeamIDForKey("no-such-key"); got != "" {
		t.Errorf("unknown key attribution = %q, want empty", got)
	}
}

// TestTeamUsageAttributionFollowsTheKey is the bitemporal contract end to
// end: a key moves from team A to team B, and the report for the window
// covering the team-A period still shows team A's usage, because the ledger
// stamped the team at request time.
func TestTeamUsageAttributionFollowsTheKey(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	createTeamRow(t, repo, "alpha", "Alpha", 0, "")
	createTeamRow(t, repo, "beta", "Beta", 0, "")

	if err := repo.SetApiKeyBillingTeam("key-1", "alpha"); err != nil {
		t.Fatalf("assign alpha: %v", err)
	}
	insertTeamUsage(t, repo, "2026-11-01T10:00:00Z", "masked-key-1", "alpha", "success", 100, 50, 0.02)
	if err := repo.SetApiKeyBillingTeam("key-1", "beta"); err != nil {
		t.Fatalf("move to beta: %v", err)
	}
	insertTeamUsage(t, repo, "2026-11-02T10:00:00Z", "masked-key-1", "beta", "success", 200, 100, 0.04)

	ctx := context.Background()
	alphaReport, err := repo.TeamUsageReport(ctx, "alpha", "2026-11-01T00:00:00Z", "2026-11-01T23:59:59Z")
	if err != nil {
		t.Fatalf("alpha report: %v", err)
	}
	if alphaReport.Summary.Requests != 1 || alphaReport.Summary.EstimatedCostUsd != 0.02 {
		t.Errorf("alpha summary = %+v, want the request earned under alpha", alphaReport.Summary)
	}
	betaReport, err := repo.TeamUsageReport(ctx, "beta", "2026-11-01T00:00:00Z", "2026-11-02T23:59:59Z")
	if err != nil {
		t.Fatalf("beta report: %v", err)
	}
	if betaReport.Summary.Requests != 1 || betaReport.Summary.InputTokens != 200 {
		t.Errorf("beta summary = %+v, want only the request earned under beta", betaReport.Summary)
	}
}

func TestTeamUsageReportAggregation(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	createTeamRow(t, repo, "acme", "Acme", 0, "")

	insertTeamUsage(t, repo, "2026-11-03T10:00:00Z", "ak-one", "acme", "success", 100, 50, 0.10)
	insertTeamUsage(t, repo, "2026-11-03T11:00:00Z", "ak-one", "acme", "success", 10, 5, 0.01)
	insertTeamUsage(t, repo, "2026-11-03T12:00:00Z", "ak-one", "acme", "error", 8, 4, 0.0)
	insertTeamUsage(t, repo, "2026-11-03T13:00:00Z", "ak-two", "acme", "success", 40, 20, 0.05)
	// Not the team's: another team's row, and one outside the window.
	insertTeamUsage(t, repo, "2026-11-03T14:00:00Z", "ak-other", "other", "success", 999, 999, 9.99)
	insertTeamUsage(t, repo, "2026-11-01T09:00:00Z", "ak-one", "acme", "success", 500, 500, 1.00)

	report, err := repo.TeamUsageReport(context.Background(), "acme", "2026-11-03T00:00:00Z", "2026-11-03T23:59:59Z")
	if err != nil {
		t.Fatalf("TeamUsageReport: %v", err)
	}
	s := report.Summary
	if s.Requests != 4 || s.SuccessfulRequests != 3 {
		t.Errorf("requests = %d/%d successful, want 4/3", s.Requests, s.SuccessfulRequests)
	}
	if s.InputTokens != 158 || s.OutputTokens != 79 {
		t.Errorf("tokens = %d/%d, want 158/79", s.InputTokens, s.OutputTokens)
	}
	if math.Abs(s.EstimatedCostUsd-0.16) > 1e-9 {
		t.Errorf("cost = %.10f, want 0.16", s.EstimatedCostUsd)
	}
	if len(report.ByAPIKey) != 2 {
		t.Fatalf("byApiKey = %d rows, want 2", len(report.ByAPIKey))
	}
	byKey := map[string]TeamUsagePerKey{}
	for _, row := range report.ByAPIKey {
		byKey[row.APIKey] = row
	}
	one, ok := byKey["ak-one"]
	if !ok || one.Requests != 3 || math.Abs(one.Cost-0.11) > 1e-9 {
		t.Errorf("ak-one row = %+v, want 3 requests at 0.11", one)
	}
	two, ok := byKey["ak-two"]
	if !ok || two.Requests != 1 || math.Abs(two.Cost-0.05) > 1e-9 {
		t.Errorf("ak-two row = %+v, want 1 request at 0.05", two)
	}

	// A team with no rows reports an empty summary, not an error.
	empty, err := repo.TeamUsageReport(context.Background(), "acme", "2020-01-01T00:00:00Z", "2020-01-02T00:00:00Z")
	if err != nil {
		t.Fatalf("empty window report: %v", err)
	}
	if empty.Summary.Requests != 0 || len(empty.ByAPIKey) != 0 {
		t.Errorf("empty window = %+v, want zeroed report", empty)
	}
}

func TestTeamSpendInWindow(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	createTeamRow(t, repo, "acme", "Acme", 0, "")
	insertTeamUsage(t, repo, "2026-11-04T23:59:59Z", "ak", "acme", "success", 1, 1, 0.75)
	insertTeamUsage(t, repo, "2026-11-05T00:00:00Z", "ak", "acme", "success", 1, 1, 0.25)
	insertTeamUsage(t, repo, "2026-11-05T12:00:00Z", "ak", "acme", "success", 1, 1, 0.50)

	spend, err := repo.TeamSpendInWindow(context.Background(), "acme", "2026-11-05T00:00:00Z")
	if err != nil {
		t.Fatalf("TeamSpendInWindow: %v", err)
	}
	if math.Abs(spend-0.75) > 1e-9 {
		t.Errorf("spend = %.4f, want 0.75 (window start inclusive)", spend)
	}
}

// TestBudgetWindowStart pins the UTC calendar anchoring, including a clock in
// a +10:00 zone whose local date is ahead of the UTC date the window must
// follow.
func TestBudgetWindowStart(t *testing.T) {
	utc := func(y int, mo time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
	}
	eastTen := time.FixedZone("UTC+10", 10*3600)
	cases := []struct {
		name     string
		duration string
		now      time.Time
		want     time.Time
	}{
		{"daily truncates to UTC midnight", TeamBudgetDaily, utc(2026, 11, 5, 10, 15, 30), utc(2026, 11, 5, 0, 0, 0)},
		{"weekly opens the Monday", TeamBudgetWeekly, utc(2026, 11, 5, 10, 0, 0), utc(2026, 11, 2, 0, 0, 0)},
		{"a Sunday belongs to the week before", TeamBudgetWeekly, utc(2026, 11, 1, 23, 0, 0), utc(2026, 10, 26, 0, 0, 0)},
		{"monthly opens the first", TeamBudgetMonthly, utc(2026, 11, 5, 0, 0, 0), utc(2026, 11, 1, 0, 0, 0)},
		{"the first of the month is its own boundary", TeamBudgetMonthly, utc(2026, 11, 1, 0, 0, 1), utc(2026, 11, 1, 0, 0, 0)},
		{"local date ahead of UTC follows the UTC date", TeamBudgetDaily,
			time.Date(2026, 11, 5, 2, 0, 0, 0, eastTen), utc(2026, 11, 4, 0, 0, 0)},
		{"unknown duration has no window", "7h", utc(2026, 11, 5, 0, 0, 0), time.Time{}},
		{"empty duration has no window", "", utc(2026, 11, 5, 0, 0, 0), time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BudgetWindowStart(tc.duration, tc.now)
			if !got.Equal(tc.want) {
				t.Errorf("BudgetWindowStart(%q, %s) = %s, want %s", tc.duration, tc.now, got, tc.want)
			}
		})
	}
}

// TestCheckTeamBudgetNotifyDedupe proves the breach raises the notification
// with the window-named dedupe key and that a repeat inside the same window
// refreshes rather than duplicates it.
func TestCheckTeamBudgetNotifyDedupe(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	createTeamRow(t, repo, "acme", "Acme", 1.0, TeamBudgetDaily)
	createTeamRow(t, repo, "no-cap", "NoCap", 0, "")
	createTeamRow(t, repo, "odd", "Odd", 1.0, "7h")

	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// No spend, no breach; a team without a budget and one with an unknown
	// duration are never breached either.
	for _, teamID := range []string{"acme", "no-cap", "odd"} {
		breached, err := repo.CheckTeamBudget(ctx, teamID, now)
		if err != nil || breached {
			t.Fatalf("CheckTeamBudget(%s) before spend = %v, %v; want false, nil", teamID, breached, err)
		}
	}

	insertTeamUsage(t, repo, "2026-11-05T08:00:00Z", "ak", "acme", "success", 10, 10, 1.5)
	breached, err := repo.CheckTeamBudget(ctx, "acme", now)
	if err != nil {
		t.Fatalf("CheckTeamBudget after breach: %v", err)
	}
	if !breached {
		t.Fatal("CheckTeamBudget = false, want breach at 1.5 >= 1.0")
	}

	countNotifs := func() int {
		t.Helper()
		var n int
		if err := repo.RawDB().QueryRow(
			`SELECT COUNT(*) FROM notifications WHERE dedupeKey LIKE 'team_budget:%'`,
		).Scan(&n); err != nil {
			t.Fatalf("count budget notifications: %v", err)
		}
		return n
	}
	if n := countNotifs(); n != 1 {
		t.Fatalf("budget notifications = %d, want 1", n)
	}

	// The same window dedupes; a later call refreshes the one notification.
	if _, err := repo.CheckTeamBudget(ctx, "acme", now); err != nil {
		t.Fatalf("second CheckTeamBudget: %v", err)
	}
	if n := countNotifs(); n != 1 {
		t.Errorf("budget notifications after repeat = %d, want still 1", n)
	}

	var dedupeKey, severity string
	if err := repo.RawDB().QueryRow(
		`SELECT dedupeKey, severity FROM notifications WHERE dedupeKey LIKE 'team_budget:%'`,
	).Scan(&dedupeKey, &severity); err != nil {
		t.Fatalf("read budget notification: %v", err)
	}
	wantKey := fmt.Sprintf("team_budget:acme:%s", BudgetWindowStart(TeamBudgetDaily, now).UTC().Format(time.RFC3339))
	if dedupeKey != wantKey {
		t.Errorf("dedupeKey = %q, want %q", dedupeKey, wantKey)
	}
	if severity != "warn" {
		t.Errorf("severity = %q, want warn", severity)
	}
}

// TestInsertUsageHistoryStampsBillingTeam pins the write half of the tenant
// cost centers: the live insert copies the key's current team into the ledger
// row, and the report reads exactly that column back. Every path here used to
// exist except the stamp — TeamUsageReport and TeamSpendInWindow filtered a
// column nothing wrote, so every team's spend read as zero and the budget
// check could never fire. The stamp is best-effort by contract: a row without
// one (unknown key, no team) is unattributed, never lost.
func TestInsertUsageHistoryStampsBillingTeam(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	createTeamRow(t, r, "acme", "Acme", 0, "")
	if err := r.CreateApiKey("k1", "sk-team-attributed", "attributed", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := r.SetApiKeyBillingTeam("k1", "acme"); err != nil {
		t.Fatalf("SetApiKeyBillingTeam: %v", err)
	}
	team := r.BillingTeamIDForKey("k1")
	if team != "acme" {
		t.Fatalf("BillingTeamIDForKey = %q, want acme", team)
	}

	// The live write shape: masked client key, then the team the key belongs
	// to at insert time.
	if err := r.InsertUsageHistory("prov", "m1", "conn-1", "masked-upstream", "/v1/chat",
		10, 20, 0.50, "success", 30, "{}", "{}", "sk-tea***tributed", team); err != nil {
		t.Fatalf("InsertUsageHistory with team: %v", err)
	}
	// A write with no attribution (the media path's shape) stays unattributed.
	if err := r.InsertUsageHistory("prov", "m1", "conn-2", "masked-upstream", "/v1/chat",
		10, 20, 0.50, "success", 30, "{}", "{}"); err != nil {
		t.Fatalf("InsertUsageHistory without team: %v", err)
	}

	now := time.Now().UTC()
	report, err := r.TeamUsageReport(context.Background(), "acme",
		now.Add(-time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("TeamUsageReport: %v", err)
	}
	if report.Summary.Requests != 1 {
		t.Errorf("report requests = %d, want 1 (the unattributed row must not count)", report.Summary.Requests)
	}
	if report.Summary.EstimatedCostUsd != 0.50 {
		t.Errorf("report cost = %v, want 0.50", report.Summary.EstimatedCostUsd)
	}

	spend, err := r.TeamSpendInWindow(context.Background(), "acme",
		now.Add(-time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("TeamSpendInWindow: %v", err)
	}
	if spend != 0.50 {
		t.Errorf("team spend = %v, want 0.50 (the budget check reads the same column)", spend)
	}
}
