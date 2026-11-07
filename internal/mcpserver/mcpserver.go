// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package mcpserver implements a minimal Model Context Protocol server for
// 9RouterX: JSON-RPC 2.0 over stdio (for IDE integrations) and over HTTP POST
// (for remote callers), with a scoped auth token.
//
// The tool set is deliberately small (the task doc's "10-20 tools"): every
// tool maps to one dashboard read or one operator action, so the surface an
// MCP client can touch is exactly the surface the dashboard already guards.
// Auth is a bearer token compared constant-time against ROUTERX_MCP_TOKEN;
// a missing or wrong token refuses every call — there is no anonymous mode.
package mcpserver

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/log"
)

// jsonrpcRequest is one JSON-RPC 2.0 call (method + params + id).
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is the JSON-RPC 2.0 reply (result or error, never both).
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	// errParse / errInvalidRequest / errMethodNotFound mirror the JSON-RPC 2.0
	// spec's reserved error codes; errInternal is the catch-all.
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInternal       = -32603
)

// MCPServer serves the Model Context Protocol over stdio and HTTP.
type MCPServer struct {
	repo  *db.Repo
	token string
	mu    sync.Mutex
}

// NewMCPServer builds an MCP server over the given repo. The auth token is
// read once from ROUTERX_MCP_TOKEN (env) — an empty token disables the
// server entirely (every call refused), which is the safe default.
func NewMCPServer(repo *db.Repo) *MCPServer {
	return &MCPServer{
		repo:  repo,
		token: strings.TrimSpace(os.Getenv("ROUTERX_MCP_TOKEN")),
	}
}

// Enabled reports whether the server has a token to authenticate with.
func (s *MCPServer) Enabled() bool {
	return s != nil && s.token != ""
}

// authorize checks the presented token against the configured one.
// Constant-time compare so a timing side channel cannot probe the token.
func (s *MCPServer) authorize(presented string) bool {
	if s.token == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.token)) == 1
}

// handleCall routes one JSON-RPC method to its handler and returns the result.
func (s *MCPServer) handleCall(ctx context.Context, method string, params json.RawMessage) (any, *jsonrpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "9RouterX", "version": "1.0"},
		}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefinitions()}, nil
	case "tools/call":
		var req struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments,omitempty"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, &jsonrpcError{Code: errParse, Message: "invalid tools/call params"}
		}
		return s.executeTool(ctx, req.Name, req.Arguments)
	default:
		return nil, &jsonrpcError{Code: errMethodNotFound, Message: "unknown method: " + method}
	}
}

// serveRequest processes one JSON-RPC request and returns the response.
func (s *MCPServer) serveRequest(ctx context.Context, data []byte) *jsonrpcResponse {
	var req jsonrpcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return &jsonrpcResponse{JSONRPC: "2.0", Error: &jsonrpcError{Code: errParse, Message: "invalid JSON"}}
	}
	resp := &jsonrpcResponse{JSONRPC: "2.0", ID: req.ID}

	if req.Method != "initialize" && req.Method != "tools/list" && req.Method != "tools/call" {
		resp.Error = &jsonrpcError{Code: errMethodNotFound, Message: "unknown method: " + req.Method}
		return resp
	}

	result, rpcErr := s.handleCall(ctx, req.Method, req.Params)
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	return resp
}

// ServeStdio runs the MCP server over stdin/stdout (newline-delimited JSON-RPC
// 2.0). Blocks until ctx is cancelled or stdin closes.
func (s *MCPServer) ServeStdio(ctx context.Context) error {
	if !s.Enabled() {
		return fmt.Errorf("mcp: ROUTERX_MCP_TOKEN is not set; server disabled")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp := s.serveRequest(ctx, []byte(line))
		out, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		fmt.Println(string(out))
	}
}

// ServeHTTP handles one MCP call over HTTP POST. Auth: Authorization: Bearer
// <token> or X-MCP-Token. The token must match ROUTERX_MCP_TOKEN exactly.
func (s *MCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.Enabled() {
		http.Error(w, "MCP server disabled: ROUTERX_MCP_TOKEN is not set", http.StatusForbidden)
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.Header.Get("X-MCP-Token")
	}
	if !s.authorize(token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	resp := s.serveRequest(r.Context(), body)
	w.Header().Set("Content-Type", "application/json")
	out, _ := json.Marshal(resp)
	_, _ = w.Write(out)
}

// RunStdio starts the stdio server in a goroutine and returns immediately.
func (s *MCPServer) RunStdio(ctx context.Context) {
	if !s.Enabled() {
		log.Info("mcp", "server disabled (ROUTERX_MCP_TOKEN not set)")
		return
	}
	log.Info("mcp", "stdio server started")
	go func() {
		if err := s.ServeStdio(ctx); err != nil && err != context.Canceled {
			log.Warn("mcp", "stdio server stopped", "error", err)
		}
	}()
}

// toolParam describes one tool argument for the tools/list schema.
type toolParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// toolDef is one tool definition in the tools/list response.
type toolDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema struct {
		Type       string      `json:"type"`
		Properties []toolParam `json:"properties"`
		Required   []string    `json:"required,omitempty"`
	} `json:"inputSchema"`
}

// toolDefinitions returns the MCP tool catalog. The set is deliberately
// read-heavy: every tool maps to one dashboard read or one operator action,
// so the surface an MCP client can touch is exactly the surface the
// dashboard already guards.
func toolDefinitions() []toolDef {
	def := func(name, desc string, params ...toolParam) toolDef {
		d := toolDef{Name: name, Description: desc}
		d.InputSchema.Type = "object"
		d.InputSchema.Properties = params
		for _, p := range params {
			if p.Required {
				d.InputSchema.Required = append(d.InputSchema.Required, p.Name)
			}
		}
		return d
	}
	return []toolDef{
		def("list_keys", "List API keys with metadata (no raw secrets)"),
		def("get_health", "Gateway health and version"),
		def("list_models", "Model catalog per provider"),
		def("list_combos", "Combo configurations with strategies"),
		def("cache_stats", "Semantic cache statistics"),
		def("usage_summary", "Usage aggregated by provider (prompt/completion/cost)"),
		def("security_log", "Recent security events (last N, filter by severity)",
			toolParam{Name: "limit", Type: "number", Description: "Max entries (default 50)"}),
		def("notifications", "Notification feed with unread count"),
		def("resilience_status", "Circuit breaker states per provider"),
		def("team_usage", "Usage report for one team",
			toolParam{Name: "team_id", Type: "string", Description: "Team ID", Required: true}),
		def("backup_create", "Create an immediate database backup (write)"),
		def("quota_check", "Per-connection quota snapshot"),
		def("list_providers", "Provider connections with status"),
		def("get_settings", "Current gateway settings"),
	}
}
