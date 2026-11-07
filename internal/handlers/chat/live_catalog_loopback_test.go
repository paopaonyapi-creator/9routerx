// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	internalproxy "9routerx/proxy/internal/proxy"

	"9routerx/proxy/internal/db"
)

// TestHandleModels_LoopbackNodeCatalogFollowsDispatchOptIn pins the SSRF
// posture of the compatible-node live catalog: the operator's
// allowPrivateProviderURLs opt-in admits the loopback catalog dial the same
// way it admits the dispatch dial.
//
// Before this was wired, the two lanes judged the same endpoint with different
// rules: chat completions to a loopback node answered 200 behind
// ALLOW_PRIVATE_PROVIDER_URLS=true while /v1/models published nothing for it —
// the catalog fetch was refused at the socket dialer with "request to
// private/reserved IP blocked by SSRF protection" (WRN [models] compatible
// node catalog fetch failed), because no marker on its context ever carried
// the operator's opt-in there. A local Ollama/LM Studio operator ended up with
// a working provider no client could list a model for.
//
// The client here is the production shape (proxy.NewSafeTransport, the
// SSRF-filtering dialer), not a bare httptest client, so the dialer's refusal
// and the loopback marker are both real.
func TestHandleModels_LoopbackNodeCatalogFollowsDispatchOptIn(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"mock-small"},{"id":"mock-large"}]}`))
	}))
	defer srv.Close()

	seed := func(t *testing.T) *ChatHandler {
		// The dialer guard must be active for both cases: this environment
		// variable is the broader socket-level switch, and whatever the
		// surrounding process set must not decide the outcome.
		t.Setenv("ALLOW_PRIVATE_NETWORKING", "")
		resetLiveCatalog(t)
		database, cleanup := setupChatTestDB(t)
		t.Cleanup(cleanup)
		if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
			t.Fatalf("delete connections: %v", err)
		}
		connData := `{"apiKey":"sk-local","providerSpecificData":{"baseUrl":"` + srv.URL + `"}}`
		if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
			('conn-loopback-node', 'openai-compatible-chat-loopback', 'apikey', 'Loopback Node', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, connData); err != nil {
			t.Fatalf("seed node connection: %v", err)
		}
		h := NewChatHandler(db.NewRepo(database))
		h.Client = &http.Client{Transport: internalproxy.NewSafeTransport()}
		return h
	}

	t.Run("opted in: catalog dial is admitted like dispatch", func(t *testing.T) {
		t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "true")
		h := seed(t)
		ids := modelsIDs(t, h)
		joined := strings.Join(ids, "\n")
		if !strings.Contains(joined, "loopback/mock-small") {
			t.Errorf("loopback node models missing from /v1/models despite the dispatch opt-in; got:\n%s", joined)
		}
		// The node's baseUrl is a prefix, so the fetch must land on the models
		// lane, not the node's root page — the same completion the chat lane gets.
		if gotPath != "/v1/models" {
			t.Errorf("catalog fetch hit %q, want /v1/models", gotPath)
		}
	})

	t.Run("not opted in: catalog dial stays refused", func(t *testing.T) {
		t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "")
		h := seed(t)
		ids := modelsIDs(t, h)
		joined := strings.Join(ids, "\n")
		if strings.Contains(joined, "loopback/mock-small") {
			t.Errorf("loopback node models published without any opt-in; got:\n%s", joined)
		}
	})
}
