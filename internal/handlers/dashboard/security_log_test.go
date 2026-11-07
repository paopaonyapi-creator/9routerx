// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9routerx/proxy/internal/securitylog"
)

func TestSecurityLogEndpoints(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	h := NewDashboardHandler(repo, nil)

	ctx := context.Background()
	// Seed some events
	_, err := repo.InsertSecurityLog(ctx, "", "login_failed", "warn", "192.168.1.10", "dashboard", "POST", "/api/auth/login", 401, "Invalid password", "curl")
	if err != nil {
		t.Fatalf("insert event 1: %v", err)
	}
	_, err = repo.InsertSecurityLog(ctx, "", "probe_detected", "critical", "10.0.0.99", "", "GET", "/.env", 404, "Secret file probe", "scanner")
	if err != nil {
		t.Fatalf("insert event 2: %v", err)
	}
	_, err = repo.InsertSecurityLog(ctx, "", "login_success", "info", "127.0.0.1", "dashboard", "POST", "/api/auth/login", 200, "Password sign-in", "browser")
	if err != nil {
		t.Fatalf("insert event 3: %v", err)
	}

	t.Run("GET /api/security-log lists and classifies events", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/security-log", nil)
		rec := httptest.NewRecorder()
		h.HandleGetSecurityLog(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Events []securitylog.Entry `json:"events"`
			Total  int                 `json:"total"`
			Summary struct {
				Counts          map[string]int `json:"counts"`
				Classifications map[string]int `json:"classifications"`
				Logins          map[string]int `json:"logins"`
				Total           int            `json:"total"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if resp.Total != 3 || len(resp.Events) != 3 {
			t.Fatalf("total = %d, events = %d, want 3", resp.Total, len(resp.Events))
		}
		if resp.Summary.Counts["critical"] != 1 || resp.Summary.Counts["warn"] != 1 || resp.Summary.Counts["info"] != 1 {
			t.Errorf("counts mismatch: %+v", resp.Summary.Counts)
		}
		if resp.Summary.Classifications["breach"] != 1 || resp.Summary.Classifications["suspicious"] != 1 || resp.Summary.Classifications["normal"] != 1 {
			t.Errorf("classifications mismatch: %+v", resp.Summary.Classifications)
		}
		if resp.Summary.Logins["success"] != 1 || resp.Summary.Logins["failed"] != 1 {
			t.Errorf("logins mismatch: %+v", resp.Summary.Logins)
		}
	})

	t.Run("GET /api/security-log with severity filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/security-log?severity=critical", nil)
		rec := httptest.NewRecorder()
		h.HandleGetSecurityLog(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Events []securitylog.Entry `json:"events"`
			Total  int                 `json:"total"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if resp.Total != 1 || len(resp.Events) != 1 {
			t.Fatalf("filtered total = %d, want 1", resp.Total)
		}
		if resp.Events[0].Classification != "breach" {
			t.Errorf("classification = %q, want breach", resp.Events[0].Classification)
		}
	})

	t.Run("DELETE /api/security-log clears events", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/security-log", nil)
		rec := httptest.NewRecorder()
		h.HandleClearSecurityLog(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var clearResp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &clearResp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if clearResp["cleared"] != true {
			t.Errorf("cleared = %v, want true", clearResp["cleared"])
		}

		// Verify empty
		getReq := httptest.NewRequest(http.MethodGet, "/api/security-log", nil)
		getRec := httptest.NewRecorder()
		h.HandleGetSecurityLog(getRec, getReq)

		var resp struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &resp)
		if resp.Total != 0 {
			t.Errorf("total after clear = %d, want 0", resp.Total)
		}
	})
}
