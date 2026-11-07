// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestHandleExportProviderKeys(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	handler := NewDashboardHandler(repo)

	p1 := 1
	p2 := 2
	if err := repo.CreateProviderConnectionFull("conn-1", "anthropic", "apikey", "Key 1", &p1, `{"apiKey":"sk-ant-12345"}`); err != nil {
		t.Fatalf("failed to seed conn-1: %v", err)
	}
	if err := repo.CreateProviderConnectionFull("conn-2", "anthropic", "oauth", "OAuth 1", &p2, `{"accessToken":"tok-ant-67890"}`); err != nil {
		t.Fatalf("failed to seed conn-2: %v", err)
	}
	if err := repo.CreateProviderConnectionFull("conn-3", "openai", "apikey", "OpenAI Key", &p1, `{"apiKey":"sk-oai-99999"}`); err != nil {
		t.Fatalf("failed to seed conn-3: %v", err)
	}

	// The happy paths model the default local install (loopback, no login),
	// the one setup that must still be able to export its own credentials.
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}

	t.Run("exports provider keys accurately", func(t *testing.T) {
		r := chi.NewRouter()
		r.Get("/api/providers/{id}/export-keys", handler.HandleExportProviderKeys)

		req := httptest.NewRequest(http.MethodGet, "/api/providers/anthropic/export-keys", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var res struct {
			Connections []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				AuthType string `json:"authType"`
				Key      string `json:"key"`
			} `json:"connections"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if len(res.Connections) != 2 {
			t.Fatalf("expected 2 connections for anthropic, got %d", len(res.Connections))
		}

		keys := map[string]string{}
		for _, c := range res.Connections {
			keys[c.ID] = c.Key
		}

		if keys["conn-1"] != "sk-ant-12345" {
			t.Errorf("expected conn-1 key 'sk-ant-12345', got %q", keys["conn-1"])
		}
		if keys["conn-2"] != "tok-ant-67890" {
			t.Errorf("expected conn-2 key 'tok-ant-67890', got %q", keys["conn-2"])
		}
	})

	t.Run("returns empty list for provider with no keys", func(t *testing.T) {
		r := chi.NewRouter()
		r.Get("/api/providers/{id}/export-keys", handler.HandleExportProviderKeys)

		req := httptest.NewRequest(http.MethodGet, "/api/providers/emptyprovider/export-keys", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var res struct {
			Connections []any `json:"connections"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if len(res.Connections) != 0 {
			t.Errorf("expected 0 connections, got %d", len(res.Connections))
		}
	})

	// The export hands out raw credentials, so an engine client API key — the
	// credential handed to a client app — must be refused outright. Otherwise
	// one leaked key would dump every provider credential, the same reason
	// HandleGetApiKeys keeps its reveal field privileged.
	t.Run("refuses an engine client api key", func(t *testing.T) {
		if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
			t.Fatalf("enable requireLogin: %v", err)
		}
		t.Cleanup(func() {
			_ = repo.UpdateSettingsRaw(map[string]any{"requireLogin": false})
		})

		if err := repo.CreateApiKey("client-key-1", "client-key", "Client", ""); err != nil {
			t.Fatalf("seed client key: %v", err)
		}

		r := chi.NewRouter()
		r.Get("/api/providers/{id}/export-keys", handler.HandleExportProviderKeys)

		req := httptest.NewRequest(http.MethodGet, "/api/providers/anthropic/export-keys", nil)
		req.Header.Set("Authorization", "Bearer client-key")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for an engine client key, got %d: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "sk-ant-12345") {
			t.Errorf("refused response leaked a stored key: %s", rec.Body.String())
		}
	})
}
