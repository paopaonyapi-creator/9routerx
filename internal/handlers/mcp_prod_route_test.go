// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package handlers

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"9routerx/proxy/internal/auth"
	"9routerx/proxy/internal/db"

	"github.com/go-chi/chi/v5"
)

// TestSetupServerRouter_MCPTokenAuthenticatesWithoutSession pins the MCP HTTP
// lane to its own credential. The MCP client carries only ROUTERX_MCP_TOKEN;
// it never has a dashboard session or a client API key, so the endpoint must
// sit outside the RequireDashboardAuth group — mounted inside it, the gate
// answered "dashboard session required" to the very token the endpoint was
// built to verify (a correct Bearer token 401'd on every install with
// requireLogin on, the default, while the stdio lane kept working).
//
// Auth expectations on the public mount:
//   - token unset       → 403, the endpoint is disabled
//   - wrong token       → 401 from ServeHTTP's own check
//   - correct token     → 200 with the JSON-RPC initialize result
//   - no session cookie involved anywhere
func TestSetupServerRouter_MCPTokenAuthenticatesWithoutSession(t *testing.T) {
	t.Setenv("JWT_SECRET", "mcp-router-test-secret")
	t.Setenv("ROUTERX_MCP_TOKEN", "mcp-test-token")
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	// The token is read once at construction (mcpserver.NewMCPServer), so it
	// must already be set when the router mounts the handler.
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	call := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := call("mcp-test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/mcp with the configured token: status %d, body %q — "+
			"the MCP token must authenticate on its own, without a dashboard session",
			rec.Code, rec.Body.String())
	}
	var out struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON-RPC: %v", err)
	}
	if out.Error != nil || out.Result == nil {
		t.Fatalf("initialize with the correct token failed: %s", rec.Body.String())
	}

	if rec := call("wrong-token"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", rec.Code)
	}
	if rec := call(""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401 from the endpoint's own check", rec.Code)
	}

	// A session cookie does not substitute for the token: ServeHTTP decides,
	// not the dashboard gate. The body distinguishes the two — the gate wrote
	// "dashboard session required", the endpoint's own check writes
	// "Unauthorized".
	token, err := auth.Sign("mcp-router-test-secret", time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("cookie without token: status %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "dashboard session required") {
		t.Errorf("the dashboard gate still fronts /api/mcp: %q", rec.Body.String())
	}
}

// TestSetupServerRouter_MCPDisabledWithoutToken pins the safe default: with no
// ROUTERX_MCP_TOKEN the HTTP endpoint refuses every call with 403 instead of
// serving an unauthenticated surface.
func TestSetupServerRouter_MCPDisabledWithoutToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "mcp-router-test-secret")
	t.Setenv("ROUTERX_MCP_TOKEN", "")
	// t.Setenv cannot unset; restore the variable the surrounding process may
	// have, since an inherited token would flip the expectation below.
	orig, had := os.LookupEnv("ROUTERX_MCP_TOKEN")
	t.Cleanup(func() {
		if had {
			os.Setenv("ROUTERX_MCP_TOKEN", orig)
		} else {
			os.Unsetenv("ROUTERX_MCP_TOKEN")
		}
	})
	os.Unsetenv("ROUTERX_MCP_TOKEN")

	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled MCP (no token configured): status %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "MCP server disabled") {
		t.Errorf("disabled MCP body %q, want the disabled notice", rec.Body.String())
	}
}
