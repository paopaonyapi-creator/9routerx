// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/handlerutil"
)

// Wire shapes and request parsing for the team cost-center handlers
// (teams.go), split out so the handlers stay readable. The request fields are
// pointers so PUT can distinguish "leave it" from "set it to zero", the same
// convention the plan handlers use.

// maxTeamNameLen bounds the operator-facing team name, the same way
// maxPlanNameLen bounds a plan's.
const maxTeamNameLen = 200

// maxTeamDescriptionLen bounds the description. Display text only.
const maxTeamDescriptionLen = 2000

// defaultUsageWindow is how far back a report reads when the request names
// no startDate.
const defaultUsageWindow = 30 * 24 * time.Hour

// teamRequest is the wire shape of a team write.
type teamRequest struct {
	Name           *string  `json:"name"`
	Description    *string  `json:"description"`
	MaxBudgetUsd   *float64 `json:"maxBudgetUsd"`
	BudgetDuration *string  `json:"budgetDuration"`
}

// teamResponse is the wire shape of a team read.
type teamResponse struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Status         string  `json:"status"`
	MaxBudgetUsd   float64 `json:"maxBudgetUsd"`
	BudgetDuration string  `json:"budgetDuration"`
	BudgetResetAt  string  `json:"budgetResetAt,omitempty"`
	ArchivedAt     string  `json:"archivedAt,omitempty"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

func teamToResponse(t db.Team) teamResponse {
	return teamResponse{
		ID: t.ID, Name: t.Name, Description: t.Description, Status: t.Status,
		MaxBudgetUsd: t.MaxBudgetUSD, BudgetDuration: t.BudgetDuration,
		BudgetResetAt: t.BudgetResetAt, ArchivedAt: t.ArchivedAt,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

// teamFromRequest merges a partial write over base and validates the fields
// the database layer trusts.
func teamFromRequest(req teamRequest, base db.Team) (db.Team, error) {
	team := base
	if req.Name != nil {
		team.Name = *req.Name
	}
	if req.Description != nil {
		team.Description = *req.Description
	}
	if req.MaxBudgetUsd != nil {
		team.MaxBudgetUSD = *req.MaxBudgetUsd
	}
	if req.BudgetDuration != nil {
		team.BudgetDuration = *req.BudgetDuration
	}
	if team.Name == "" || len(team.Name) > maxTeamNameLen {
		return db.Team{}, fmt.Errorf("name is required (max %d characters)", maxTeamNameLen)
	}
	if len(team.Description) > maxTeamDescriptionLen {
		return db.Team{}, fmt.Errorf("description too long (max %d characters)", maxTeamDescriptionLen)
	}
	if team.BudgetDuration != "" && !db.IsValidTeamBudgetDuration(team.BudgetDuration) {
		return db.Team{}, fmt.Errorf("budgetDuration must be one of 1d, 7d, 30d")
	}
	if team.MaxBudgetUSD < 0 {
		return db.Team{}, fmt.Errorf("maxBudgetUsd must not be negative")
	}
	return team, nil
}

// readTeamBody reads and parses a team write body. It writes the error
// itself and reports whether parsing succeeded.
func readTeamBody(w http.ResponseWriter, r *http.Request) (teamRequest, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read request body")
		return teamRequest{}, false
	}
	defer func() { _ = r.Body.Close() }()
	var req teamRequest
	if err := json.Unmarshal(body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return teamRequest{}, false
	}
	return req, true
}

// parseUsageWindow resolves the report window from the query string. It
// writes the error itself and reports whether the bounds are usable.
func parseUsageWindow(w http.ResponseWriter, r *http.Request) (startISO, endISO string, ok bool) {
	now := time.Now().UTC()
	endISO = now.Format(time.RFC3339)
	startISO = now.Add(-defaultUsageWindow).Format(time.RFC3339)
	if raw := r.URL.Query().Get("startDate"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "startDate must be RFC3339")
			return "", "", false
		}
		startISO = parsed.UTC().Format(time.RFC3339)
	}
	if raw := r.URL.Query().Get("endDate"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "endDate must be RFC3339")
			return "", "", false
		}
		endISO = parsed.UTC().Format(time.RFC3339)
	}
	if startISO > endISO {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "startDate must not be after endDate")
		return "", "", false
	}
	return startISO, endISO, true
}
