// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"io"
	"net/http"

	"github.com/google/uuid"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/log"
)

// Team cost centers (OmniRoute port) — the dashboard surface for tenant
// budgets. A team is a billing bucket keys are attributed to: the usage a
// key earns while it belongs to a team stays on that team's report even
// after the key moves, because the ledger stamps the team at request time
// (usageHistory.billingTeamId), not at read time. The wire shapes and the
// request parsing live in teams_wire.go.

// HandleListTeams handles GET /api/teams?status=. No filter lists every
// team, archived included, so a report's subject never disappears from the
// dashboard.
func (h *DashboardHandler) HandleListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.Repo.ListTeams(r.URL.Query().Get("status"))
	if err != nil {
		log.Error("teams", "list failed", "error", err)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to list teams")
		return
	}
	out := make([]teamResponse, 0, len(teams))
	for _, t := range teams {
		out = append(out, teamToResponse(t))
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"teams": out})
}

// HandleCreateTeam handles POST /api/teams. The id is generated here, so a
// client cannot collide with an existing team or pin a reserved id.
func (h *DashboardHandler) HandleCreateTeam(w http.ResponseWriter, r *http.Request) {
	req, ok := readTeamBody(w, r)
	if !ok {
		return
	}
	team, err := teamFromRequest(req, db.Team{Status: db.TeamStatusActive})
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	team.ID = uuid.New().String()
	if err := h.Repo.CreateTeam(team); err != nil {
		log.Error("teams", "create failed", "error", err)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to create team")
		return
	}
	stored, err := h.Repo.GetTeam(team.ID)
	if err != nil {
		log.Error("teams", "read back failed", "error", err, "id", team.ID)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to read the new team")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, teamToResponse(stored))
}

// HandleUpdateTeam handles PUT /api/teams/{id} as a partial update: an
// omitted field keeps its stored value rather than being reset to zero.
func (h *DashboardHandler) HandleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing team id")
		return
	}
	existing, err := h.Repo.GetTeam(id)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "team not found")
		return
	}
	req, ok := readTeamBody(w, r)
	if !ok {
		return
	}
	team, err := teamFromRequest(req, existing)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	team.ID = id
	if err := h.Repo.UpdateTeam(team); err != nil {
		log.Error("teams", "update failed", "error", err, "id", id)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to update team")
		return
	}
	stored, err := h.Repo.GetTeam(id)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to read the updated team")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, teamToResponse(stored))
}

// HandleArchiveTeam handles DELETE /api/teams/{id}. The row is archived, not
// deleted: history rows and past windows still name it.
func (h *DashboardHandler) HandleArchiveTeam(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing team id")
		return
	}
	if err := h.Repo.ArchiveTeam(id); err != nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "team not found")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "archived": id})
}

// HandleTeamUsage handles GET /api/teams/{id}/usage?startDate=&endDate=.
// Both bounds are RFC3339; a missing endDate is now, a missing startDate is
// 30 days back. The report aggregates the ledger rows the team's keys
// earned in the window, regardless of where the keys point today.
func (h *DashboardHandler) HandleTeamUsage(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing team id")
		return
	}
	if _, err := h.Repo.GetTeam(id); err != nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "team not found")
		return
	}
	startISO, endISO, ok := parseUsageWindow(w, r)
	if !ok {
		return
	}
	report, err := h.Repo.TeamUsageReport(r.Context(), id, startISO, endISO)
	if err != nil {
		log.Error("teams", "usage report failed", "error", err, "id", id)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to build the usage report")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, report)
}

// HandleSetApiKeyBillingTeam handles PUT /api/keys/{id}/billing-team with a
// {teamId} body. An empty teamId clears the assignment. The write records
// the change in the bitemporal history, so reports for earlier windows keep
// the earlier attribution.
func (h *DashboardHandler) HandleSetApiKeyBillingTeam(w http.ResponseWriter, r *http.Request) {
	keyID := getURLParam(r, "id")
	if keyID == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing key id")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer func() { _ = r.Body.Close() }()
	var req struct {
		TeamID string `json:"teamId"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	if req.TeamID != "" {
		if _, err := h.Repo.GetTeam(req.TeamID); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "team not found: "+req.TeamID)
			return
		}
	}
	// GetApiKeyByID answers (nil, nil) for an absent row rather than an error.
	if key, err := h.Repo.GetApiKeyByID(keyID); err != nil || key == nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "api key not found")
		return
	}
	if err := h.Repo.SetApiKeyBillingTeam(keyID, req.TeamID); err != nil {
		log.Error("teams", "set billing team failed", "error", err, "key", keyID)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to set the billing team")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"keyId":  keyID,
		"teamId": req.TeamID,
	})
}
