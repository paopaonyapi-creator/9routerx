// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"strings"
	"testing"

	"9routerx/proxy/internal/translator"
)

func TestWebCookieToolChoiceParsing(t *testing.T) {
	tests := []struct {
		name       string
		raw        any
		wantMode   string
		wantForced string
	}{
		{"nil", nil, "auto", ""},
		{"none", "none", "none", ""},
		{"required", "required", "required", ""},
		{"any", "any", "required", ""},
		{"auto", "auto", "auto", ""},
		{"named_object_function", map[string]any{"type": "function", "function": map[string]any{"name": "exec_cmd"}}, "required", "exec_cmd"},
		{"named_object_direct", map[string]any{"name": "fetch_url"}, "required", "fetch_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := ParseToolChoiceRule(tt.raw)
			if rule.Mode != tt.wantMode || rule.Forced != tt.wantForced {
				t.Errorf("ParseToolChoiceRule(%v) = %+v, want mode=%s, forced=%s", tt.raw, rule, tt.wantMode, tt.wantForced)
			}
		})
	}
}

func TestBuildWebCookieToolInstructionsAndReminder(t *testing.T) {
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "run_cmd",
				"description": "Run shell command",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	t.Run("marker_style_instructions", func(t *testing.T) {
		res := BuildWebCookieToolInstructions(tools, "auto", "marker")
		if !strings.Contains(res, "<<TOOL_CALL>>") {
			t.Errorf("expected <<TOOL_CALL>> in marker instructions: %s", res)
		}
		if !strings.Contains(res, "run_cmd") {
			t.Errorf("expected tool name in instructions: %s", res)
		}
	})

	t.Run("fenced_style_instructions", func(t *testing.T) {
		res := BuildWebCookieToolInstructions(tools, "required", "fenced")
		if !strings.Contains(res, "```tool_call") {
			t.Errorf("expected ```tool_call in fenced instructions: %s", res)
		}
		if !strings.Contains(res, "You MUST call one of the tools above") {
			t.Errorf("expected required rule in instructions: %s", res)
		}
	})

	t.Run("tool_choice_none_returns_empty", func(t *testing.T) {
		res := BuildWebCookieToolInstructions(tools, "none", "marker")
		if res != "" {
			t.Errorf("expected empty string when tool_choice is none, got %q", res)
		}
	})

	t.Run("tool_reminder", func(t *testing.T) {
		rem := BuildWebCookieToolReminder("marker", ToolChoiceRule{Mode: "required"})
		if !strings.Contains(rem, "System instruction - highest priority") {
			t.Errorf("missing priority line in reminder: %s", rem)
		}
		if !strings.Contains(rem, "This turn is FORCED") {
			t.Errorf("missing forced line in reminder: %s", rem)
		}
	})
}

func TestRenderWebCookieToolTranscript(t *testing.T) {
	messages := []translator.OpenAIMessage{
		{
			Role: "assistant",
			ToolCalls: []translator.OpenAIToolCall{
				{
					ID:   "c1",
					Type: "function",
					Function: translator.OpenAIFunctionCall{
						Name:      "run_cmd",
						Arguments: `{"cmd":"ls -la"}`,
					},
				},
			},
		},
		{
			Role:    "tool",
			Content: "file1.txt\nfile2.txt",
		},
	}

	rendered := RenderWebCookieToolTranscript(messages, "marker")
	if !strings.Contains(rendered, "<<TOOL_CALL>>") || !strings.Contains(rendered, "run_cmd") {
		t.Errorf("missing tool call in transcript: %s", rendered)
	}
	if !strings.Contains(rendered, "<<TOOL_RESULT>> tool: file1.txt") {
		t.Errorf("missing tool result in transcript: %s", rendered)
	}
}

func TestParseWebCookieToolCallReply(t *testing.T) {
	t.Run("parses_marker_style", func(t *testing.T) {
		text := "I will run the command for you.\n<<TOOL_CALL>> {\"name\": \"run_cmd\", \"arguments\": {\"cmd\": \"pwd\"}} <<TOOL_CALL>>\nPlease wait."
		content, calls := ParseWebCookieToolCallReply(text)
		if len(calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(calls))
		}
		if calls[0].Name != "run_cmd" || calls[0].Arguments["cmd"] != "pwd" {
			t.Errorf("unexpected call: %+v", calls[0])
		}
		if strings.Contains(content, "<<TOOL_CALL>>") {
			t.Errorf("content still contains markers: %s", content)
		}
	})

	t.Run("parses_fenced_style", func(t *testing.T) {
		text := "Executing tool:\n```tool_call\n{\"name\": \"view_file\", \"arguments\": {\"path\": \"README.md\"}}\n```\nDone."
		content, calls := ParseWebCookieToolCallReply(text)
		if len(calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(calls))
		}
		if calls[0].Name != "view_file" || calls[0].Arguments["path"] != "README.md" {
			t.Errorf("unexpected call: %+v", calls[0])
		}
		if strings.Contains(content, "```tool_call") {
			t.Errorf("content still contains fence: %s", content)
		}
	})

	t.Run("parses_raw_json_brace_format", func(t *testing.T) {
		text := "Here is the call: {\"name\": \"calc\", \"arguments\": {\"expr\": \"2+2\"}}"
		_, calls := ParseWebCookieToolCallReply(text)
		if len(calls) != 1 || calls[0].Name != "calc" {
			t.Fatalf("failed to parse raw brace call: %+v", calls)
		}
	})
}
