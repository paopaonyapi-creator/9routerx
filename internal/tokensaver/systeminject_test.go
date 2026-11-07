// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package tokensaver

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

// systeminject_test.go pins the per-key system-prompt injector
// (port(serenhope) TASK-1.5), following the matrix upstream
// tests/unit/system-inject.test.js runs: append to an existing system channel,
// create one when absent, exact-segment idempotency (never a substring
// fingerprint), the wire-shape dispatch order, and fail-open on garbage.

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal injected body: %v", err)
	}
	return m
}

const keyPrompt = "You are terse. Answer in one sentence."

func TestInjectKeySystemPromptChat(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	out, did := InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false, want a new system turn to be unshifted")
	}
	m := decodeBody(t, out)
	msgs := m["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 with the system turn at index 0", len(msgs))
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != keyPrompt {
		t.Fatalf("first = %v, want the injected system turn", first)
	}

	// An existing system turn is appended to, not replaced.
	body = []byte(`{"messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"hi"}]}`)
	out, did = InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false, want the prompt appended to the system turn")
	}
	m = decodeBody(t, out)
	sys := m["messages"].([]any)[0].(map[string]any)
	if sys["content"] != "Be brief.\n\n"+keyPrompt {
		t.Fatalf("content = %v, want append after a blank line", sys["content"])
	}

	// A developer turn is a system channel too.
	body = []byte(`{"messages":[{"role":"developer","content":"Be brief."}]}`)
	out, did = InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for a developer turn")
	}
	m = decodeBody(t, out)
	dev := m["messages"].([]any)[0].(map[string]any)
	if dev["content"] != "Be brief.\n\n"+keyPrompt {
		t.Fatalf("developer content = %v", dev["content"])
	}

	// A block-array content keeps its blocks and gains one.
	body = []byte(`{"messages":[{"role":"system","content":[{"type":"text","text":"Be brief."}]}]}`)
	out, did = InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for a block-array system turn")
	}
	m = decodeBody(t, out)
	blocks := m["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want the prompt as one more block", len(blocks))
	}
}

func TestInjectKeySystemPromptIdempotency(t *testing.T) {
	// Exact segment: a second injection is a no-op.
	body := []byte(`{"messages":[{"role":"system","content":"` + keyPrompt + `"}]}`)
	if _, did := InjectKeySystemPrompt(body, false, keyPrompt); did {
		t.Fatal("did = true, want an exact-segment match to be a no-op")
	}
	segmented := `{"messages":[{"role":"system","content":"Be brief.\n\n` + keyPrompt + `"}]}`
	if _, did := InjectKeySystemPrompt([]byte(segmented), false, keyPrompt); did {
		t.Fatal("did = true, want the prompt found as its own blank-line segment")
	}
	// A substring of unrelated text does NOT count as present (upstream pins
	// "never uses a first-100 fingerprint").
	substring := `{"messages":[{"role":"system","content":"Rules: ` + keyPrompt + ` and more"}]}`
	out, did := InjectKeySystemPrompt([]byte(substring), false, keyPrompt)
	if !did {
		t.Fatal("did = false, want a substring occurrence to still append")
	}
	m := decodeBody(t, out)
	sys := m["messages"].([]any)[0].(map[string]any)
	if sys["content"] != "Rules: "+keyPrompt+" and more\n\n"+keyPrompt {
		t.Fatalf("content = %v, want the prompt appended after the unrelated text", sys["content"])
	}
	// And repeating the whole injection converges: the second pass is a no-op.
	if _, did := InjectKeySystemPrompt(out, false, keyPrompt); did {
		t.Fatal("did = true on re-injection, want idempotency across retries")
	}
}

