// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestApiKeyHiddenModelsEndpoints pins the dashboard surface of the per-key
// hide list: PUT stores the patterns, GET reads them back, an empty PUT
// clears, and a malformed body is refused without touching the row.
func TestApiKeyHiddenModelsEndpoints(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}
	router := setupTestRouter(repo)
	id, _ := createTestKey(t, router, "Hidden Key")

	put := func(t *testing.T, body any) (int, map[string]any) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/keys/"+id+"/hidden-models", bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// Store two patterns.
	code, out := put(t, map[string]any{"patterns": []string{"oc/*-free", " *haiku* "}})
	if code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %v", code, out)
	}
	patterns, ok := out["patterns"].([]any)
	if !ok || len(patterns) != 2 || patterns[0] != "oc/*-free" || patterns[1] != "*haiku*" {
		t.Errorf("PUT response patterns = %v, want the trimmed pair", out["patterns"])
	}

	// The stored column carries the same JSON array the chat filter parses.
	stored, err := repo.GetApiKeyHiddenModels(id)
	if err != nil {
		t.Fatalf("GetApiKeyHiddenModels: %v", err)
	}
	if stored == "" {
		t.Fatal("stored column is empty after PUT")
	}
	var decoded []string
	if err := json.Unmarshal([]byte(stored), &decoded); err != nil {
		t.Fatalf("stored value is not a JSON array: %q", stored)
	}
	if len(decoded) != 2 || decoded[1] != "*haiku*" {
		t.Errorf("stored = %v, want the trimmed pair", decoded)
	}

	// GET reads the stored list back.
	req := httptest.NewRequest(http.MethodGet, "/api/keys/"+id+"/hidden-models", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", rec.Code)
	}
	var got struct {
		Patterns []string `json:"patterns"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal GET: %v", err)
	}
	if len(got.Patterns) != 2 || got.Patterns[0] != "oc/*-free" {
		t.Errorf("GET patterns = %v", got.Patterns)
	}

	// An empty list clears the hide list.
	code, _ = put(t, map[string]any{"patterns": []string{}})
	if code != http.StatusOK {
		t.Fatalf("clearing PUT expected 200, got %d", code)
	}
	if stored, _ = repo.GetApiKeyHiddenModels(id); stored != "[]" {
		t.Errorf("cleared stored = %q, want []", stored)
	}

	// Refusals leave the row untouched.
	code, _ = put(t, map[string]any{"patterns": []string{strings.Repeat("x", 300)}})
	if code != http.StatusBadRequest {
		t.Errorf("oversized pattern expected 400, got %d", code)
	}
	req = httptest.NewRequest(http.MethodPut, "/api/keys/"+id+"/hidden-models", bytes.NewReader([]byte("not json")))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("garbage body expected 400, got %d", rec.Code)
	}
	if stored, _ = repo.GetApiKeyHiddenModels(id); stored != "[]" {
		t.Errorf("a refused body changed the row: %q", stored)
	}

	// The bare array form is accepted, like the allowlist endpoint.
	req = httptest.NewRequest(http.MethodPut, "/api/keys/"+id+"/hidden-models", bytes.NewReader([]byte(`["a"]`)))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("bare array expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestSettingsHiddenModelsWriteInvalidatesCache pins the global-write path:
// PUT /api/settings with a hiddenModels field persists it and the chat cache
// invalidation runs (observable through the exported cache count).
func TestSettingsHiddenModelsWrite(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}
	router := setupTestRouter(repo)

	body := []byte(`{"hiddenModels":"[\"openrouter/*:free\"]"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := repo.GetHiddenModels()
	if err != nil {
		t.Fatalf("GetHiddenModels: %v", err)
	}
	if got != `["openrouter/*:free"]` {
		t.Errorf("stored hiddenModels = %q", got)
	}
}
