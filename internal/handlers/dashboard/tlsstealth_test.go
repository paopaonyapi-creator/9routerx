// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"9routerx/proxy/internal/tlsstealth"
)

// TestGetTLSStealthStatusBanner pins GET /api/tls-stealth/status: the payload
// the mandatory banner renders from. Off by default the flag reports false and
// the legal notice is still carried — a client that ignores the notice field
// is a bug, not a reason to hide it.
func TestGetTLSStealthStatusBanner(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	router := setupTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/tls-stealth/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Enabled       bool   `json:"enabled"`
		ActiveProfile string `json:"activeProfile"`
		LegalNotice   string `json:"legalNotice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Enabled {
		t.Error("tls stealth must default OFF")
	}
	if body.LegalNotice == "" {
		t.Error("legal notice missing from banner payload")
	}
}

// TestGetTLSStealthStatusEnabled flips the process-wide flag on and checks the
// endpoint reports the active profile — the banner names the fingerprint the
// lane actually presents, so the payload cannot disagree with the controller.
func TestGetTLSStealthStatusEnabled(t *testing.T) {
	tlsstealth.SetEnabled(true)
	defer tlsstealth.SetEnabled(false)
	profile := tlsstealth.ActiveProfile()
	if profile == "" {
		t.Fatal("active profile empty while enabled")
	}

	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/tls-stealth/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var body struct {
		Enabled       bool   `json:"enabled"`
		ActiveProfile string `json:"activeProfile"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.Enabled || body.ActiveProfile != profile {
		t.Errorf("payload = %+v, want enabled with profile %s", body, profile)
	}
}
