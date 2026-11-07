// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"strings"
	"testing"

	"9routerx/proxy/internal/middleware"
	"9routerx/proxy/internal/models"
)

// fallback_key_system_prompt_test.go pins the handler-level wiring of the
// per-key system prompt (port(serenhope) TASK-1.5): the key's prompt is read
// from the request context, appended AFTER the global prefix/suffix (global
// first, per-key second), and a request without an authenticated key or with
// an empty prompt is left untouched.

func TestApplyTokenSaversPerKeySystemPrompt(t *testing.T) {
	const keyPrompt = "KEY-PROMPT-MARKER"
	const body = `{"messages":[{"role":"user","content":"hi"}]}`

	t.Run("per_key_prompt_is_injected", func(t *testing.T) {
		h, cleanup := setupHandlerForForward(t)
		defer cleanup()
		ctx := context.WithValue(context.Background(), middleware.ApiKeyContextKey,
			&models.APIKey{ID: "k1", SystemPrompt: keyPrompt})
		got, _, _, _ := h.applyTokenSavers(ctx, []byte(body), false)
		if !strings.Contains(string(got), keyPrompt) {
			t.Fatalf("body = %s, want the key prompt injected", got)
		}
	})

	t.Run("global_first_then_per_key", func(t *testing.T) {
		h, cleanup := setupHandlerForForward(t)
		defer cleanup()
		h.TokenSaver.SetSystemPrompt(true, "GLOBAL-HEAD", "GLOBAL-TAIL")
		ctx := context.WithValue(context.Background(), middleware.ApiKeyContextKey,
			&models.APIKey{ID: "k1", SystemPrompt: keyPrompt})
		got, _, _, _ := h.applyTokenSavers(ctx, []byte(body), false)
		s := string(got)
		head := strings.Index(s, "GLOBAL-HEAD")
		key := strings.Index(s, keyPrompt)
		tail := strings.Index(s, "GLOBAL-TAIL")
		if head < 0 || key < 0 || tail < 0 {
			t.Fatalf("body = %s, want all three prompt parts", s)
		}
		if !(head < tail && tail < key) {
			// "Global dulu": the operator's global prompt completes first —
			// prefix then suffix — and the key's own prompt appends after it,
			// exactly the order the injection chain runs in.
			t.Errorf("order = head@%d tail@%d key@%d, want global prompt completed first, then the key prompt", head, tail, key)
		}
	})

	t.Run("no_key_or_empty_prompt_is_untouched", func(t *testing.T) {
		h, cleanup := setupHandlerForForward(t)
		defer cleanup()
		if got, _, _, _ := h.applyTokenSavers(context.Background(), []byte(body), false); strings.Contains(string(got), keyPrompt) {
			t.Fatalf("body = %s, want untouched without a key", got)
		}
		ctx := context.WithValue(context.Background(), middleware.ApiKeyContextKey,
			&models.APIKey{ID: "k1", SystemPrompt: ""})
		got, _, _, _ := h.applyTokenSavers(ctx, []byte(body), false)
		if strings.Contains(string(got), keyPrompt) {
			t.Fatalf("body = %s, want untouched with an empty prompt", got)
		}
	})

	t.Run("claude_native_injects_into_system_field", func(t *testing.T) {
		h, cleanup := setupHandlerForForward(t)
		defer cleanup()
		ctx := context.WithValue(context.Background(), middleware.ApiKeyContextKey,
			&models.APIKey{ID: "k1", SystemPrompt: keyPrompt})
		claudeBody := `{"messages":[{"role":"user","content":"hi"}]}`
		got, _, _, _ := h.applyTokenSavers(ctx, []byte(claudeBody), true)
		s := string(got)
		if !strings.Contains(s, `"system"`) || !strings.Contains(s, keyPrompt) {
			t.Fatalf("body = %s, want a top-level system field with the key prompt", s)
		}
		if strings.Contains(s, `"role":"system"`) {
			t.Fatalf("body = %s, want no role:system turn for Anthropic", s)
		}
	})
}
