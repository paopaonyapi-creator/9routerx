// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The 403 a client receives when its key may not use the requested model is a
// WIRE CONTRACT. Upstream decolua/9router derives the whole envelope from the
// status (ERROR_TYPES[403] = {permission_error, insufficient_quota}); this
// gateway keeps upstream's type and message but carries the code
// "model_not_allowed" (port(serenhope) TASK-1.1), so a client can tell "wrong
// model" from a real quota rejection — a deliberate divergence from upstream
// recorded in .changes/939-serenhope-per-key-governance.md.
//
// These cases pin the whole body — the type, the code AND the exact message
// string — so the divergence cannot come back silently. A client that matches
// on the type, the code or the message breaks on a change, so the change has
// to be deliberate. The message is compared in full, not by substring: any
// wording that merely mentions the model would pass a substring check and
// still be a different body from upstream's.

func TestWriteModelAccessError_ForbiddenBodyMatchesUpstream(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{
			name:  "openai model",
			model: "gpt-4o",
			want:  `This API key is not allowed to use model "gpt-4o"`,
		},
		{
			name:  "provider qualified model",
			model: "openai/gpt-4o",
			want:  `This API key is not allowed to use model "openai/gpt-4o"`,
		},
		{
			name:  "combo name",
			model: "smart-mix",
			want:  `This API key is not allowed to use model "smart-mix"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeModelAccessError(rec, &ErrModelNotAllowed{Model: tt.model})

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal error body: %v", err)
			}
			if len(body) != 1 {
				t.Fatalf("body = %v, want only the error envelope", body)
			}
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("body = %v, want an error object", body)
			}
			// The envelope carries exactly these three fields, as upstream's
			// buildErrorBody does.
			if len(errObj) != 3 {
				t.Fatalf("error = %v, want exactly message, type and code", errObj)
			}
			if errObj["type"] != "permission_error" {
				t.Errorf("error.type = %v, want permission_error", errObj["type"])
			}
			if errObj["code"] != "model_not_allowed" {
				t.Errorf("error.code = %v, want model_not_allowed", errObj["code"])
			}
			msg, _ := errObj["message"].(string)
			if msg != tt.want {
				t.Errorf("error.message = %q, want %q", msg, tt.want)
			}
		})
	}
}
