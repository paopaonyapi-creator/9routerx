// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"9routerx/proxy/internal/freeresilience"
)

// TestHandleGetProvidersHealth covers the B.3 endpoint: the roster lists the
// shipped free catalogue even when untried, outcomes move the numbers, and a
// cooling provider carries its status and countdown.
func TestHandleGetProvidersHealth(t *testing.T) {
	tracker := freeresilience.NewTracker(freeresilience.DefaultFallbackConfig(), nil, func(provider, model string) string {
		if provider == "groq" {
			return "recurring-daily"
		}
		if provider == "opencode" {
			return "keyless"
		}
		return ""
	})
	tracker.RecordOutcome("groq", "openai/gpt-oss-120b", 200, nil, 150, nil)
	tracker.RecordOutcome("groq", "openai/gpt-oss-120b", 200, nil, 250, nil)
	tracker.RecordOutcome("opencode", "big-pickle", 429, []byte(`rate limit`), 0, nil)

	h := &DashboardHandler{FreeResilience: tracker}
	req := httptest.NewRequest(http.MethodGet, "/api/providers/health", nil)
	rec := httptest.NewRecorder()
	h.HandleGetProvidersHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp ProvidersHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v — body: %s", err, rec.Body.String())
	}
	if len(resp.Providers) < 30 {
		t.Fatalf("roster = %d providers, want the full free catalogue (30+)", len(resp.Providers))
	}
	byName := map[string]ProviderHealthResponse{}
	for _, row := range resp.Providers {
		byName[row.Provider] = row
	}
	groq, ok := byName["groq"]
	if !ok {
		t.Fatal("groq missing from the roster")
	}
	if groq.Status != "healthy" || groq.Samples != 2 || groq.SuccessRatePct != 100 || groq.Regime != "recurring-daily" {
		t.Errorf("groq = %+v", groq)
	}
	if groq.FreeModels != 5 {
		t.Errorf("groq freeModels = %d, want 5 (the catalogued ids)", groq.FreeModels)
	}
	oc, ok := byName["opencode"]
	if !ok {
		t.Fatal("opencode missing from the roster")
	}
	if oc.Status != "cooling" || oc.CoolingUntil == "" || oc.Regime != "keyless" {
		t.Errorf("opencode = %+v, want cooling with a countdown", oc)
	}
	if resp.CoolingNow < 1 {
		t.Errorf("coolingNow = %d, want >= 1", resp.CoolingNow)
	}
	if resp.GeneratedAt == "" {
		t.Error("generatedAt must be stamped")
	}
	// Untried providers still appear, marked healthy with no data.
	if gemini, ok := byName["gemini"]; !ok || gemini.Samples != 0 || gemini.SuccessRatePct != -1 {
		t.Errorf("gemini = %+v, want present with no data", gemini)
	}
}

// TestHandleGetProvidersHealth_NoTracker: a handler built outside a boot
// answers an empty roster, not a 500.
func TestHandleGetProvidersHealth_NoTracker(t *testing.T) {
	h := &DashboardHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/providers/health", nil)
	rec := httptest.NewRecorder()
	h.HandleGetProvidersHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp ProvidersHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Errorf("body must be valid JSON: %v — %s", err, rec.Body.String())
	}
}

// TestHandleResetProvidersHealth exercises the operator reset: a cooling
// provider is lifted and reported as cleared.
func TestHandleResetProvidersHealth(t *testing.T) {
	tracker := freeresilience.NewTracker(freeresilience.DefaultFallbackConfig(), nil, nil)
	tracker.RecordOutcome("opencode", "big-pickle", 429, []byte(`rate limit`), 0, nil)
	if cooling, _ := tracker.IsCooling("opencode"); !cooling {
		t.Fatal("opencode must be cooling before the reset")
	}

	h := &DashboardHandler{FreeResilience: tracker}
	req := httptest.NewRequest(http.MethodPost, "/api/providers/health/reset", nil)
	rec := httptest.NewRecorder()
	h.HandleResetProvidersHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Cleared []string `json:"cleared"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Cleared) != 1 || resp.Cleared[0] != "opencode" {
		t.Fatalf("cleared = %v, want [opencode]", resp.Cleared)
	}
	if cooling, until := tracker.IsCooling("opencode"); cooling {
		t.Errorf("opencode still cooling until %v after the reset", until)
	}
	_ = time.Now()
}
