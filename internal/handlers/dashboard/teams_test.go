// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// teams_test.go drives the team cost-center routes over the real route table
// (create → list → set key team → usage report → archive), plus the refusal
// paths each handler owns. It reuses doJSON/setupTestDB/setupTestRouter from
// the package's shared test helpers.

// teamBody is this file's write shape; it must not depend on the handler's
// request struct, or a rename there would keep these tests green.
func teamBody(fields map[string]any) *bytes.Reader {
	raw, _ := json.Marshal(fields)
	return bytes.NewReader(raw)
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v (body %s)", err, rec.Body.String())
	}
	return out
}

type usageReportFixture struct {
	Summary struct {
		Requests           int     `json:"requests"`
		SuccessfulRequests int     `json:"successfulRequests"`
		InputTokens        int64   `json:"inputTokens"`
		OutputTokens       int64   `json:"outputTokens"`
		EstimatedCostUsd   float64 `json:"estimatedCostUsd"`
	} `json:"summary"`
	ByAPIKey []struct {
		APIKey   string  `json:"apiKey"`
		Requests int     `json:"requests"`
		Cost     float64 `json:"cost"`
	} `json:"byApiKey"`
}

func decodeUsageReport(t *testing.T, rec *httptest.ResponseRecorder) usageReportFixture {
	t.Helper()
	var out usageReportFixture
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal usage report: %v (body %s)", err, rec.Body.String())
	}
	return out
}

