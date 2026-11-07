// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/middleware"
	"9routerx/proxy/internal/models"
)

// keyedRequest attaches a synthetic API key to a request the way the auth
// middleware would, so the visibility filter sees a caller.
func keyedRequest(t *testing.T, method, target string, key *models.APIKey) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	return req.WithContext(context.WithValue(req.Context(), middleware.ApiKeyContextKey, key))
}

// TestModelHiddenPatterns pins the matcher contract: the hide list uses the
// allowlist glob (exact, prefix*, *suffix, *mid*), and any candidate spelling
// matching any pattern hides the model.
func TestModelHiddenPatterns(t *testing.T) {
	cases := []struct {
		name       string
		patterns   []string
		candidates []string
		want       bool
	}{
		{"no patterns hide nothing", nil, []string{"openai/gpt-4o"}, false},
		{"exact pattern", []string{"oc/space-bunny-free"}, []string{"oc/space-bunny-free"}, true},
		{"exact pattern misses a sibling", []string{"oc/space-bunny-free"}, []string{"oc/jev-1.13-free"}, false},
		{"prefix wildcard", []string{"oc/*-free"}, []string{"oc/space-bunny-free"}, true},
		{"prefix wildcard misses another provider", []string{"oc/*-free"}, []string{"openai/gpt-4o"}, false},
		{"suffix wildcard", []string{"*haiku*"}, []string{"anthropic/claude-haiku-4"}, true},
		{"match on either spelling", []string{"*haiku*"}, []string{"claude-haiku-4", "anthropic/claude-haiku-4"}, true},
		{"bare word is exact, not substring", []string{"haiku"}, []string{"anthropic/claude-haiku-4"}, false},
		{"match is case-insensitive", []string{"*HAIKU*"}, []string{"anthropic/claude-haiku-4"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelHidden(tc.patterns, tc.candidates...); got != tc.want {
				t.Errorf("modelHidden(%v, %v) = %v, want %v", tc.patterns, tc.candidates, got, tc.want)
			}
		})
	}
}

// TestMergeHiddenPatterns pins the union: both halves parse, blanks and
// duplicates drop, and a malformed half contributes nothing rather than
// failing the read.
func TestMergeHiddenPatterns(t *testing.T) {
	cases := []struct {
		name   string
		own    string
		global string
		want   []string
	}{
		{"both empty", "", "", nil},
		{"own only", `["a","b"]`, "", []string{"a", "b"}},
		{"global only", "", `["g"]`, []string{"g"}},
		{"union in order, deduped", `["a","b"]`, `["b","c"]`, []string{"a", "b", "c"}},
		{"blanks dropped", `["a", " "]`, `["", "c"]`, []string{"a", "c"}},
		{"malformed own half ignored", `not json`, `["g"]`, []string{"g"}},
		{"malformed global half ignored", `["a"]`, `not json`, []string{"a"}},
		{"wrong json type ignored", `[1,2]`, `["g"]`, []string{"g"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeHiddenPatterns(tc.own, tc.global)
			if !slices.Equal(got, tc.want) {
				t.Errorf("mergeHiddenPatterns(%q, %q) = %v, want %v", tc.own, tc.global, got, tc.want)
			}
		})
	}
}

// TestFilterModelsByHidden exercises the listing filter through the DB: the
// key's own patterns and the global list union, an empty list is a no-op, and
// the loader is keyed per API key.
func TestFilterModelsByHidden(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	data := []ModelInfoObject{
		{ID: "oc/space-bunny-free", OwnedBy: "oc"},
		{ID: "openai/gpt-4o", OwnedBy: "openai"},
		{ID: "anthropic/claude-haiku-4", OwnedBy: "anthropic"},
	}

	InvalidateHiddenModelsCache()
	if got := h.filterModelsByHidden(&models.APIKey{ID: "1"}, data); len(got) != len(data) {
		t.Fatalf("no hide list configured: got %d entries, want all %d", len(got), len(data))
	}

	if err := repo.SetApiKeyHiddenModels("1", `["oc/*-free","*haiku*"]`); err != nil {
		t.Fatalf("SetApiKeyHiddenModels: %v", err)
	}
	InvalidateHiddenModelsCache()

	got := h.filterModelsByHidden(&models.APIKey{ID: "1"}, data)
	if len(got) != 1 || got[0].ID != "openai/gpt-4o" {
		t.Fatalf("filtered = %v, want only openai/gpt-4o", modelInfoIDs(got))
	}

	// Another key sees everything: the patterns are per key.
	if got := h.filterModelsByHidden(&models.APIKey{ID: "other"}, data); len(got) != len(data) {
		t.Errorf("other key: got %v, want the full list", modelInfoIDs(got))
	}

	// The global list applies to every key, including keyless callers.
	if err := repo.SetHiddenModels(`["openai/*"]`); err != nil {
		t.Fatalf("SetHiddenModels: %v", err)
	}
	InvalidateHiddenModelsCache()
	if got := h.filterModelsByHidden(&models.APIKey{ID: "other"}, data); len(got) != 2 {
		t.Errorf("global list: got %v, want the two non-openai entries", modelInfoIDs(got))
	}
	if got := h.filterModelsByHidden(nil, data); len(got) != 2 {
		t.Errorf("keyless caller: got %v, want the two non-openai entries", modelInfoIDs(got))
	}
}

