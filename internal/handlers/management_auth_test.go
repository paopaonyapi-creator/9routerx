// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9routerx/proxy/internal/auth"
	"9routerx/proxy/internal/db"
	"github.com/go-chi/chi/v5"
)

func TestEngineKeyCannotManageDashboard(t *testing.T) {
	t.Setenv("JWT_SECRET", "audit-test-session-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	if err := repo.CreateApiKey("audit-test-engine", "audit-test-engine-secret", "audit-test-engine", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetApiKeyPermissions("audit-test-engine", `{"endpoints":["chat"],"admin":false}`); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	SetupServerRouter(router, repo, nil)
	blocked := []struct{ method, path string }{
		{http.MethodPost, "/api/keys"},
		{http.MethodDelete, "/api/keys/audit-test-target"},
		{http.MethodPost, "/api/keys/audit-test-target/rotate"},
		{http.MethodPut, "/api/keys/audit-test-target"},
		{http.MethodPut, "/api/keys/audit-test-target/models"},
		{http.MethodPatch, "/api/settings"},
		{http.MethodPut, "/settings"},
		{http.MethodGet, "/api/keys"},
		{http.MethodGet, "/api/webhooks"},
		{http.MethodPost, "/api/settings/auto-backup"},
		{http.MethodPost, "/headroom/start"},
		{http.MethodGet, "/cli-tools/all-statuses"},
		{http.MethodGet, "/api/oauth/kiro/auto-import"},
		{http.MethodPost, "/api/version/update"},
	}
	for _, login := range []bool{true, false} {
		if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": login}); err != nil {
			t.Fatal(err)
		}
		for _, tc := range blocked {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer audit-test-engine-secret")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("login=%v %s %s = %d, want 403: %s", login, tc.method, tc.path, rec.Code, rec.Body.String())
			}
		}
		for _, path := range []string{"/api/providers/catalog", "/api/models/caps?provider=openai"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer audit-test-engine-secret")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("allowlisted %s = %d: %s", path, rec.Code, rec.Body.String())
			}
		}
	}
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatal(err)
	}
	token, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, cli := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/api/keys", strings.NewReader(`{"name":"audit-test-operator-created"}`))
		if cli {
			req.Header.Set(auth.CLITokenHeader, auth.CLIToken())
		} else {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("operator create (cli=%v) = %d: %s", cli, rec.Code, rec.Body.String())
		}
	}
}
