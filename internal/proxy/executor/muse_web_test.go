// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "encoding/json/v2"
)

func TestMuseWebExecutorDisabledByDefault(t *testing.T) {
	SetWebExecutorsEnabled(false)
	t.Setenv("ENABLE_WEB_EXECUTORS", "")

	req := &Request{
		APIKey: "BDUSS=test-session-cookie",
		Body:   []byte(`{"model":"ernie-4.0","messages":[{"role":"user","content":"hi"}]}`),
	}

	rec := httptest.NewRecorder()
	err := ForwardMuseWeb(rec, req)

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

func TestMuseWebMissingSessionCookie(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	req := &Request{
		APIKey: "",
		Body:   []byte(`{"model":"ernie-4.0","messages":[{"role":"user","content":"hi"}]}`),
	}

	rec := httptest.NewRecorder()
	err := ForwardMuseWeb(rec, req)

	if err == nil {
		t.Fatal("expected error for missing session cookie")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_session") {
		t.Errorf("body = %s, want missing_session", rec.Body.String())
	}
}

func TestMuseWebStreamingResponse(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	frames := `data: {"result":"Ni"}

data: {"result":" hao"}

data: {"result":"!"}

data: [DONE]

`

	fakeClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) *http.Response {
			if !strings.Contains(r.Header.Get("Cookie"), "BDUSS=test-bduss") {
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Body:       io.NopCloser(strings.NewReader(`{"error":"unauthorized"}`)),
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(frames)),
				Header:     http.Header{},
			}
		}),
	}

	req := &Request{
		APIKey:   "test-bduss",
		Client:   fakeClient,
		IsStream: true,
		Body:     []byte(`{"model":"ernie-4.0","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	}

	rec := httptest.NewRecorder()
	err := ForwardMuseWeb(rec, req)
	if err != nil {
		t.Fatalf("ForwardMuseWeb error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: ") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("missing SSE framing: %s", body)
	}
	if !strings.Contains(body, `"content":"Ni"`) || !strings.Contains(body, `"content":" hao"`) {
		t.Errorf("missing expected streaming content: %s", body)
	}
}

func TestMuseWebNonStreamingResponse(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	frames := `data: {"result":"Hello from "}

data: {"result":"Baidu Muse Spark"}

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
		APIKey:   "BDUSS=my-token",
		Client:   fakeClient,
		IsStream: false,
		Body:     []byte(`{"model":"muse-web/muse-spark","stream":false,"messages":[{"role":"user","content":"ping"}]}`),
	}

	rec := httptest.NewRecorder()
	err := ForwardMuseWeb(rec, req)
	if err != nil {
		t.Fatalf("ForwardMuseWeb error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello from Baidu Muse Spark" {
		t.Errorf("unexpected choices: %+v", resp.Choices)
	}
}

func TestMuseMetaRemainsDistinct(t *testing.T) {
	// Verify that muse.go (Meta Model API) and muse_web.go (Baidu Web) remain isolated.
	metaExec := Get("muse")
	if metaExec == nil {
		t.Fatal("executor for 'muse' (Meta) not registered")
	}
	webExec := Get("muse-web")
	if webExec == nil {
		t.Fatal("executor for 'muse-web' (Baidu) not registered")
	}
}
