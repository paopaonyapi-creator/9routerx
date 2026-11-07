// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "encoding/json/v2"

	"9routerx/proxy/internal/proxy"
)

func TestChatGPTWebExecutorDisabledByDefault(t *testing.T) {
	SetWebExecutorsEnabled(false)
	t.Setenv("ENABLE_WEB_EXECUTORS", "")

	req := &Request{
		APIKey: "test-token",
		Body:   []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
	}

	rec := httptest.NewRecorder()
	err := ForwardChatGPTWeb(rec, req)

	// The gate must refuse with 403 before any upstream call happens.
	if err == nil {
		t.Fatal("expected error when web executors disabled")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "web_executors_disabled") {
		t.Errorf("body = %s, want web_executors_disabled code", rec.Body.String())
	}
}

func TestChatGPTWebMissingSession(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	req := &Request{
		APIKey: "anonymous",
		Body:   []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
	}

	rec := httptest.NewRecorder()
	err := ForwardChatGPTWeb(rec, req)
	if err == nil {
		t.Fatal("expected error for missing session token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_session") {
		t.Errorf("body = %s, want missing_session", rec.Body.String())
	}
}

func TestChatGPTWebSentinelDetection(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"kind":"arkose_challenge","sentinel":true}}`))
	}))
	defer upstream.Close()

	// Point the executor at the test server by overriding the package-level
	// base URL through the request's client and ConnData baseUrl.
	req := &Request{
		APIKey:   "test-session-token",
		Client:   upstream.Client(),
		IsStream: true,
		Body:     []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		ConnData: map[string]any{
			"sessionToken": "test-session-token",
			"baseUrl":      upstream.URL,
		},
	}

	// The executor hardcodes the production URL; this test verifies the
	// sentinel-detection branch by hitting the live URL is impossible, so we
	// exercise the error path through a fake transport that always answers 403.
	fakeClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"kind":"arkose_challenge","sentinel":true}}`)),
				Header:     http.Header{},
			}
		}),
	}
	req.Client = fakeClient

	rec := httptest.NewRecorder()
	err := ForwardChatGPTWeb(rec, req)
	if err == nil {
		t.Fatal("expected sentinel error")
	}
	var ue *proxy.UpstreamError
	if !asUpstreamError(err, &ue) {
		t.Fatalf("err = %v, want *proxy.UpstreamError", err)
	}
	if ue.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", ue.StatusCode)
	}
	if !strings.Contains(string(ue.Body), "sentinel_required") {
		t.Errorf("body = %s, want sentinel_required", string(ue.Body))
	}
}

func TestChatGPTWebStreamingDeltas(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	// Upstream fixture: ChatGPT web SSE with cumulative message parts.
	frames := `data: {"message":{"id":"m1","content":{"content_type":"text","parts":["Hel"]}}}

data: {"message":{"id":"m1","content":{"content_type":"text","parts":["Hello"]}}}

data: {"message":{"id":"m1","content":{"content_type":"text","parts":["Hello world"]}}}

data: [DONE]

`

	fakeClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(frames)),
				Header:     http.Header{},
			}
		}),
	}

	req := &Request{
		APIKey:   "test-session-token",
		Client:   fakeClient,
		IsStream: true,
		Body:     []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	}

	rec := httptest.NewRecorder()
	err := ForwardChatGPTWeb(rec, req)
	if err != nil {
		t.Fatalf("ForwardChatGPTWeb: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("no SSE frames in output: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("missing [DONE] terminator")
	}

	// Verify deltas: "Hel" -> "lo" -> " world"
	wantDeltas := []string{`"content":"Hel"`, `"content":"lo"`, `"content":" world"`}
	for _, want := range wantDeltas {
		if !strings.Contains(body, want) {
			t.Errorf("missing delta chunk %s in:\n%s", want, body)
		}
	}
}

func TestChatGPTWebNonStreaming(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	frames := `data: {"message":{"id":"m1","content":{"content_type":"text","parts":["Hello"]}}}

data: {"message":{"id":"m1","content":{"content_type":"text","parts":["Hello world"]}}}

data: [DONE]

`

	fakeClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(frames)),
				Header:     http.Header{},
			}
		}),
	}

	req := &Request{
		APIKey:   "test-session-token",
		Client:   fakeClient,
		IsStream: false,
		Body:     []byte(`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`),
	}

	rec := httptest.NewRecorder()
	if err := ForwardChatGPTWeb(rec, req); err != nil {
		t.Fatalf("ForwardChatGPTWeb: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object = %s, want chat.completion", resp.Object)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("choices = %+v, want single completion 'Hello world'", resp.Choices)
	}
}

// roundTripFunc adapts a function into an http.RoundTripper.
type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

func asUpstreamError(err error, target **proxy.UpstreamError) bool {
	if ue, ok := err.(*proxy.UpstreamError); ok {
		*target = ue
		return true
	}
	return false
}
