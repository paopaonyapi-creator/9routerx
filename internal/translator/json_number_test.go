// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"strings"
	"testing"

	json "encoding/json/v2"
)

// The repair/degrade/squeeze passes re-marshal request bodies that ride
// through map[string]any. A plain v2 decode coerces every number to float64,
// silently rounding integer literals above 2^53 — snowflake IDs inside tool
// call arguments chief among them. These tests pin the preservation.

const bigID = "9007199254740993" // 2^53+1, not representable as float64

func TestRepairToolPayloadBytesPreservesLargeIntegers(t *testing.T) {
	t.Run("object arguments keep their integer literals", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":{"user_id":` + bigID + `}}}]}]}`)
		out, changed := RepairToolPayloadBytes(body)
		if !changed {
			t.Fatalf("changed = false, want true (arguments object normalized to string)")
		}
		if !strings.Contains(string(out), bigID) {
			t.Fatalf("large integer lost precision: %s", out)
		}
	})

	t.Run("claude tool_use input keeps its integer literals", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"y"},{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"get","input":{"id":` + bigID + `}}]}]}`)
		out, changed := RepairToolPayloadBytes(body)
		if !changed {
			t.Fatalf("changed = false, want true (tool_result orphan pairing)")
		}
		if !strings.Contains(string(out), bigID) {
			t.Fatalf("large integer lost precision: %s", out)
		}
	})

	t.Run("string arguments keep their integer literals", func(t *testing.T) {
		args := `{"user_id":` + bigID + `}`
		body := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":` + strconvQuote(args) + `}}]}]}`)
		out, _ := RepairToolPayloadBytes(body)
		// The arguments string rides opaquely; only the literal must survive.
		if !strings.Contains(string(out), bigID) {
			t.Fatalf("large integer lost precision inside arguments string: %s", out)
		}
	})

	t.Run("tool-less body is returned untouched", func(t *testing.T) {
		body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"temperature":0.7}`)
		out, changed := RepairToolPayloadBytes(body)
		if changed {
			t.Fatalf("changed = true for a body with no tool machinery")
		}
		if string(out) != string(body) {
			t.Fatalf("tool-less body was re-marshaled: %s", out)
		}
	})
}

func TestDegradeToolPayloadBytesPreservesLargeIntegers(t *testing.T) {
	// minimum is not one of the fragile schema keywords level 2 strips, so
	// the literal must survive both the strip pass and the re-marshal.
	body := []byte(`{"tools":[{"type":"function","function":{"name":"get","parameters":{"type":"object","properties":{"id":{"type":"integer","minimum":` + bigID + `}}}}}],"messages":[{"role":"user","content":"hi"}]}`)
	out, ok := DegradeToolPayloadBytes(body, 2)
	if !ok {
		t.Fatal("degrade level 2 failed")
	}
	if !strings.Contains(string(out), bigID) {
		t.Fatalf("large integer lost precision in schema: %s", out)
	}
}

func TestSqueezeContextRequestBodyPreservesLargeIntegers(t *testing.T) {
	// String content long enough that pass 1 shortens it (the only way a
	// single-message conversation reports changed). The >2^53 integer sits in
	// a top-level field, which the body map round-trips verbatim — the thing
	// the numbered decode protects. (Per-message fields are decoded into the
	// typed OpenAIMessage struct, which carries none.)
	body := []byte(`{"model":"gpt-4o","metadata":{"ref":` + bigID + `},"messages":[{"role":"user","content":"` + strings.Repeat("a", 20000) + `"}]}`)
	out, changed, _ := SqueezeContextRequestBody(body, SqueezeOptions{ContextWindow: 2000})
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !strings.Contains(string(out), bigID) {
		t.Fatalf("large integer lost precision: %s", out)
	}
}

// strconvQuote renders s as a JSON string literal for embedding in a test body.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestDegradeToolPayloadLevel3KeepsNonTextBlocks(t *testing.T) {
	t.Run("user image blocks survive the flatten", func(t *testing.T) {
		body := map[string]any{
			"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "Bash"}}},
			"messages": []any{
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "text", "text": "what is this?"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
				}},
				map[string]any{"role": "assistant", "tool_calls": []any{
					map[string]any{"id": "a", "function": map[string]any{"name": "Bash", "arguments": `{"command":"ls"}`}},
				}},
				map[string]any{"role": "tool", "tool_call_id": "a", "content": "out"},
			},
		}
		out, ok := DegradeToolPayload(body, 3)
		if !ok {
			t.Fatal("degrade level 3 failed")
		}
		msgs := out["messages"].([]any)
		user := msgs[0].(map[string]any)
		blocks, isArr := user["content"].([]any)
		if !isArr {
			t.Fatalf("user content flattened to %T; image_url block dropped", user["content"])
		}
		found := false
		for _, b := range blocks {
			if m, ok := b.(map[string]any); ok && m["type"] == "image_url" {
				found = true
			}
		}
		if !found {
			t.Fatalf("image_url block lost on level-3 degrade: %v", blocks)
		}
	})

	t.Run("assistant content array keeps images alongside the tool note", func(t *testing.T) {
		body := map[string]any{
			"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "Bash"}}},
			"messages": []any{
				map[string]any{"role": "user", "content": "go"},
				map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/y.png"}},
				}, "tool_calls": []any{
					map[string]any{"id": "a", "function": map[string]any{"name": "Bash", "arguments": `{}`}},
				}},
			},
		}
		out, ok := DegradeToolPayload(body, 3)
		if !ok {
			t.Fatal("degrade level 3 failed")
		}
		msgs := out["messages"].([]any)
		asst := msgs[1].(map[string]any)
		blocks, isArr := asst["content"].([]any)
		if !isArr {
			t.Fatalf("assistant content flattened to %T; image lost", asst["content"])
		}
		hasImage, hasNote := false, false
		for _, b := range blocks {
			m := b.(map[string]any)
			if m["type"] == "image_url" {
				hasImage = true
			}
			if s, ok := m["text"].(string); ok && strings.Contains(s, "[Called tools: Bash]") {
				hasNote = true
			}
		}
		if !hasImage || !hasNote {
			t.Fatalf("image=%v note=%v blocks=%v", hasImage, hasNote, blocks)
		}
	})

	t.Run("text-only content still inlines to a string", func(t *testing.T) {
		body := map[string]any{
			"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "Bash"}}},
			"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "plain"}}}},
		}
		out, ok := DegradeToolPayload(body, 3)
		if !ok {
			t.Fatal("degrade level 3 failed")
		}
		msgs := out["messages"].([]any)
		user := msgs[0].(map[string]any)
		if s, isStr := user["content"].(string); !isStr || s != "plain" {
			t.Fatalf("text-only content not inlined: %v", user["content"])
		}
	})
}
