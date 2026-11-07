// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"fmt"
	"time"
)

// Team budget reads and the budget-check gate, split from teams.go so the
// CRUD surface and the spend accounting stay under the file-size target.
// The window math is anchored on UTC calendar boundaries (BudgetWindowStart),
// not rolling windows, so an operator reading the dashboard and this check
// agree on what "this window" means without sharing a clock.

// TeamUsageSummary is the total block of a cost-center report.
type TeamUsageSummary struct {
	Requests           int     `json:"requests"`
	SuccessfulRequests int     `json:"successfulRequests"`
	InputTokens        int64   `json:"inputTokens"`
	OutputTokens       int64   `json:"outputTokens"`
	EstimatedCostUsd   float64 `json:"estimatedCostUsd"`
}

// TeamUsagePerKey is one key's slice of the window.
type TeamUsagePerKey struct {
	APIKey   string  `json:"apiKey"`
	Requests int     `json:"requests"`
	Cost     float64 `json:"cost"`
}

// TeamUsageReport is a cost center's spend over [startISO, endISO].
type TeamUsageReport struct {
	Summary  TeamUsageSummary  `json:"summary"`
	ByAPIKey []TeamUsagePerKey `json:"byApiKey"`
}

// TeamUsageReport aggregates the window in ONE grouped statement and folds
// the summary out of the per-key rows, the same fold GetPlansForKeys' callers
// use: a team's total is by definition the sum of its keys' totals, so a
// second GROUP BY pass over the same rows would buy nothing. The read is
// answered from the row's billingTeamId, which the write path stamps at
// request time — re-pointing a key after the fact never rewrites it.
func (r *Repo) TeamUsageReport(ctx context.Context, teamID, startISO, endISO string) (TeamUsageReport, error) {
	report := TeamUsageReport{ByAPIKey: []TeamUsagePerKey{}}
	if r == nil || r.db == nil || teamID == "" {
		return report, fmt.Errorf("teams: usage report: nil repo or empty team id")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT COALESCE(apiKey, ''), COUNT(*),
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(promptTokens), 0), COALESCE(SUM(completionTokens), 0),
		        COALESCE(SUM(cost), 0)
		   FROM usageHistory
		  WHERE billingTeamId = ? AND timestamp >= ? AND timestamp <= ?
		  GROUP BY COALESCE(apiKey, '')`,
		teamID, startISO, endISO,
	)
	if err != nil {
		return report, fmt.Errorf("teams: usage report %s: %w", teamID, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var row TeamUsagePerKey
		var successful, prompt, completion int64
		if err := rows.Scan(&row.APIKey, &row.Requests, &successful,
			&prompt, &completion, &row.Cost); err != nil {
			return report, fmt.Errorf("teams: usage report %s scan: %w", teamID, err)
		}
		report.Summary.Requests += row.Requests
		report.Summary.SuccessfulRequests += int(successful)
		report.Summary.InputTokens += prompt
		report.Summary.OutputTokens += completion
		report.Summary.EstimatedCostUsd += row.Cost
		report.ByAPIKey = append(report.ByAPIKey, row)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("teams: usage report %s iterate: %w", teamID, err)
	}
	return report, nil
}

// TeamSpendInWindow sums the cost a team has accrued since windowStartISO.
// The budget checker calls it once per team per check, never per request.
func (r *Repo) TeamSpendInWindow(ctx context.Context, teamID, windowStartISO string) (float64, error) {
	if r == nil || r.db == nil || teamID == "" {
		return 0, fmt.Errorf("teams: spend: nil repo or empty team id")
	}
	var spend float64
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost), 0) FROM usageHistory
		  WHERE billingTeamId = ? AND timestamp >= ?`,
		teamID, windowStartISO,
	).Scan(&spend)
	if err != nil {
		return 0, fmt.Errorf("teams: spend %s since %s: %w", teamID, windowStartISO, err)
	}
	return spend, nil
}

// BudgetWindowStart anchors a budget duration on its UTC calendar boundary:
// the day for 1d, the Monday-starting week for 7d, the first of the month for
// 30d. now is injected so callers (and tests) judge the window from the clock
// they hold. An unknown duration returns the zero Time, which callers treat
// as "no window" rather than guessing one.
func BudgetWindowStart(duration string, now time.Time) time.Time {
	utc := now.UTC()
	y, m, d := utc.Date()
	switch duration {
	case TeamBudgetDaily:
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	case TeamBudgetWeekly:
		// Go's Weekday numbers Sunday 0; shift so Monday opens the week.
		offset := (int(utc.Weekday()) + 6) % 7
		return time.Date(y, m, d-offset, 0, 0, 0, 0, time.UTC)
	case TeamBudgetMonthly:
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Time{}
	}
}

// CheckTeamBudget raises the budget notification when the team's spend in its
// current window has reached max_budget_usd. It returns whether the budget is
// breached. Teams without a budget, archived teams, and unknown durations are
// never breached — no window means nothing to measure against. The dedupe key
// names the window, so the breach alerts once per window (Repo.Notify folds
// repeats) and the next window re-arms automatically.
func (r *Repo) CheckTeamBudget(ctx context.Context, teamID string, now time.Time) (bool, error) {
	team, err := r.GetTeam(teamID)
	if err != nil {
		return false, fmt.Errorf("teams: budget check %s: %w", teamID, err)
	}
	if team.Status != TeamStatusActive || team.MaxBudgetUSD <= 0 ||
		!IsValidTeamBudgetDuration(team.BudgetDuration) {
		return false, nil
	}
	windowStart := BudgetWindowStart(team.BudgetDuration, now)
	if windowStart.IsZero() {
		return false, nil
	}
	spend, err := r.TeamSpendInWindow(ctx, teamID, windowStart.UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	if spend < team.MaxBudgetUSD {
		return false, nil
	}
	dedupeKey := fmt.Sprintf("team_budget:%s:%s", teamID, windowStart.UTC().Format(time.RFC3339))
	title := "Team budget exceeded: " + team.Name
	body := fmt.Sprintf("Team %s has spent %.4f USD of its %.2f USD %s budget.",
		team.Name, spend, team.MaxBudgetUSD, team.BudgetDuration)
	if _, err := r.Notify(ctx, "", "", dedupeKey, "budget", "warn", title, body); err != nil {
		return true, fmt.Errorf("teams: budget check %s: notify: %w", teamID, err)
	}
	return true, nil
}
