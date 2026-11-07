// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9routerx/proxy/internal/db"
)

func TestHandleGetProviderErrorRates(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	h := NewDashboardHandler(repo, nil)

	// Seed usageHistory
	_ = repo.InsertUsageHistory("openai", "gpt-4o", "c1", "sk-1", "/v1/chat/completions",
		10, 10, 0, "success", 20, "{}", "{}", "")
	_ = repo.InsertRequestDetail("rd-err-1", "openai", "gpt-4o", "c1", "error", "{}")

	req := httptest.NewRequest(http.MethodGet, "/api/usage/provider-error-rates", nil)
	rec := httptest.NewRecorder()
	h.HandleGetProviderErrorRates(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Rates []db.ProviderHourlyErrorRate `json:"rates"`
		Total int                          `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Total == 0 || len(resp.Rates) == 0 {
		t.Fatalf("expected at least 1 rate entry, got total=%d", resp.Total)
	}
	if resp.Rates[0].Provider != "openai" {
		t.Errorf("provider = %s, want openai", resp.Rates[0].Provider)
	}
	if resp.Rates[0].Total != 2 || resp.Rates[0].Errors != 1 || resp.Rates[0].ErrorRate != 0.5 {
		t.Errorf("rate mismatch: %+v", resp.Rates[0])
	}
}
