// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Team cost centers (OmniRoute port): a named budget bucket API keys are
// attributed to. This file holds the Team row and its CRUD plus the per-key
// billing attribution; the spend accounting and the budget check live in
// team_budget.go. The reads and writes here are ctx-less on purpose, matching
// the policy layer (plans.go, guardrails.go).

// Team lifecycle statuses.
const (
	TeamStatusActive   = "active"
	TeamStatusArchived = "archived"
)

// Budget window durations a team may declare. The window is anchored on UTC
// calendar boundaries (BudgetWindowStart), not on a rolling window, so an
// operator reading the dashboard and the budget checker agree on what
// "this window" means without sharing a clock.
const (
	TeamBudgetDaily   = "1d"
	TeamBudgetWeekly  = "7d"
	TeamBudgetMonthly = "30d"
)

// IsValidTeamBudgetDuration reports whether d is one of the three windows.
func IsValidTeamBudgetDuration(d string) bool {
	switch d {
	case TeamBudgetDaily, TeamBudgetWeekly, TeamBudgetMonthly:
		return true
	}
	return false
}

// Team is one tenant cost center.
type Team struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Status         string  `json:"status"`
	MaxBudgetUSD   float64 `json:"maxBudgetUsd"`
	BudgetDuration string  `json:"budgetDuration"`
	BudgetResetAt  string  `json:"budgetResetAt,omitempty"`
	ArchivedAt     string  `json:"archivedAt,omitempty"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

const teamSelectCols = `id, name, COALESCE(description, ''), COALESCE(status, 'active'),
	COALESCE(max_budget_usd, 0), COALESCE(budget_duration, ''), COALESCE(budget_reset_at, ''),
	COALESCE(archived_at, ''), created_at, updated_at`

// CreateTeam inserts a new cost center. The caller supplies the id and the
// initial status; empty status means active.
func (r *Repo) CreateTeam(team Team) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("teams: create: nil repo")
	}
	if team.ID == "" {
		return fmt.Errorf("teams: create: empty id")
	}
	if team.Status == "" {
		team.Status = TeamStatusActive
	}
	now := nowUTC()
	_, err := r.db.Exec( //nolint:noctx
		`INSERT INTO teams (id, name, description, status, max_budget_usd, budget_duration, budget_reset_at, archived_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
		team.ID, team.Name, team.Description, team.Status, team.MaxBudgetUSD,
		team.BudgetDuration, team.BudgetResetAt, now, now,
	)
	if err != nil {
		return fmt.Errorf("teams: create: %w", err)
	}
	return nil
}

// GetTeam returns one team by id, sql.ErrNoRows when absent.
func (r *Repo) GetTeam(id string) (Team, error) {
	if r == nil || r.db == nil || id == "" {
		return Team{}, fmt.Errorf("teams: get: nil repo or empty id")
	}
	row := r.db.QueryRow( //nolint:noctx
		`SELECT `+teamSelectCols+` FROM teams WHERE id = ?`, id,
	)
	return scanTeam(row)
}

// ListTeams returns teams newest first, optionally filtered by status. An
// empty or "all" status lists every team, archived ones included, so the
// dashboard can still render a archived team's spend.
func (r *Repo) ListTeams(status string) ([]Team, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("teams: list: nil repo")
	}
	query := `SELECT ` + teamSelectCols + ` FROM teams`
	args := []any{}
	if status != "" && status != "all" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.Query(query, args...) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("teams: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Team{}
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("teams: list scan: %w", err)
		}
		out = append(out, team)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("teams: list iterate: %w", err)
	}
	return out, nil
}

