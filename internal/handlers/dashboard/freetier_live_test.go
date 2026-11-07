// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFreeTierLiveHandler pins GET /api/free-tier/live: every tracked
// provider's budget and spend come back with the remaining arithmetic done
// server-side, an overdrawn tier clamps at zero, and an unpublished quota
// never reports low.
func TestFreeTierLiveHandler(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	if err := repo.SetFreeTierBudget("groq", 1000, 1); err != nil {
		t.Fatalf("seed groq: %v", err)
	}
	if err := repo.IncrementFreeTierUsage("groq", 950); err != nil {
		t.Fatalf("increment groq: %v", err)
	}
	// An overdrawn tier and one without a published quota.
	if err := repo.SetFreeTierBudget("overdrawn", 100, 1); err != nil {
		t.Fatalf("seed overdrawn: %v", err)
	}
	if err := repo.IncrementFreeTierUsage("overdrawn", 250); err != nil {
		t.Fatalf("increment overdrawn: %v", err)
	}
	if err := repo.SetFreeTierBudget("unpublished", 0, 0); err != nil {
		t.Fatalf("seed unpublished: %v", err)
	}
	if err := repo.IncrementFreeTierUsage("unpublished", 42); err != nil {
		t.Fatalf("increment unpublished: %v", err)
	}

	router := setupTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/free-tier/live", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Providers []struct {
			Provider     string  `json:"provider"`
			MonthlyQuota int64   `json:"monthlyQuota"`
			ResetDay     int     `json:"resetDay"`
			Used         int64   `json:"used"`
			Remaining    int64   `json:"remaining"`
			RemainingPct float64 `json:"remainingPct"`
			Low          bool    `json:"low"`
		} `json:"providers"`
		GeneratedAt string `json:"generatedAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.GeneratedAt == "" {
		t.Error("generatedAt missing")
	}
	type liveRow struct {
		MonthlyQuota int64
		ResetDay     int
		Used         int64
		Remaining    int64
		RemainingPct float64
		Low          bool
	}
	byProvider := map[string]liveRow{}
	for _, p := range body.Providers {
		byProvider[p.Provider] = liveRow{
			MonthlyQuota: p.MonthlyQuota, ResetDay: p.ResetDay, Used: p.Used,
			Remaining: p.Remaining, RemainingPct: p.RemainingPct, Low: p.Low,
		}
	}

	groq, ok := byProvider["groq"]
	if !ok {
		t.Fatalf("groq missing from response: %v", rec.Body.String())
	}
	if groq.Used != 950 || groq.Remaining != 50 || groq.MonthlyQuota != 1000 {
		t.Errorf("groq = %+v", groq)
	}
	if !groq.Low {
		t.Error("groq at 5% remaining must report low")
	}
	if groq.RemainingPct < 4.9 || groq.RemainingPct > 5.1 {
		t.Errorf("groq remainingPct = %f, want ~5", groq.RemainingPct)
	}

	if od := byProvider["overdrawn"]; od.Remaining != 0 || !od.Low {
		t.Errorf("overdrawn = %+v, want remaining 0 and low", od)
	}
	// A tier without a published quota never badges: a percentage of nothing
	// means nothing.
	if up := byProvider["unpublished"]; up.Low || up.RemainingPct != 100 {
		t.Errorf("unpublished = %+v, want pct 100 and not low", up)
	}
}
