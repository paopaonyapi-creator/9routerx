// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"strings"
	"testing"

	json "encoding/json/v2"
)

func TestToolCallRepair(t *testing.T) {
	t.Run("empty_string_arguments_become_braces", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "a", "function": map[string]any{"name": "Bash", "arguments": ""}},
					},
				},
			},
		}
		repaired := RepairToolPayload(body)
		tcs := repaired["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)
		fn := tcs[0].(map[string]any)["function"].(map[string]any)
		if fn["arguments"] != "{}" {
			t.Errorf("arguments = %v, want {}", fn["arguments"])
		}
	})

	t.Run("truncated_json_arguments_closed_up", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "a", "function": map[string]any{"name": "Bash", "arguments": `{"command":"ls`}},
					},
				},
			},
		}
		repaired := RepairToolPayload(body)
		tcs := repaired["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)
		fn := tcs[0].(map[string]any)["function"].(map[string]any)
		argsStr := fn["arguments"].(string)

		var parsed struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(argsStr), &parsed); err != nil {
			t.Fatalf("unmarshal repaired arguments error: %v (raw: %s)", err, argsStr)
		}
		if parsed.Command != "ls" {
			t.Errorf("command = %s, want ls", parsed.Command)
		}
	})

	t.Run("object_arguments_are_stringified", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "a", "function": map[string]any{"name": "Bash", "arguments": map[string]any{"command": "ls"}}},
					},
				},
			},
		}
		repaired := RepairToolPayload(body)
		tcs := repaired["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)
		fn := tcs[0].(map[string]any)["function"].(map[string]any)
		if fn["arguments"] != `{"command":"ls"}` {
			t.Errorf("arguments = %v, want {\"command\":\"ls\"}", fn["arguments"])
		}
	})

	t.Run("nameless_tool_calls_dropped", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "a", "function": map[string]any{}},
					},
				},
			},
		}
		repaired := RepairToolPayload(body)
		tcs := repaired["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)
		if len(tcs) != 0 {
			t.Errorf("tool_calls len = %d, want 0", len(tcs))
		}
	})

	t.Run("orphaned_tool_results_dropped", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "a", "function": map[string]any{"name": "Bash", "arguments": "{}"}},
					},
				},
				map[string]any{"role": "tool", "tool_call_id": "stale", "content": "error output"},
				map[string]any{"role": "tool", "tool_call_id": "a", "content": "paired output"},
			},
		}
		repaired := RepairToolPayload(body)
		msgs := repaired["messages"].([]any)
		if len(msgs) != 2 {
			t.Fatalf("messages count = %d, want 2 (orphan dropped)", len(msgs))
		}
		if msgs[1].(map[string]any)["tool_call_id"] != "a" {
			t.Errorf("remaining tool message = %v, want tool_call_id a", msgs[1])
		}
	})

	t.Run("orphaned_claude_tool_result_dropped", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"content": []any{
						map[string]any{"type": "tool_use", "id": "u1", "name": "Bash", "input": map[string]any{}},
					},
				},
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "tool_result", "tool_use_id": "gone", "content": "bad"},
					},
				},
			},
		}
		repaired := RepairToolPayload(body)
		msgs := repaired["messages"].([]any)
		c2 := msgs[1].(map[string]any)["content"].([]any)
		if len(c2) != 0 {
			t.Errorf("claude content blocks = %d, want 0", len(c2))
		}
	})

	t.Run("responses_orphan_function_call_output_dropped", func(t *testing.T) {
		body := map[string]any{
			"input": []any{
				map[string]any{"type": "function_call", "call_id": "c1", "name": "Bash", "arguments": "{}"},
				map[string]any{"type": "function_call_output", "call_id": "gone", "output": "ok"},
			},
		}
		repaired := RepairToolPayload(body)
		input := repaired["input"].([]any)
		if len(input) != 1 {
			t.Fatalf("responses input len = %d, want 1", len(input))
		}
	})

	t.Run("tool_choice_validation", func(t *testing.T) {
		// Unknown tool name in tool_choice is removed
		body := map[string]any{
			"tools": []any{
				map[string]any{"type": "function", "function": map[string]any{"name": "Read"}},
			},
			"tool_choice": map[string]any{
				"type": "tool", "function": map[string]any{"name": "Bash"},
			},
		}
		repaired := RepairToolPayload(body)
		if repaired["tool_choice"] != nil {
			t.Errorf("tool_choice should be removed when tool name is unknown")
		}

		// Forced tool_choice without tools is removed
		body2 := map[string]any{
			"messages":    []any{},
			"tool_choice": "required",
		}
		repaired2 := RepairToolPayload(body2)
		if repaired2["tool_choice"] != nil {
			t.Errorf("tool_choice should be removed when tools array is missing")
		}
	})
}

