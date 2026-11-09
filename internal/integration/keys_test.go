//go:build integration

// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package integration

import (
	"net/http"
	"strings"
	"testing"

	"9routerx/proxy/internal/auth"

	"github.com/samber/lo"
)

// apiKeyRow is one row of GET /api/keys.
type apiKeyRow struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	IsActive int    `json:"isActive"`
}

// listApiKeys fetches every client key the dashboard can see.
func listApiKeys(t *testing.T, env *Env) []apiKeyRow {
	t.Helper()
	res := env.Get(t, "/api/keys")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /api/keys = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var rows []apiKeyRow
	res.Decode(t, &rows)
	return rows
}

// requireApiKey returns the row with the given id, failing when it is absent.
// Callers assert on one specific row, so a list that came back empty (or
// without it) must fail rather than silently skip the assertion.
func requireApiKey(t *testing.T, rows []apiKeyRow, id string) apiKeyRow {
	t.Helper()
	row, ok := lo.Find(rows, func(r apiKeyRow) bool { return r.ID == id })
	if !ok {
		t.Fatalf("api key %s is not in the list %+v", id, rows)
	}
	return row
}

// TestApiKeyLifecycle pins the key-management flow end to end: create a key,
// use it to call the engine, deactivate it and see the call rejected, then
// delete it. Each transition is a distinct authorization state a client depends
// on, so a toggle that stopped revoking would keep a retired key usable.
func TestApiKeyLifecycle(t *testing.T) {
	env := newEnv(t)

	created := env.Post(t, "/api/keys", map[string]any{"name": "CI Key"})
	if created.Status != http.StatusOK {
		t.Fatalf("POST /api/keys = %d, want 200 (body: %s)", created.Status, truncate(created.Body))
	}
	var createResp struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	created.Decode(t, &createResp)
	if createResp.Key == "" || !strings.HasPrefix(createResp.Key, "sk-") {
		t.Fatalf("POST /api/keys key = %q, want a generated sk- secret returned once", createResp.Key)
	}
	id := createResp.ID

	t.Run("the new key authenticates the engine", func(t *testing.T) {
		res := env.Get(t, "/v1/models", WithAPIKey(createResp.Key))
		if res.Status != http.StatusOK {
			t.Fatalf("GET /v1/models with the new key = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
		}
		if got := requireApiKey(t, listApiKeys(t, env), id).Name; got != "CI Key" {
			t.Errorf("key name = %q, want \"CI Key\"", got)
		}
	})

	t.Run("deactivating revokes it", func(t *testing.T) {
		if res := env.Put(t, "/api/keys/"+id+"/toggle", map[string]any{"isActive": false}); res.Status != http.StatusOK {
			t.Fatalf("PUT /api/keys/%s/toggle = %d, want 200 (body: %s)", id, res.Status, truncate(res.Body))
		}
		if got := requireApiKey(t, listApiKeys(t, env), id).IsActive; got != 0 {
			t.Errorf("isActive = %d, want 0 after the toggle", got)
		}
		res := env.Get(t, "/v1/models", WithAPIKey(createResp.Key))
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("GET /v1/models with a deactivated key = %d, want 401 (body: %s)", res.Status, truncate(res.Body))
		}
	})

	t.Run("deleting revokes it", func(t *testing.T) {
		if err := env.Repo.SetApiKeyStatus(id, true); err != nil {
			t.Fatalf("reactivate key: %v", err)
		}
		if res := env.Delete(t, "/api/keys/"+id); res.Status != http.StatusOK {
			t.Fatalf("DELETE /api/keys/%s = %d, want 200 (body: %s)", id, res.Status, truncate(res.Body))
		}
		res := env.Get(t, "/v1/models", WithAPIKey(createResp.Key))
		if res.Status != http.StatusUnauthorized {
			t.Fatalf("GET /v1/models with a deleted key = %d, want 401 (body: %s)", res.Status, truncate(res.Body))
		}
	})
}

