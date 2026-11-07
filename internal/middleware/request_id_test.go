// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDMiddleware_Generated(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := GetRequestID(r)
		if reqID == "" {
			t.Error("expected non-empty request ID from context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	headerReqID := rec.Header().Get("X-Request-ID")
	if headerReqID == "" {
		t.Error("expected X-Request-ID header in response")
	}
}

func TestRequestIDMiddleware_Preserved(t *testing.T) {
	incomingID := "custom-req-id-12345"
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := GetRequestID(r)
		if reqID != incomingID {
			t.Errorf("expected request ID %s, got %s", incomingID, reqID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("X-Request-ID", incomingID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	headerReqID := rec.Header().Get("X-Request-ID")
	if headerReqID != incomingID {
		t.Errorf("expected X-Request-ID header %s, got %s", incomingID, headerReqID)
	}
}

func TestGetRequestID_EdgeCases(t *testing.T) {
	if got := GetRequestID(nil); got != "" {
		t.Errorf("GetRequestID(nil) = %q, want empty", got)
	}
	if got := GetRequestIDFromContext(nil); got != "" {
		t.Errorf("GetRequestIDFromContext(nil) = %q, want empty", got)
	}
}

// The ID rides verbatim into every structured log line, so a client-supplied
// value carrying a newline is a forged log entry, and an unbounded one is a
// log-width lever. Control characters are dropped and the value is capped;
// clean values pass through unchanged.
func TestSanitizeRequestID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty generates", input: "", want: ""},
		{name: "clean id passes through", input: "req_abc123", want: "req_abc123"},
		{
			name:  "newline stripped (log forging)",
			input: "req_abc\nFAKE ERROR upstream key leaked",
			want:  "req_abcFAKE ERROR upstream key leaked",
		},
		{
			name:  "control characters stripped",
			input: "req_\x00\x1b[31mabc\r\n",
			want:  "req_[31mabc",
		},
		{name: "tab is a control character too", input: "req_\tabc", want: "req_abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeRequestID(tt.input); got != tt.want {
				t.Errorf("sanitizeRequestID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeRequestID_CapsLength(t *testing.T) {
	long := strings.Repeat("a", maxClientRequestIDLen+500)
	got := sanitizeRequestID(long)
	if len(got) != maxClientRequestIDLen {
		t.Errorf("sanitized length = %d, want %d", len(got), maxClientRequestIDLen)
	}
}

// The middleware must never adopt an unsanitized client value: whatever reaches
// the response header is what the logs record.
func TestRequestIDMiddleware_SanitizesClientHeader(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("X-Request-ID", "req_abc\nforged line")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "req_abcforged line" {
		t.Errorf("X-Request-ID = %q, want %q", got, "req_abcforged line")
	}
}