func TestIsToolCallRejection(t *testing.T) {
	if !IsToolCallRejection(400, "Invalid schema for function 'Bash': tools[0].parameters") {
		t.Error("expected true for schema 400")
	}
	if IsToolCallRejection(400, "This model's maximum context length is 8192 tokens") {
		t.Error("expected false for context length overflow")
	}
	if IsToolCallRejection(429, "rate limit exceeded") {
		t.Error("expected false for 429")
	}
	if IsToolCallRejection(400, "") {
		t.Error("expected false for empty message")
	}
}

func TestToolCallDegrade(t *testing.T) {
	t.Run("level_1_drops_tool_choice_and_strict", func(t *testing.T) {
		body := map[string]any{
			"tools": []any{
				map[string]any{
					"type":   "function",
					"strict": true,
					"function": map[string]any{
						"name":   "Bash",
						"strict": true,
						"parameters": map[string]any{
							"type": "object",
						},
					},
				},
			},
			"tool_choice": "required",
		}
		out, ok := DegradeToolPayload(body, 1)
		if !ok || out == nil {
			t.Fatalf("degrade level 1 failed")
		}
		if out["tool_choice"] != nil {
			t.Errorf("tool_choice not dropped in level 1")
		}
		tools := out["tools"].([]any)
		t0 := tools[0].(map[string]any)
		if t0["strict"] != nil {
			t.Errorf("strict on tool not dropped")
		}
		if fn := t0["function"].(map[string]any); fn["strict"] != nil {
			t.Errorf("strict on function not dropped")
		}
	})

	t.Run("level_2_strips_fragile_keywords", func(t *testing.T) {
		body := map[string]any{
			"tools": []any{
				map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": "Bash",
						"parameters": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"command": map[string]any{"type": "string", "const": "ls"},
								"list": map[string]any{
									"type":  "array",
									"items": map[string]any{"oneOf": []any{map[string]any{"type": "string"}}},
								},
							},
							"required": []any{"command"},
						},
					},
				},
			},
		}
		out, ok := DegradeToolPayload(body, 2)
		if !ok || out == nil {
			t.Fatalf("degrade level 2 failed")
		}
		params := out["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
		if params["additionalProperties"] != nil {
			t.Errorf("additionalProperties not stripped")
		}
		props := params["properties"].(map[string]any)
		cmd := props["command"].(map[string]any)
		if cmd["const"] != nil {
			t.Errorf("const not stripped")
		}
		list := props["list"].(map[string]any)["items"].(map[string]any)
		if list["oneOf"] != nil {
			t.Errorf("oneOf not stripped")
		}
		if cmd["type"] != "string" {
			t.Errorf("command type = %v, want string", cmd["type"])
		}
	})

	t.Run("level_3_removes_tools_and_inlines_history", func(t *testing.T) {
		body := map[string]any{
			"tools": []any{
				map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "Bash", "parameters": map[string]any{"type": "object"}},
				},
			},
			"messages": []any{
				map[string]any{"role": "user", "content": "list files"},
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{"id": "a", "function": map[string]any{"name": "Bash", "arguments": `{"command":"ls"}`}},
					},
				},
				map[string]any{"role": "tool", "tool_call_id": "a", "content": "package.json"},
			},
		}
		out, ok := DegradeToolPayload(body, 3)
		if !ok || out == nil {
			t.Fatalf("degrade level 3 failed")
		}
		if out["tools"] != nil {
			t.Errorf("tools not removed in level 3")
		}
		msgs := out["messages"].([]any)
		if len(msgs) != 3 {
			t.Fatalf("turn count = %d, want 3", len(msgs))
		}
		m1 := msgs[1].(map[string]any)
		if m1["tool_calls"] != nil {
			t.Errorf("tool_calls not removed from assistant turn")
		}
		if !strings.Contains(m1["content"].(string), "Bash") {
			t.Errorf("assistant content missing inlined tool name: %v", m1["content"])
		}
		m2 := msgs[2].(map[string]any)
		if !strings.Contains(m2["content"].(string), "package.json") {
			t.Errorf("tool result missing inlined content: %v", m2["content"])
		}
	})

	t.Run("degrade_beyond_max_level_returns_nil", func(t *testing.T) {
		body := map[string]any{"tools": []any{map[string]any{"name": "Bash"}}}
		out, ok := DegradeToolPayload(body, ToolFallbackMaxLevel+1)
		if ok || out != nil {
			t.Errorf("expected false/nil for level > max")
		}
	})

	t.Run("degrade_with_no_tools_returns_nil", func(t *testing.T) {
		body := map[string]any{"messages": []any{}}
		out, ok := DegradeToolPayload(body, 1)
		if ok || out != nil {
			t.Errorf("expected false/nil when body has no tools")
		}
	})
}
