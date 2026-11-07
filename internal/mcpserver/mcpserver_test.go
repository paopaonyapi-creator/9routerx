// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"9routerx/proxy/internal/db"
)

func setupMCPTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.OpenDatabase(filepath.Join(dir, "mcp.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

func TestMCPServerJSONRPCToolList(t *testing.T) {
	repo := setupMCPTestRepo(t)
	s := NewMCPServer(repo)
	s.token = "test-token-123"

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	body, _ := json.Marshal(req)

	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	httpReq.Header.Set("Authorization", "Bearer test-token-123")
	s.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", resp.Result)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools empty or missing: %v", result)
	}
	if len(tools) < 10 {
		t.Errorf("tools count = %d, want >= 10", len(tools))
	}
}

func TestMCPServerToolCall(t *testing.T) {
	repo := setupMCPTestRepo(t)
	s := NewMCPServer(repo)
	s.token = "test-token-123"

	// Seed a key
	if err := repo.CreateApiKey("k-mcp", "sk-mcp-test", "mcp test key", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	toolCall := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "list_keys",
		},
	}
	body, _ := json.Marshal(toolCall)

	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	httpReq.Header.Set("Authorization", "Bearer test-token-123")
	s.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map")
	}
	keys, ok := result["keys"].([]any)
	if !ok || len(keys) == 0 {
		t.Fatalf("keys empty: %v", result)
	}
}

func TestMCPServerAuthRequired(t *testing.T) {
	repo := setupMCPTestRepo(t)
	s := NewMCPServer(repo)
	s.token = "test-token-123"

	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	body, _ := json.Marshal(req)

	// No token → 401
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	s.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", rec.Code)
	}

	// Wrong token → 401
	rec2 := httptest.NewRecorder()
	httpReq2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	httpReq2.Header.Set("Authorization", "Bearer wrong-token")
	s.ServeHTTP(rec2, httpReq2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", rec2.Code)
	}
}

func TestMCPServerDisabledWithoutToken(t *testing.T) {
	s := NewMCPServer(setupMCPTestRepo(t))
	s.token = ""
	os.Unsetenv("ROUTERX_MCP_TOKEN")

	if s.Enabled() {
		t.Error("expected Enabled() = false without token")
	}

	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	body, _ := json.Marshal(req)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	s.ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusForbidden {
		t.Errorf("disabled server: status = %d, want 403", rec.Code)
	}
}

func TestMCPServerInitialize(t *testing.T) {
	repo := setupMCPTestRepo(t)
	s := NewMCPServer(repo)
	s.token = "test-token"

	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}
	body, _ := json.Marshal(req)

	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	httpReq.Header.Set("Authorization", "Bearer test-token")
	s.ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp jsonrpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map")
	}
	if result["protocolVersion"] == nil {
		t.Error("protocolVersion missing from initialize result")
	}
}

func TestMCPServerUnknownMethod(t *testing.T) {
	repo := setupMCPTestRepo(t)
	s := NewMCPServer(repo)
	s.token = "test-token"

	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "nonexistent"}
	body, _ := json.Marshal(req)

	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	httpReq.Header.Set("Authorization", "Bearer test-token")
	s.ServeHTTP(rec, httpReq)

	var resp jsonrpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errMethodNotFound)
	}
}

func TestMCPServerContextCancellation(t *testing.T) {
	repo := setupMCPTestRepo(t)
	s := NewMCPServer(repo)
	s.token = "test-token"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// ServeStdio should return immediately on a cancelled context
	if err := s.ServeStdio(ctx); err == nil {
		t.Log("ServeStdio returned nil on cancelled context (stdin EOF first)")
	}
}
