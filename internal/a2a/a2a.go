// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package a2a implements the Agent-to-Agent protocol surface for 9RouterX: an
// Agent Card served at /.well-known/agent.json and a JSON-RPC 2.0 endpoint at
// POST /api/a2a carrying the message/send, message/stream, tasks/get,
// tasks/list and tasks/cancel methods.
//
// It is a port of the reference A2A server with the execution skills removed.
// No skill routes a chat completion, so an agent that reaches this endpoint
// can read the gateway's state (keys, combos, models, usage, resilience) but
// cannot spend a single token through it. Every skill maps to one dashboard
// read, which is exactly the surface the dashboard already guards.
//
// The surface is off by default: it answers requests only once
// ROUTERX_ENABLE_A2A (or the equivalent config flag) turns it on, and while
// it is off the JSON-RPC methods refuse with HTTP 503 and error code -32000
// while GET /api/a2a/status reports {status: "disabled", online: false}.
//
// Auth is a bearer token from ROUTERX_A2A_TOKEN compared constant-time.
// Enabled servers without a configured token fail closed with 401.
package a2a

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"9routerx/proxy/internal/db"
)

// DefaultTaskTTL is how long a task stays queryable. Terminal tasks are swept
// once this elapses, so a long-lived gateway does not accumulate one map entry
// per delegation forever.
const DefaultTaskTTL = 5 * time.Minute

const (
	// sweepInterval is how often the background sweep drops expired tasks.
	sweepInterval = time.Minute
	// maxRequestBody caps one JSON-RPC request; a delegation is a skill name
	// plus a short message, never a payload.
	maxRequestBody = 1 << 20
	// defaultListLimit / maxListLimit bound tasks/list so one call cannot
	// serialise the whole task map.
	defaultListLimit = 50
	maxListLimit     = 200
)

// JSON-RPC 2.0 error codes. The first five are the spec's reserved range;
// errDisabled is the A2A-specific "endpoint is switched off" code.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
	errDisabled       = -32000
)

// Agent identity advertised on the Agent Card and the health skill.
const (
	agentName        = "9RouterX"
	agentDescription = "9RouterX read-only gateway agent: API keys, combos, models, usage and resilience state."
	// protocolVersion is the A2A protocol revision this surface speaks.
	protocolVersion = "0.3"
)

// The enable flag is package state rather than Server state so a server built
// at boot answers correctly after the one-line wiring call, and so every
// handler agrees on one answer without the flag being threaded through
// constructors.
var (
	flagMu    sync.RWMutex
	flagValue bool
	flagIsSet bool
)

// Enabled reports whether the A2A surface is switched on. It is false until an
// operator sets it: explicitly through SetEnabled, or through the
// ROUTERX_ENABLE_A2A environment variable when nothing has set it.
func Enabled() bool {
	flagMu.RLock()
	set, value := flagIsSet, flagValue
	flagMu.RUnlock()
	if set {
		return value
	}
	return envEnabled()
}

// SetEnabled records the operator's switch. Wiring is one line at boot:
//
//	a2a.SetEnabled(cfg.EnableA2A)
//
// Servers built before the call still answer Enabled() correctly because the
// flag is package state, not per-server state.
func SetEnabled(v bool) {
	flagMu.Lock()
	flagValue = v
	flagIsSet = true
	flagMu.Unlock()
}

// EnabledFrom is SetEnabled under the name a config-plumbing call site reads
// as: the value arrives from config, it is not a request.
func EnabledFrom(v bool) { SetEnabled(v) }

// envEnabled reads ROUTERX_ENABLE_A2A, accepting the usual truthy spellings.
func envEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ROUTERX_ENABLE_A2A"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Server serves the A2A surface over one repo. It holds no request state: the
// task map is the only mutable part and it is guarded by the TaskManager.
type Server struct {
	repo  *db.Repo
	token string
	tasks *TaskManager
}

// NewServer builds an A2A server over the given repo. The auth token is read
// once from ROUTERX_A2A_TOKEN; an empty token refuses enabled requests.
// The task sweeper starts here and stops on Close.
func NewServer(repo *db.Repo) *Server {
	return &Server{
		repo:  repo,
		token: strings.TrimSpace(os.Getenv("ROUTERX_A2A_TOKEN")),
		tasks: NewTaskManager(DefaultTaskTTL),
	}
}

// Close stops the background task sweeper. Safe to call more than once, and
// safe to skip: an idle sweeper is one goroutine on a minute timer.
func (s *Server) Close() {
	if s != nil && s.tasks != nil {
		s.tasks.Close()
	}
}

// Tasks exposes the task manager for tests and diagnostics.
func (s *Server) Tasks() *TaskManager { return s.tasks }

// ServeAgentCard serves the Agent Card at GET /.well-known/agent.json. The
// card is public and cacheable: discovery must work before a caller knows
// which token to present, and it changes only when the skill set does.
func (s *Server) ServeAgentCard(w http.ResponseWriter, r *http.Request) {
	card := agentCard{
		Name:               agentName,
		Description:        agentDescription,
		Version:            agentVersion(),
		URL:                baseURL(r) + "/api/a2a",
		ProtocolVersion:    protocolVersion,
		Capabilities:       map[string]any{"streaming": true, "pushNotifications": false, "stateTransitionHistory": true},
		Authentication:     map[string]any{"schemes": []string{"bearer"}},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text", "data"},
		Skills:             skillDefs,
		Provider:           map[string]any{"organization": agentName},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, card)
}

