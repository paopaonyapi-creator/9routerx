// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package antislop

import (
	"strings"
	"testing"
)

func TestGetPrompt(t *testing.T) {
	t.Run("lite_prompt_content_and_len", func(t *testing.T) {
		lite := GetPrompt(LevelLite)
		if len(lite) != 913 {
			t.Errorf("lite len = %d, want 913", len(lite))
		}
		if !strings.Contains(lite, "You use antislop: rules for AI coding agents") {
			t.Errorf("lite prompt missing expected header")
		}
	})

	t.Run("full_prompt_contains_core", func(t *testing.T) {
		full := GetPrompt(LevelFull)
		if len(full) < 40000 {
			t.Errorf("full len = %d, want >= 40000", len(full))
		}
		if !strings.Contains(full, "Runtime note: this text is injected by 9Router's Anti Slop plugin") {
			t.Errorf("full prompt missing runtime mode note")
		}
		if !strings.Contains(full, "## What This Is") {
			t.Errorf("full prompt missing core start")
		}
	})

	t.Run("ultra_prompt_contains_all_five_skills", func(t *testing.T) {
		ultra := GetPrompt(LevelUltra)
		if len(ultra) < 120000 {
			t.Errorf("ultra len = %d, want >= 120000", len(ultra))
		}
		for _, skill := range []string{
			"# antislop-ui",
			"# antislop-copywriting",
			"# antislop-human",
			"# antislop-layoutmobile",
			"# antislop-code",
		} {
			if !strings.Contains(ultra, skill) {
				t.Errorf("ultra prompt missing skill section: %s", skill)
			}
		}
	})

	t.Run("off_returns_empty", func(t *testing.T) {
		if got := GetPrompt(LevelOff); got != "" {
			t.Errorf("off level returned non-empty: %q", got)
		}
	})

	t.Run("unknown_level_falls_back_to_full", func(t *testing.T) {
		unknown := GetPrompt("unrecognized-level")
		full := GetPrompt(LevelFull)
		if unknown != full {
			t.Errorf("unknown level did not fall back to full")
		}
	})
}

func TestApplyAntiSlop(t *testing.T) {
	t.Run("applies_to_openai_chat", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"build a form"}]}`)
		out, did := ApplyAntiSlop(body, false, LevelLite)
		if !did {
			t.Fatalf("did = false, want true")
		}
		if !strings.Contains(string(out), "You use antislop") {
			t.Errorf("output missing antislop prompt: %s", string(out))
		}
	})

	t.Run("applies_to_claude_native", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"build a form"}]}`)
		out, did := ApplyAntiSlop(body, true, LevelLite)
		if !did {
			t.Fatalf("did = false, want true")
		}
		if !strings.Contains(string(out), `"system"`) {
			t.Errorf("output missing system field for Claude: %s", string(out))
		}
	})

	t.Run("off_level_returns_unchanged", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
		out, did := ApplyAntiSlop(body, false, LevelOff)
		if did {
			t.Errorf("did = true, want false for off level")
		}
		if string(out) != string(body) {
			t.Errorf("body modified under off level")
		}
	})
}