// UpdateTeam rewrites the mutable fields of a team and stamps updated_at.
// Status is not mutable here: archiving is its own operation (ArchiveTeam).
func (r *Repo) UpdateTeam(team Team) error {
	if r == nil || r.db == nil || team.ID == "" {
		return fmt.Errorf("teams: update: nil repo or empty id")
	}
	res, err := r.db.Exec( //nolint:noctx
		`UPDATE teams SET name = ?, description = ?, max_budget_usd = ?, budget_duration = ?,
		 budget_reset_at = ?, updated_at = ? WHERE id = ?`,
		team.Name, team.Description, team.MaxBudgetUSD, team.BudgetDuration,
		team.BudgetResetAt, nowUTC(), team.ID,
	)
	if err != nil {
		return fmt.Errorf("teams: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("teams: update: %w", sql.ErrNoRows)
	}
	return nil
}

// ArchiveTeam retires a team: status archived plus an archived_at stamp, so
// it stops accepting key assignments but every report and history row keeps
// naming it. Not a delete for the same reason a plan delete refuses while
// keys reference it — the attribution history would dangle.
func (r *Repo) ArchiveTeam(id string) error {
	if r == nil || r.db == nil || id == "" {
		return fmt.Errorf("teams: archive: nil repo or empty id")
	}
	now := nowUTC()
	res, err := r.db.Exec( //nolint:noctx
		`UPDATE teams SET status = ?, archived_at = ?, updated_at = ? WHERE id = ?`,
		TeamStatusArchived, now, now, id,
	)
	if err != nil {
		return fmt.Errorf("teams: archive: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("teams: archive: %w", sql.ErrNoRows)
	}
	return nil
}

// scanTeam scans one row into a Team using the shared column order.
// rowScanner is the plans.go abstraction over sql.Row and *sql.Rows.
func scanTeam(row rowScanner) (Team, error) {
	var t Team
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Status, &t.MaxBudgetUSD,
		&t.BudgetDuration, &t.BudgetResetAt, &t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return Team{}, err
	}
	return t, nil
}

// SetApiKeyBillingTeam points one key at a team and records the change in the
// bitemporal history. An empty teamID clears the assignment: the open history
// row is closed and the key's column returns to the empty string. The three
// writes run in one transaction because a crash between the column write and
// the history append would leave the two disagreeing about what "now"
// belongs to.
func (r *Repo) SetApiKeyBillingTeam(apiKeyID, teamID string) error {
	if r == nil || r.db == nil || apiKeyID == "" {
		return fmt.Errorf("teams: set for key: nil repo or empty key id")
	}
	now := nowUTC()
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("teams: set for key %s: begin: %w", apiKeyID, err)
	}
	defer func() { _ = tx.Rollback() }()

	// Close the currently open interval, if any, then open the new one.
	if _, err := tx.Exec(
		`UPDATE apiKeyBillingTeams SET validTo = ? WHERE apiKeyId = ? AND validTo = ''`,
		now, apiKeyID,
	); err != nil {
		return fmt.Errorf("teams: set for key %s: close interval: %w", apiKeyID, err)
	}
	if teamID != "" {
		if _, err := tx.Exec(
			`INSERT INTO apiKeyBillingTeams (id, apiKeyId, teamId, validFrom, validTo)
			 VALUES (?, ?, ?, ?, '')`,
			uuid.New().String(), apiKeyID, teamID, now,
		); err != nil {
			return fmt.Errorf("teams: set for key %s: open interval: %w", apiKeyID, err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE apiKeys SET billingTeamId = ? WHERE id = ?`, teamID, apiKeyID,
	); err != nil {
		return fmt.Errorf("teams: set for key %s: write column: %w", apiKeyID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("teams: set for key %s: commit: %w", apiKeyID, err)
	}
	return nil
}

// BillingTeamIDForKey returns the team a key is currently billed to, or the
// empty string when the key is unassigned or unknown. Attribution is
// best-effort by design: a failed read must not fail the request whose usage
// row is being written.
func (r *Repo) BillingTeamIDForKey(apiKeyID string) string {
	if r == nil || r.db == nil || apiKeyID == "" {
		return ""
	}
	var teamID string
	err := r.db.QueryRow( //nolint:noctx
		`SELECT COALESCE(billingTeamId, '') FROM apiKeys WHERE id = ?`, apiKeyID,
	).Scan(&teamID)
	if err != nil {
		return ""
	}
	return teamID
}
