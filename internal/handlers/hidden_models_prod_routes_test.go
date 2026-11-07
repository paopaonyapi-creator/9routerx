// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package handlers

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9routerx/proxy/internal/auth"
	"9routerx/proxy/internal/db"

	"github.com/go-chi/chi/v5"
)

// TestSetupServerRouter_HiddenModelsRoutesAreLive pins the FASE 10.3 per-key
// visibility endpoints to the router the server actually mounts
// (SetupServerRouter), not the tests-only dashboard.RegisterRoutes table.
//
// Both routes existed only in that tests-only table, so the live gateway
// answered "404 page not found" to GET and PUT /api/keys/{id}/hidden-models —
// the exact failure shape the teams and jailbreak pairs documented above, and
// one that turns the dashboard's visibility toggle into a silent no-op while
// the package-internal tests stay green.
func TestSetupServerRouter_HiddenModelsRoutesAreLive(t *testing.T) {
	t.Setenv("JWT_SECRET", "hidden-models-router-test-secret")
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	keyID := "hidden-models-route-key"
	if err := repo.CreateApiKey(keyID, "sk-hidden-models-route", "route test", ""); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	token, err := auth.Sign("hidden-models-router-test-secret", time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	authed := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// PUT accepts the wrapped form the SPA sends.
	rec := authed(http.MethodPut, "/api/keys/"+keyID+"/hidden-models",
		`{"patterns":["nan/*"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/keys/{id}/hidden-models: status %d, body %s",
			rec.Code, rec.Body.String())
	}

	rec = authed(http.MethodGet, "/api/keys/"+keyID+"/hidden-models", "")
	if rec.Code == http.StatusNotFound || strings.Contains(rec.Body.String(), "404 page not found") {
		t.Fatalf("GET /api/keys/{id}/hidden-models is not registered on the production router: %s", rec.Body.String())
	}
	var out struct {
		Patterns []string `json:"patterns"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET response not JSON: %v", err)
	}
	if len(out.Patterns) != 1 || out.Patterns[0] != "nan/*" {
		t.Errorf("stored patterns = %v, want [nan/*]", out.Patterns)
	}
}

// TestSetupServerRouter_HiddenModelsMaskModelsListing runs the whole
// interplay: a key with stored hide patterns sees those models dropped from
// its /v1/models listing, while a key without patterns sees them.
func TestSetupServerRouter_HiddenModelsMaskModelsListing(t *testing.T) {
	t.Setenv("JWT_SECRET", "hidden-models-mask-test-secret")
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	hiddenKey := "hidden-mask-key"
	plainKey := "plain-mask-key"
	if err := repo.CreateApiKey(hiddenKey, "sk-hidden-mask", "hidden", ""); err != nil {
		t.Fatalf("create hidden key: %v", err)
	}
	if err := repo.CreateApiKey(plainKey, "sk-plain-mask", "plain", ""); err != nil {
		t.Fatalf("create plain key: %v", err)
	}
	// The hide list in the shape the dashboard stores it.
	if err := repo.SetApiKeyHiddenModels(hiddenKey, `["nan/*"]`); err != nil {
		t.Fatalf("set hidden models: %v", err)
	}

	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	models := func(key string) []string {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/models with key: status %d, body %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("models response not JSON: %v", err)
		}
		ids := make([]string, 0, len(out.Data))
		for _, m := range out.Data {
			ids = append(ids, m.ID)
		}
		return ids
	}

	hidden := models("sk-hidden-mask")
	plain := models("sk-plain-mask")

	hiddenSeesNan, plainSeesNan := false, false
	for _, id := range hidden {
		if strings.HasPrefix(id, "nan/") {
			hiddenSeesNan = true
		}
	}
	for _, id := range plain {
		if strings.HasPrefix(id, "nan/") {
			plainSeesNan = true
		}
	}
	if hiddenSeesNan {
		t.Errorf("key with hide list [nan/*] still sees a nan/* model in /v1/models: %v", hidden)
	}
	if len(plain) == 0 {
		t.Skip("registry catalog is empty in the offline test environment; masking could not be exercised")
	}
	if !plainSeesNan {
		t.Errorf("key without hide list lost the nan/* models: %v — the mask matched too much", plain)
	}
}