func TestInjectKeySystemPromptResponses(t *testing.T) {
	// The instructions string takes precedence over input[].
	body := []byte(`{"instructions":"Be brief.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	out, did := InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for an instructions body")
	}
	m := decodeBody(t, out)
	if m["instructions"] != "Be brief.\n\n"+keyPrompt {
		t.Fatalf("instructions = %v", m["instructions"])
	}
	// The input[] array is left alone when instructions carried the prompt.
	if len(m["input"].([]any)) != 1 {
		t.Fatalf("input was touched while instructions existed: %v", m["input"])
	}

	// input[] with no system item: one is unshifted in the item shape —
	// block array, never a bare string (Codex rejects input[0].content strings).
	body = []byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	out, did = InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for an input[] body")
	}
	m = decodeBody(t, out)
	items := m["input"].([]any)
	if len(items) != 2 {
		t.Fatalf("input = %d items, want the system item unshifted", len(items))
	}
	sys := items[0].(map[string]any)
	if sys["type"] != "message" || sys["role"] != "system" {
		t.Fatalf("item = %v, want a typed system message", sys)
	}

	// A bare string input stays untouched.
	body = []byte(`{"input":"plain text"}`)
	if out, did := InjectKeySystemPrompt(body, false, keyPrompt); did || string(out) != string(body) {
		t.Fatalf("string input was touched: did=%v out=%s", did, out)
	}
}

func TestInjectKeySystemPromptClaude(t *testing.T) {
	// claudeNative routes to the top-level system field.
	body := []byte(`{"system":"Be brief.","messages":[{"role":"user","content":"hi"}]}`)
	out, did := InjectKeySystemPrompt(body, true, keyPrompt)
	if !did {
		t.Fatal("did = false for a Claude system string")
	}
	m := decodeBody(t, out)
	if m["system"] != "Be brief.\n\n"+keyPrompt {
		t.Fatalf("system = %v", m["system"])
	}

	// Block array with a cache_control breakpoint: the prompt goes BEFORE the
	// last breakpoint so the breakpoint stays the final element.
	body = []byte(`{"system":[{"type":"text","text":"Be brief.","cache_control":{"type":"ephemeral"}}]}`)
	out, did = InjectKeySystemPrompt(body, true, keyPrompt)
	if !did {
		t.Fatal("did = false for a Claude block array")
	}
	m = decodeBody(t, out)
	blocks := m["system"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want the prompt inserted", len(blocks))
	}
	if last := blocks[len(blocks)-1].(map[string]any); last["cache_control"] == nil {
		t.Fatalf("last block = %v, want the cache_control breakpoint to stay last", last)
	}

	// claudeNative must NOT write a role:"system" turn into messages[].
	body = []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out, did = InjectKeySystemPrompt(body, true, keyPrompt)
	if !did {
		t.Fatal("did = false: Claude wants a system field created")
	}
	m = decodeBody(t, out)
	if m["system"] != keyPrompt {
		t.Fatalf("system = %v, want the field created", m["system"])
	}
	if len(m["messages"].([]any)) != 1 {
		t.Fatalf("messages grew under claudeNative: %v", m["messages"])
	}
}

func TestInjectKeySystemPromptGemini(t *testing.T) {
	// Sniffed via systemInstruction.
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	out, did := InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for a contents[] body")
	}
	m := decodeBody(t, out)
	sys := m["systemInstruction"].(map[string]any)
	if sys["parts"].([]any)[0].(map[string]any)["text"] != keyPrompt {
		t.Fatalf("systemInstruction = %v", sys)
	}

	// The snake_case spelling a body already uses wins.
	body = []byte(`{"system_instruction":{"parts":[{"text":"Be brief."}]}}`)
	out, did = InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for a snake_case body")
	}
	m = decodeBody(t, out)
	snake := m["system_instruction"].(map[string]any)
	if snake["parts"].([]any)[1].(map[string]any)["text"] != keyPrompt {
		t.Fatalf("snake_case parts = %v, want appended", snake)
	}
	if _, ok := m["systemInstruction"]; ok {
		t.Fatal("camelCase field created on a snake_case body")
	}

	// Antigravity's request wrapper is unwrapped.
	body = []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`)
	out, did = InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for a wrapped body")
	}
	m = decodeBody(t, out)
	inner := m["request"].(map[string]any)
	if inner["systemInstruction"] == nil {
		t.Fatalf("systemInstruction = %v, want it inside the wrapper", inner)
	}
}

func TestInjectKeySystemPromptKiro(t *testing.T) {
	body := []byte(`{"conversationState":{"history":[{"userInputMessage":{"content":"fix the bug"}}]}}`)
	out, did := InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for a Kiro body")
	}
	m := decodeBody(t, out)
	cs := m["conversationState"].(map[string]any)
	turn := cs["history"].([]any)[0].(map[string]any)["userInputMessage"].(map[string]any)
	if turn["content"] != "fix the bug\n\n"+keyPrompt {
		t.Fatalf("content = %v, want appended to the first user turn", turn["content"])
	}
	// No top-level systemPrompt is ever written: kiro.dev answers that field
	// with 400 REQUEST_BODY_INVALID.
	if _, ok := m["systemPrompt"]; ok {
		t.Fatal("top-level systemPrompt written on a Kiro body")
	}
	// currentMessage is the fallback turn.
	body = []byte(`{"conversationState":{"currentMessage":{"userInputMessage":{"content":"fix the bug"}}}}`)
	out, did = InjectKeySystemPrompt(body, false, keyPrompt)
	if !did {
		t.Fatal("did = false for a currentMessage body")
	}
	m = decodeBody(t, out)
	cur := m["conversationState"].(map[string]any)["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	if !strings.Contains(cur["content"].(string), keyPrompt) {
		t.Fatalf("content = %v", cur["content"])
	}
}

func TestInjectKeySystemPromptFailOpen(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("not json"),
		[]byte(`[1,2,3]`),       // not an object
		[]byte(`{}`),            // nothing injectable
		[]byte(`{"model":"x"}`), // OpenAI-shaped, no array
	}
	for i, body := range cases {
		out, did := InjectKeySystemPrompt(body, false, keyPrompt)
		if did {
			t.Errorf("case %d: did = true, want fail-open no-op", i)
		}
		if len(body) > 0 && string(out) != string(body) {
			t.Errorf("case %d: body changed on failure: %s", i, out)
		}
	}
	// An empty prompt is a no-op everywhere.
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if out, did := InjectKeySystemPrompt(body, false, ""); did || string(out) != string(body) {
		t.Error("empty prompt changed the body")
	}
	// A system field of an unexpected shape is left alone, not overwritten.
	body = []byte(`{"system":{"weird":"object"}}`)
	if out, did := InjectKeySystemPrompt(body, true, keyPrompt); did || string(out) != string(body) {
		t.Error("unexpected system shape was overwritten")
	}
}
