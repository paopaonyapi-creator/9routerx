// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"strings"
	"testing"

	"9routerx/proxy/internal/proxy"
)

// TestQoderFirstFrameErrors ports tests/unit/qoder-stream-errors.test.js:
// First-frame errors in Qoder's {statusCodeValue, body} stream are surfaced as
// proper UpstreamError instances rather than streamed as choices, and status codes
// are accurately mapped.
func TestQoderFirstFrameErrors(t *testing.T) {
	duplicateBody := `{"code":"103","message":"Duplicate request"}`

	t.Run("duplicate_request_surfaces_as_403", func(t *testing.T) {
		env := qoderEnvelope{
			StatusCodeValue: 403,
			StatusCode:      "FORBIDDEN",
			Body:            jsontext.Value(duplicateBody),
		}
		err := qoderFirstFrameError(env)
		var ue *proxy.UpstreamError
		if !errors.As(err, &ue) {
			t.Fatalf("expected UpstreamError, got %v", err)
		}
		if ue.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", ue.StatusCode)
		}
		if !strings.Contains(string(ue.Body), "Duplicate request") {
			t.Errorf("body missing error message: %s", string(ue.Body))
		}
	})

	t.Run("preserves_non_billing_http_status", func(t *testing.T) {
		for _, status := range []int{401, 429, 500, 503} {
			env := qoderEnvelope{
				StatusCodeValue: status,
				StatusCode:      "ERROR",
				Body:            jsontext.Value(`{"message":"upstream failure"}`),
			}
			err := qoderFirstFrameError(env)
			var ue *proxy.UpstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("status %d: expected UpstreamError, got %v", status, err)
			}
			if ue.StatusCode != status {
				t.Errorf("status = %d, want %d", ue.StatusCode, status)
			}
		}
	})

	t.Run("maps_invalid_status_to_502", func(t *testing.T) {
		for _, status := range []int{0, 302, 600} {
			env := qoderEnvelope{
				StatusCodeValue: status,
				StatusCode:      "ERROR",
				Body:            jsontext.Value(`{"message":"invalid status"}`),
			}
			err := qoderFirstFrameError(env)
			var ue *proxy.UpstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("status %d: expected UpstreamError, got %v", status, err)
			}
			if ue.StatusCode != http.StatusBadGateway {
				t.Errorf("status %d mapped to %d, want 502", status, ue.StatusCode)
			}
		}
	})

	t.Run("heartbeat_prefix_with_successful_frames", func(t *testing.T) {
		raw := ": keepalive\n\n" +
			envelope(t, 200, `{"id":"c1","choices":[{"index":0,"delta":{"content":"hello"}}]}`) +
			envelope(t, 200, `{"id":"c1","choices":[{"index":0,"delta":{"content":" world"}}]}`) +
			"data: [DONE]\n\n"

		out := rewrite(t, raw)
		got := frames(t, out)
		if len(got) != 2 {
			t.Fatalf("got %d frames, want 2", len(got))
		}
		c0 := got[0]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["content"]
		c1 := got[1]["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["content"]
		if c0 != "hello" || c1 != " world" {
			t.Errorf("content = %v + %v, want hello + world", c0, c1)
		}
	})
}
