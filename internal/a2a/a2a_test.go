// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
)

func setupA2ATestRepo(t *testing.T) *db.Repo {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.OpenDatabase(filepath.Join(dir, "a2a.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

// newTestServer builds a server with the flag forced off so every test opts
// into the mode it exercises, and stops the sweeper on cleanup.
func newTestServer(t *testing.T, repo *db.Repo, token string) *Server {
	t.Helper()
	SetEnabled(false)
	s := &Server{repo: repo, token: token, tasks: NewTaskManager(DefaultTaskTTL)}
	t.Cleanup(s.Close)
	return s
}

func postRPC(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/a2a", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeJSONRPC(rec, req)
	return rec
}

func postRPCHeader(s *Server, body string, header func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/a2a", strings.NewReader(body))
	if header != nil {
		header(req)
	}
	rec := httptest.NewRecorder()
	s.ServeJSONRPC(rec, req)
	return rec
}

func TestAgentCardShape(t *testing.T) {
	s := newTestServer(t, setupA2ATestRepo(t), "")

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent.json", nil)
	rec := httptest.NewRecorder()
	s.ServeAgentCard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var card agentCard
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if card.Name != "9RouterX" {
		t.Errorf("name = %q, want 9RouterX", card.Name)
	}
	if card.Description == "" {
		t.Error("description empty")
	}
	if card.Version == "" {
		t.Error("version empty")
	}
	if card.URL == "" || !strings.HasSuffix(card.URL, "/api/a2a") {
		t.Errorf("url = %q, want /api/a2a suffix", card.URL)
	}
	schemes, ok := card.Authentication["schemes"].([]any)
	if !ok || len(schemes) != 1 || schemes[0] != "bearer" {
		t.Errorf("authentication.schemes = %v, want [bearer]", card.Authentication["schemes"])
	}
	if len(card.Skills) != 6 {
		t.Fatalf("skills count = %d, want 6", len(card.Skills))
	}
	want := []string{"list-keys", "health-check", "list-models", "list-combos", "usage-report", "resilience-status"}
	got := make([]string, 0, len(card.Skills))
	for _, skill := range card.Skills {
		got = append(got, skill.ID)
	}
	for i, id := range want {
		if got[i] != id {
			t.Errorf("skill[%d] = %q, want %q", i, got[i], id)
		}
	}
}

// TestDisabledMode covers the whole surface with the flag off: every JSON-RPC
// method refuses with 503 and -32000, status reports disabled, and the agent
// card stays public.
func TestDisabledMode(t *testing.T) {
	s := newTestServer(t, setupA2ATestRepo(t), "")
	SetEnabled(false)

	params := `{"skill":"list-keys","messages":[{"role":"user","content":"hi"}]}`
	tests := []struct {
		name   string
		method string
		params string
	}{
		{"message/send", "message/send", params},
		{"message/stream", "message/stream", params},
		{"tasks/get", "tasks/get", `{"taskId":"x"}`},
		{"tasks/list", "tasks/list", `{}`},
		{"tasks/cancel", "tasks/cancel", `{"taskId":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, tt.method, tt.params)
			rec := postRPC(s, body)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
			}
			var resp rpcResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp.Error == nil || resp.Error.Code != errDisabled {
				t.Errorf("error = %+v, want code %d", resp.Error, errDisabled)
			}
		})
	}

	t.Run("status reports disabled", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.ServeStatus(rec, httptest.NewRequest(http.MethodGet, "/api/a2a/status", nil))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out["status"] != "disabled" || out["online"] != false {
			t.Errorf("status = %v, online = %v, want disabled/false", out["status"], out["online"])
		}
	})

	t.Run("agent card still served", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.ServeAgentCard(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent.json", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("card status = %d, want 200", rec.Code)
		}
	})
}

// TestAuth covers bearer auth when a token is configured and the bypass when
// none is.
func TestAuth(t *testing.T) {
	repo := setupA2ATestRepo(t)
	SetEnabled(true)

	body := `{"jsonrpc":"2.0","id":1,"method":"tasks/list","params":{}}`
	tests := []struct {
		name     string
		token    string
		header   func(*http.Request)
		wantCode int
	}{
		{"no token refused", "tok-1", nil, http.StatusUnauthorized},
		{"wrong token refused", "tok-1", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"bearer accepted", "tok-1", func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok-1") }, http.StatusOK},
		{"bearer scheme case-insensitive", "tok-1", func(r *http.Request) { r.Header.Set("Authorization", "bearer tok-1") }, http.StatusOK},
		{"x-a2a-token accepted", "tok-1", func(r *http.Request) { r.Header.Set("X-A2A-Token", "tok-1") }, http.StatusOK},
		{"no token configured bypasses", "", nil, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, repo, tt.token)
			SetEnabled(true)
			rec := postRPCHeader(s, body, tt.header)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}
		})
	}
}

// TestMessageSendListKeys runs one skill end-to-end: a seeded key comes back as
// a data artifact without its raw secret.
func TestMessageSendListKeys(t *testing.T) {
	repo := setupA2ATestRepo(t)
	if err := repo.CreateApiKey("k-a2a", "sk-a2a-secret-value", "a2a test key", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	s := newTestServer(t, repo, "")
	SetEnabled(true)

	body := `{"jsonrpc":"2.0","id":"1","method":"message/send","params":{"skill":"list-keys","messages":[{"role":"user","content":"list keys"}]}}`
	rec := postRPC(s, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", resp.Result)
	}
	task, _ := result["task"].(map[string]any)
	if task == nil || task["state"] != StateCompleted {
		t.Errorf("task = %v, want state %q", task, StateCompleted)
	}
	artifacts, ok := result["artifacts"].([]any)
	if !ok || len(artifacts) != 1 {
		t.Fatalf("artifacts = %v, want one entry", result["artifacts"])
	}
	artifact, _ := artifacts[0].(map[string]any)
	data, _ := artifact["data"].(map[string]any)
	keys, _ := data["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("keys = %v, want the seeded key", data["keys"])
	}
	key, _ := keys[0].(map[string]any)
	if key["id"] != "k-a2a" {
		t.Errorf("key id = %v, want k-a2a", key["id"])
	}
	if strings.Contains(rec.Body.String(), "sk-a2a-secret-value") {
		t.Error("raw key secret leaked in response")
	}
}

func TestMessageStreamSSE(t *testing.T) {
	repo := setupA2ATestRepo(t)
	if err := repo.CreateApiKey("k-a2a", "sk-a2a-secret-value", "a2a test key", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	s := newTestServer(t, repo, "")
	SetEnabled(true)

	body := `{"jsonrpc":"2.0","id":7,"method":"message/stream","params":{"skill":"list-keys","messages":[{"role":"user","content":"list keys"}]}}`
	rec := postRPC(s, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	out := rec.Body.String()
	events := strings.Count(out, "data: ")
	if events != 2 {
		t.Errorf("event count = %d, want 2 (status + final):\n%s", events, out)
	}
	if !strings.Contains(out, `"state":"working"`) {
		t.Errorf("missing working status event:\n%s", out)
	}
	if !strings.Contains(out, `"state":"completed"`) {
		t.Errorf("missing completed final event:\n%s", out)
	}
	if !strings.Contains(out, `"result"`) {
		t.Errorf("final event missing result:\n%s", out)
	}
	if strings.Contains(out, "sk-a2a-secret-value") {
		t.Error("raw key secret leaked in stream")
	}
}

func TestTasksLifecycle(t *testing.T) {
	repo := setupA2ATestRepo(t)
	s := newTestServer(t, repo, "")
	SetEnabled(true)

	send := `{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"skill":"health-check","messages":[{"role":"user","content":"hi"}]}}`
	rec := postRPC(s, send)
	if rec.Code != http.StatusOK {
		t.Fatalf("send status = %d: %s", rec.Code, rec.Body.String())
	}
	var sendResp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sendResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	result := sendResp.Result.(map[string]any)
	task := result["task"].(map[string]any)
	taskID, _ := task["id"].(string)
	if taskID == "" {
		t.Fatalf("task id empty: %v", task)
	}

	tests := []struct {
		name       string
		method     string
		params     string
		wantErr    int
		checkState string
	}{
		{"tasks/get found", "tasks/get", `{"taskId":"` + taskID + `"}`, 0, StateCompleted},
		{"tasks/get unknown", "tasks/get", `{"taskId":"nope"}`, errInvalidParams, ""},
		{"tasks/list", "tasks/list", `{}`, 0, ""},
		{"tasks/cancel terminal is no-op", "tasks/cancel", `{"taskId":"` + taskID + `"}`, 0, StateCompleted},
		{"tasks/cancel unknown", "tasks/cancel", `{"taskId":"nope"}`, errInvalidParams, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":%q,"params":%s}`, tt.method, tt.params)
			rec := postRPC(s, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var resp rpcResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if tt.wantErr != 0 {
				if resp.Error == nil || resp.Error.Code != tt.wantErr {
					t.Errorf("error = %+v, want code %d", resp.Error, tt.wantErr)
				}
				return
			}
			if resp.Error != nil {
				t.Fatalf("unexpected error: %+v", resp.Error)
			}
			out := resp.Result.(map[string]any)
			if tt.method == "tasks/list" {
				tasks, _ := out["tasks"].([]any)
				if len(tasks) == 0 {
					t.Errorf("tasks list empty: %v", out)
				}
				if total, _ := out["total"].(float64); int(total) < len(tasks) {
					t.Errorf("total = %v < returned %d", out["total"], len(tasks))
				}
				return
			}
			got := out["task"].(map[string]any)
			if tt.checkState != "" && got["state"] != tt.checkState {
				t.Errorf("task state = %v, want %q", got["state"], tt.checkState)
			}
		})
	}
}

func TestUnknownMethod(t *testing.T) {
	s := newTestServer(t, setupA2ATestRepo(t), "")
	SetEnabled(true)

	rec := postRPC(s, `{"jsonrpc":"2.0","id":1,"method":"bogus","params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != errMethodNotFound {
		t.Errorf("error = %+v, want %d", resp.Error, errMethodNotFound)
	}
}

// TestTaskManagerSweep pins the TTL rule at the manager level: terminal tasks
// leave after one TTL, in-flight tasks stay queryable.
func TestTaskManagerSweep(t *testing.T) {
	m := NewTaskManager(DefaultTaskTTL)
	defer m.Close()

	done := m.Create("list-keys")
	m.Complete(done.ID, nil, nil)
	working := m.Create("list-keys")
	m.Transition(working.ID, StateWorking)

	now := time.Now().UTC()
	if dropped := m.Sweep(now); dropped != 0 {
		t.Errorf("fresh terminal task swept early: dropped %d", dropped)
	}
	if _, ok := m.Get(working.ID); !ok {
		t.Error("working task lost")
	}

	dropped := m.Sweep(now.Add(DefaultTaskTTL + time.Minute))
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if _, ok := m.Get(done.ID); ok {
		t.Error("terminal task survived its TTL")
	}
	if _, ok := m.Get(working.ID); !ok {
		t.Error("non-terminal task swept")
	}
}

// TestCancelNonTerminal pins the cancel path on a task its handler still owns.
func TestCancelNonTerminal(t *testing.T) {
	m := NewTaskManager(DefaultTaskTTL)
	defer m.Close()

	task := m.Create("list-keys")
	canceled, ok := m.Cancel(task.ID)
	if !ok || canceled.State != StateCanceled {
		t.Errorf("cancel = %q, ok = %v, want canceled", canceled.State, ok)
	}
	again, _ := m.Cancel(task.ID)
	if again.State != StateCanceled {
		t.Errorf("second cancel state = %q, want canceled (idempotent)", again.State)
	}
}