// TestApiKeysAreMaskedForApiKeyCallers pins the boundary item 1 drew. Listing
// keys is management, so an engine client key — the credential handed to a
// client app — is refused outright rather than answered with the masked list:
// one leaked key must not even enumerate the other keys.
func TestApiKeysAreMaskedForApiKeyCallers(t *testing.T) {
	env := newEnv(t)

	if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatalf("set requireLogin: %v", err)
	}
	if err := env.Repo.CreateApiKey("other-key", "sk-other-secret-value", "Other", ""); err != nil {
		t.Fatalf("seed second key: %v", err)
	}

	// The seeded key must actually be listed to an operator, otherwise the
	// refusal below would prove nothing about what an engine key can reach.
	requireApiKey(t, listApiKeys(t, env), "other-key")

	res := env.Get(t, "/api/keys", WithAPIKey("sk-other-secret-value"))
	if res.Status != http.StatusForbidden {
		t.Fatalf("GET /api/keys as an api-key caller = %d, want 403: %s", res.Status, truncate(res.Body))
	}
	if strings.Contains(string(res.Body), "sk-other-secret-value") {
		t.Errorf("GET /api/keys leaked a key in a refusal body: %s", truncate(res.Body))
	}

	// The operator credential still reads the list, and a legacy row that
	// still holds its plaintext is revealed to it — the transition behavior
	// item 5 kept.
	privileged := env.Get(t, "/api/keys", WithHeader(auth.CLITokenHeader, auth.CLIToken()))
	if privileged.Status != http.StatusOK {
		t.Fatalf("GET /api/keys with the CLI token = %d, want 200: %s", privileged.Status, truncate(privileged.Body))
	}
	if !strings.Contains(string(privileged.Body), "sk-other-secret-value") {
		t.Errorf("CLI-token caller did not receive the full key: %s", truncate(privileged.Body))
	}
}

// comboRow is one row of GET /api/combos.
type comboRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Models string `json:"models"`
}

// listCombos fetches every combo.
func listCombos(t *testing.T, env *Env) []comboRow {
	t.Helper()
	res := env.Get(t, "/api/combos")
	if res.Status != http.StatusOK {
		t.Fatalf("GET /api/combos = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var rows []comboRow
	res.Decode(t, &rows)
	return rows
}

// requireCombo returns the row with the given id, failing when it is absent.
func requireCombo(t *testing.T, rows []comboRow, id string) comboRow {
	t.Helper()
	row, ok := lo.Find(rows, func(r comboRow) bool { return r.ID == id })
	if !ok {
		t.Fatalf("combo %s is not in the list %+v", id, rows)
	}
	return row
}

// TestComboLifecycleThroughTheDashboard pins combo CRUD over HTTP, including
// the 400 on a nameless create. Combos are how an operator expresses failover,
// and the dashboard is the only place they can be created.
func TestComboLifecycleThroughTheDashboard(t *testing.T) {
	env := newEnv(t)

	t.Run("create requires a name", func(t *testing.T) {
		res := env.Post(t, "/api/combos", map[string]any{"models": []string{"deepseek/deepseek-chat"}})
		if res.Status != http.StatusBadRequest {
			t.Fatalf("POST /api/combos without a name = %d, want 400 (body: %s)", res.Status, truncate(res.Body))
		}
	})

	created := env.Post(t, "/api/combos", map[string]any{
		"name":   "dashboard-combo",
		"kind":   "llm",
		"models": []string{"deepseek/deepseek-chat", "groq/llama-3.3-70b-versatile"},
	})
	if created.Status != http.StatusOK {
		t.Fatalf("POST /api/combos = %d, want 200 (body: %s)", created.Status, truncate(created.Body))
	}
	var createResp struct {
		ID string `json:"id"`
	}
	created.Decode(t, &createResp)
	id := createResp.ID

	t.Run("it is listed with its members", func(t *testing.T) {
		found := requireCombo(t, listCombos(t, env), id)
		if !strings.Contains(found.Models, "deepseek/deepseek-chat") {
			t.Errorf("combo models = %q, want the first member persisted", found.Models)
		}
		if !strings.Contains(found.Models, "groq/llama-3.3-70b-versatile") {
			t.Errorf("combo models = %q, want the second member persisted", found.Models)
		}
	})

	t.Run("update replaces the member list", func(t *testing.T) {
		res := env.Put(t, "/api/combos/"+id, map[string]any{
			"name":   "dashboard-combo",
			"kind":   "llm",
			"models": []string{"groq/llama-3.3-70b-versatile"},
		})
		if res.Status != http.StatusOK {
			t.Fatalf("PUT /api/combos/%s = %d, want 200 (body: %s)", id, res.Status, truncate(res.Body))
		}
		if got := requireCombo(t, listCombos(t, env), id).Models; strings.Contains(got, "deepseek/") {
			t.Errorf("combo models = %q, want the removed member gone", got)
		}
	})

	t.Run("delete removes it", func(t *testing.T) {
		res := env.Delete(t, "/api/combos/"+id)
		if res.Status != http.StatusOK {
			t.Fatalf("DELETE /api/combos/%s = %d, want 200 (body: %s)", id, res.Status, truncate(res.Body))
		}
		remaining := listCombos(t, env)
		if lo.ContainsBy(remaining, func(r comboRow) bool { return r.ID == id }) {
			t.Errorf("combo %s still listed after DELETE", id)
		}
	})
}