// ServeStatus serves GET /api/a2a/status: whether the surface is online, which
// skills it exposes and whether a token is required. Public, because a caller
// deciding whether to delegate needs the answer before it can authenticate.
func (s *Server) ServeStatus(w http.ResponseWriter, r *http.Request) {
	online := Enabled()
	status := "disabled"
	if online {
		status = "online"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"online":         online,
		"name":           agentName,
		"version":        agentVersion(),
		"protocol":       protocolVersion,
		"endpoint":       "/api/a2a",
		"authRequired":   online || s.token != "",
		"skills":         skillIDs(),
		"taskTTLMinutes": int(DefaultTaskTTL / time.Minute),
		"tasks":          s.tasks.Count(),
	})
}

// ServeJSONRPC serves POST /api/a2a. Auth runs first, then the enable check,
// then method dispatch: message/send answers synchronously, message/stream
// answers over SSE, the tasks/* methods read and cancel the task map.
func (s *Server) ServeJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRPCError(w, http.StatusMethodNotAllowed, nil, errInvalidRequest, "POST required")
		return
	}
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		message := "Unauthorized"
		if s.token == "" {
			message = "A2A token not configured"
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": message})
		return
	}
	if !Enabled() {
		writeRPCError(w, http.StatusServiceUnavailable, nil, errDisabled,
			"A2A endpoint is disabled (set ROUTERX_ENABLE_A2A to enable)")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		writeRPCError(w, http.StatusOK, nil, errInvalidRequest, "read error")
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, http.StatusOK, nil, errParse, "invalid JSON")
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		writeRPCError(w, http.StatusOK, req.ID, errInvalidRequest, `jsonrpc must be "2.0"`)
		return
	}

	switch req.Method {
	case "message/send":
		params, rpcErr := decodeSendParams(req.Params)
		if rpcErr != nil {
			writeRPCError(w, http.StatusOK, req.ID, rpcErr.Code, rpcErr.Message)
			return
		}
		result, rpcErr := s.sendMessage(r.Context(), params)
		if rpcErr != nil {
			writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr})
			return
		}
		writeRPCResult(w, req.ID, result)
	case "message/stream":
		params, rpcErr := decodeSendParams(req.Params)
		if rpcErr != nil {
			writeRPCError(w, http.StatusOK, req.ID, rpcErr.Code, rpcErr.Message)
			return
		}
		s.streamMessage(w, r, req.ID, params)
	case "tasks/get", "tasks/list", "tasks/cancel":
		result, rpcErr := s.taskMethod(req.Method, req.Params)
		if rpcErr != nil {
			writeRPCError(w, http.StatusOK, req.ID, rpcErr.Code, rpcErr.Message)
			return
		}
		writeRPCResult(w, req.ID, result)
	default:
		writeRPCError(w, http.StatusOK, req.ID, errMethodNotFound, "unknown method: "+req.Method)
	}
}

// authorized preserves disabled-mode responses when no token is configured;
// an enabled server must always have a configured token. Configured tokens
// are compared constant-time.
func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return !Enabled()
	}
	return subtle.ConstantTimeCompare([]byte(requestToken(r)), []byte(s.token)) == 1
}

// requestToken reads the presented token from Authorization: Bearer <token>
// (scheme match is case-insensitive per RFC 7235) or X-A2A-Token.
func requestToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if scheme, value, ok := strings.Cut(auth, " "); ok && strings.EqualFold(scheme, "bearer") {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(r.Header.Get("X-A2A-Token"))
}

// agentCard is the A2A discovery document. Fields follow the protocol's card
// shape so a generic A2A client can drive this gateway without a custom
// integration.
type agentCard struct {
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	Version            string         `json:"version"`
	URL                string         `json:"url"`
	ProtocolVersion    string         `json:"protocolVersion"`
	Capabilities       map[string]any `json:"capabilities"`
	Authentication     map[string]any `json:"authentication"`
	DefaultInputModes  []string       `json:"defaultInputModes"`
	DefaultOutputModes []string       `json:"defaultOutputModes"`
	Skills             []skillDef     `json:"skills"`
	Provider           map[string]any `json:"provider"`
}

// rpcRequest is one JSON-RPC 2.0 call. ID stays raw so a string, number or
// null id round-trips unchanged.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the JSON-RPC 2.0 reply (result or error, never both).
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is one JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "marshal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	writeJSON(w, status, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

// baseURL rebuilds the origin the request arrived on. X-Forwarded-Proto is
// honoured because a gateway behind TLS termination would otherwise advertise
// http:// to every discovering agent; it affects only this URL.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); fwd != "" {
		scheme = fwd
	}
	return scheme + "://" + r.Host
}

var (
	versionOnce  sync.Once
	versionValue string
)

// agentVersion reports the gateway version advertised on the card and the
// health skill. It reads the repo's VERSION file once — working directory,
// then beside the executable so a daemon started elsewhere still finds it —
// and falls back to "1.0" when neither is readable, so a stripped deployment
// still answers discovery.
func agentVersion() string {
	versionOnce.Do(func() {
		versionValue = "1.0"
		candidates := []string{"VERSION"}
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "VERSION"))
		}
		for _, path := range candidates {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if v := strings.TrimSpace(string(data)); v != "" {
				versionValue = v
				return
			}
		}
	})
	return versionValue
}