func TestTeamsEndpointsCRUD(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// Create.
	rec := doJSON(t, router, http.MethodPost, "/api/teams", teamBody(map[string]any{
		"name":           "Acme",
		"description":    "tenant A",
		"maxBudgetUsd":   10.5,
		"budgetDuration": "7d",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("create team expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeMap(t, rec)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created team has no id")
	}
	if created["status"] != "active" || created["maxBudgetUsd"] != float64(10.5) || created["budgetDuration"] != "7d" {
		t.Errorf("created team = %v, want active/10.5/7d", created)
	}

	// List.
	rec = doJSON(t, router, http.MethodGet, "/api/teams", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list teams expected 200, got %d", rec.Code)
	}
	var list struct {
		Teams []map[string]any `json:"teams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Teams) != 1 || list.Teams[0]["name"] != "Acme" {
		t.Fatalf("list = %v, want the one Acme team", list.Teams)
	}

	// Partial update keeps the fields it does not mention.
	rec = doJSON(t, router, http.MethodPut, "/api/teams/"+id, teamBody(map[string]any{"name": "Acme Corp"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("update team expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	updated := decodeMap(t, rec)
	if updated["name"] != "Acme Corp" || updated["maxBudgetUsd"] != float64(10.5) {
		t.Errorf("updated team = %v, want new name with budget preserved", updated)
	}

	// Delete archives rather than removes: the row stays readable for reports.
	rec = doJSON(t, router, http.MethodDelete, "/api/teams/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive team expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, router, http.MethodGet, "/api/teams?status=archived", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal archived list: %v", err)
	}
	if len(list.Teams) != 1 || list.Teams[0]["status"] != "archived" {
		t.Errorf("archived list = %v, want the archived team", list.Teams)
	}
	rec = doJSON(t, router, http.MethodGet, "/api/teams?status=active", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal active list: %v", err)
	}
	if len(list.Teams) != 0 {
		t.Errorf("active list after archive = %v, want empty", list.Teams)
	}
}

// TestTeamsBillingAttributionFlow walks the full route chain the feature
// exists for: create a team, point a key at it, earn usage under it, and read
// the report back through the endpoint.
func TestTeamsBillingAttributionFlow(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	if _, err := repo.RawDB().Exec(
		`INSERT INTO apiKeys (id, key, name, createdAt) VALUES (?, ?, ?, ?)`,
		"key-1", "sk-test-key-1", "Test Key", time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	rec := doJSON(t, router, http.MethodPost, "/api/teams", teamBody(map[string]any{"name": "Acme"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("create team expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	teamID, _ := decodeMap(t, rec)["id"].(string)

	// Set the key's billing team through the route.
	rec = doJSON(t, router, http.MethodPut, "/api/keys/key-1/billing-team", teamBody(map[string]any{"teamId": teamID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("set billing team expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := repo.BillingTeamIDForKey("key-1"); got != teamID {
		t.Fatalf("attribution = %q, want %q", got, teamID)
	}

	// Earn usage under the team, then read it back through the report route.
	if _, err := repo.RawDB().Exec(
		`INSERT INTO usageHistory (timestamp, apiKey, billingTeamId, promptTokens, completionTokens, cost, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"2026-11-05T10:00:00Z", "masked-key-1", teamID, 100, 50, 0.25, "success",
	); err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	rec = doJSON(t, router, http.MethodGet,
		"/api/teams/"+teamID+"/usage?startDate=2026-11-05T00:00:00Z&endDate=2026-11-05T23:59:59Z", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("usage report expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	report := decodeUsageReport(t, rec)
	if report.Summary.Requests != 1 || report.Summary.SuccessfulRequests != 1 ||
		report.Summary.InputTokens != 100 || report.Summary.OutputTokens != 50 {
		t.Errorf("summary = %+v, want the one successful request", report.Summary)
	}
	if report.Summary.EstimatedCostUsd != float64(0.25) {
		t.Errorf("cost = %v, want 0.25", report.Summary.EstimatedCostUsd)
	}
	if len(report.ByAPIKey) != 1 || report.ByAPIKey[0].APIKey != "masked-key-1" {
		t.Errorf("byApiKey = %+v, want one masked-key-1 row", report.ByAPIKey)
	}

	// Defaults: no window params still returns a well-formed report.
	rec = doJSON(t, router, http.MethodGet, "/api/teams/"+teamID+"/usage", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default-window report expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	_ = decodeUsageReport(t, rec)

	// Clearing the assignment empties the attribution.
	rec = doJSON(t, router, http.MethodPut, "/api/keys/key-1/billing-team", teamBody(map[string]any{"teamId": ""}))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear billing team expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := repo.BillingTeamIDForKey("key-1"); got != "" {
		t.Errorf("attribution after clear = %q, want empty", got)
	}
}

func TestTeamsHandlerRefusals(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// One real team and one real key, so every case below exercises the
	// refusal itself rather than a missing fixture.
	rec := doJSON(t, router, http.MethodPost, "/api/teams", teamBody(map[string]any{"name": "Seed"}))
	seedID, _ := decodeMap(t, rec)["id"].(string)
	if _, err := repo.RawDB().Exec(
		`INSERT INTO apiKeys (id, key, name, createdAt) VALUES (?, ?, ?, ?)`,
		"key-2", "sk-test-key-2", "K", time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
		want   int
	}{
		{"create without a name", http.MethodPost, "/api/teams", map[string]any{"description": "x"}, http.StatusBadRequest},
		{"create with a bad duration", http.MethodPost, "/api/teams",
			map[string]any{"name": "X", "budgetDuration": "fortnight"}, http.StatusBadRequest},
		{"create with a negative budget", http.MethodPost, "/api/teams",
			map[string]any{"name": "X", "maxBudgetUsd": -1}, http.StatusBadRequest},
		{"update an unknown team", http.MethodPut, "/api/teams/nope", map[string]any{"name": "X"}, http.StatusNotFound},
		{"usage of an unknown team", http.MethodGet, "/api/teams/nope/usage", nil, http.StatusNotFound},
		{"usage with a bad startDate", http.MethodGet,
			"/api/teams/" + seedID + "/usage?startDate=yesterday", nil, http.StatusBadRequest},
		{"usage with an inverted window", http.MethodGet,
			"/api/teams/" + seedID + "/usage?startDate=2026-11-05T00:00:00Z&endDate=2026-11-01T00:00:00Z",
			nil, http.StatusBadRequest},
		{"billing team on an unknown key", http.MethodPut, "/api/keys/nope/billing-team",
			map[string]any{"teamId": ""}, http.StatusNotFound},
		{"billing team on a ghost team", http.MethodPut, "/api/keys/key-2/billing-team",
			map[string]any{"teamId": "ghost"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, router, tc.method, tc.path, teamBody(tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// A refused ghost assignment must not have touched the key.
	if got := repo.BillingTeamIDForKey("key-2"); got != "" {
		t.Errorf("attribution after refusals = %q, want empty", got)
	}
	// The happy path still works after all the refusals.
	rec = doJSON(t, router, http.MethodPut, "/api/keys/key-2/billing-team", teamBody(map[string]any{"teamId": seedID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("assign existing team expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := repo.BillingTeamIDForKey("key-2"); got != seedID {
		t.Errorf("attribution = %q, want %q", got, seedID)
	}
}
