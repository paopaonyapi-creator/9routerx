// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package thinking

import (
	"slices"
	"testing"

	"9routerx/proxy/internal/providers"
)

// TestOpenCodeFreeGLMThinkingFormat ports tests/unit/opencode-free-glm-thinking-format.test.js:
// glm-5.3-flash-free on OpenCode/OpenCode-Zen uses OpenAI reasoning_effort and never zai thinking,
// while native zai models (glm/glm-cn) keep the zai thinking object.
func TestOpenCodeFreeGLMThinkingFormat(t *testing.T) {
	lanes := []string{"opencode", "opencode-zen"}

	for _, provider := range lanes {
		t.Run(provider+"_sends_reasoning_effort_never_zai_thinking", func(t *testing.T) {
			body := map[string]any{
				"reasoning_effort": "high",
				"messages": []any{
					map[string]any{"role": "user", "content": "hi"},
				},
			}
			out, _ := Apply(body, "openai", provider, "glm-5.3-flash-free", nil)
			if out["thinking"] != nil {
				t.Errorf("%s: thinking must be nil for glm-5.3-flash-free, got %v", provider, out["thinking"])
			}
			if out["reasoning_effort"] != "high" {
				t.Errorf("%s: reasoning_effort = %v, want high", provider, out["reasoning_effort"])
			}
		})

		t.Run(provider+"_thinking_levels", func(t *testing.T) {
			levels := providers.GetThinkingLevels(provider, "glm-5.3-flash-free")
			if !slices.Contains(levels, "high") {
				t.Errorf("%s: levels should contain high: %v", provider, levels)
			}
			if slices.Contains(levels, "thinking") {
				t.Errorf("%s: levels should not contain zai 'thinking'", provider)
			}
			if slices.Contains(levels, "none") {
				t.Errorf("%s: levels should not contain 'none' (thinkingCanDisable: false)", provider)
			}
		})
	}

	t.Run("does_not_regress_zai_served_glm_flash", func(t *testing.T) {
		caps := providers.GetCapabilitiesForModel("glm", "glm-5.3-flash")
		if caps.ThinkingFormat != "zai" {
			t.Errorf("glm-5.3-flash thinkingFormat = %q, want zai", caps.ThinkingFormat)
		}
		body := map[string]any{
			"reasoning_effort": "high",
		}
		out, _ := Apply(body, "openai", "glm", "glm-5.3-flash", nil)
		thinkingObj, ok := out["thinking"].(map[string]any)
		if !ok || thinkingObj["type"] != "enabled" {
			t.Errorf("thinking = %v, want {type: enabled}", out["thinking"])
		}
		if out["reasoning_effort"] != nil {
			t.Errorf("reasoning_effort should be stripped for zai format, got %v", out["reasoning_effort"])
		}
	})
}