// TestHandleModels_HiddenGlobalFiltersListing pins the wire: a model named by
// the global hide list never reaches GET /v1/models, while the rest of the
// catalog is untouched.
func TestHandleModels_HiddenGlobalFiltersListing(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear connections: %v", err)
	}

	before := modelsIDsQuery(t, h, "?all=1")
	if !slices.Contains(before, "oc/jev-1.13-free") {
		t.Fatalf("fixture drift: oc/jev-1.13-free missing from the unfiltered listing (%d ids)", len(before))
	}

	if err := repo.SetHiddenModels(`["oc/*-free"]`); err != nil {
		t.Fatalf("SetHiddenModels: %v", err)
	}
	InvalidateHiddenModelsCache()

	after := modelsIDsQuery(t, h, "?all=1")
	if slices.Contains(after, "oc/jev-1.13-free") {
		t.Error("hidden model still listed on /v1/models")
	}
	if !slices.Contains(after, "openai/gpt-4o") {
		t.Error("the hide list removed more than it matched")
	}
}

// TestEnforceModelAccess_HiddenAnswers404 pins the dispatch contract: a
// request for a hidden model is a 404 with OpenAI's model_not_found code —
// never a 403, which would confirm the model exists — and hidden takes
// precedence over the allowlist verdict.
func TestEnforceModelAccess_HiddenAnswers404(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	if err := repo.SetApiKeyHiddenModels("1", `["oc/space-bunny-free"]`); err != nil {
		t.Fatalf("SetApiKeyHiddenModels: %v", err)
	}
	InvalidateHiddenModelsCache()

	cases := []struct {
		name     string
		model    string
		wantCode int
	}{
		{"hidden model is a 404", "oc/space-bunny-free", http.StatusNotFound},
		{"hidden model under its provider spelling is a 404", "oc/space-bunny-free", http.StatusNotFound},
		{"visible model passes the gate", "openai/gpt-4o", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := keyedRequest(t, http.MethodPost, "/v1/chat/completions", &models.APIKey{ID: "1"})
			w := httptest.NewRecorder()
			if got := h.EnforceModelAccess(w, req, tc.model); got != (tc.wantCode == 0) {
				t.Fatalf("EnforceModelAccess = %v, want pass %v", got, tc.wantCode == 0)
			}
			if tc.wantCode == 0 {
				return
			}
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantCode, w.Body.String())
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if body.Error.Code != "model_not_found" {
				t.Errorf("code = %q, want model_not_found", body.Error.Code)
			}
			// The message must not name the policy: it reads as an ordinary
			// resolution failure.
			if body.Error.Message != "model not found: "+tc.model {
				t.Errorf("message = %q, want the resolution-failure shape", body.Error.Message)
			}
		})
	}

	// A hidden model answers 404 even when the allowlist would have refused
	// it with 403: the 403 would reveal the model exists.
	if err := repo.SetAllowedModels("1", []string{"openai/*"}); err != nil {
		t.Fatalf("SetAllowedModels: %v", err)
	}
	req := keyedRequest(t, http.MethodPost, "/v1/chat/completions", &models.APIKey{ID: "1"})
	w := httptest.NewRecorder()
	if h.EnforceModelAccess(w, req, "oc/space-bunny-free") {
		t.Fatal("hidden model passed the gate")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("hidden behind an allowlist: status = %d, want 404", w.Code)
	}
}

// TestHiddenPolicyCacheInvalidation pins the cache contract: a merged list is
// served from cache until invalidated, and a write that invalidates shows up
// on the next request without waiting out the TTL.
func TestHiddenPolicyCacheInvalidation(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	key := &models.APIKey{ID: "1"}
	data := []ModelInfoObject{{ID: "oc/space-bunny-free", OwnedBy: "oc"}}

	InvalidateHiddenModelsCache()
	if got := h.filterModelsByHidden(key, data); len(got) != 1 {
		t.Fatalf("pre-condition: uncached pass should keep the model, got %v", modelInfoIDs(got))
	}

	if err := repo.SetApiKeyHiddenModels("1", `["oc/*"]`); err != nil {
		t.Fatalf("SetApiKeyHiddenModels: %v", err)
	}
	// Without the invalidation the 30s cache still holds the old verdict.
	if got := h.filterModelsByHidden(key, data); len(got) != 1 {
		t.Fatalf("cached verdict was not served: got %v", modelInfoIDs(got))
	}
	InvalidateHiddenModelsCache()
	if got := h.filterModelsByHidden(key, data); len(got) != 0 {
		t.Errorf("post-invalidation: got %v, want the model hidden", modelInfoIDs(got))
	}
}

func modelInfoIDs(data []ModelInfoObject) []string {
	out := make([]string, 0, len(data))
	for _, m := range data {
		out = append(out, m.ID)
	}
	return out
}
