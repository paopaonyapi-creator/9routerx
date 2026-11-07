// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package tokensaver

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

// The rewrites that re-marshal a request body (tool-result compression, per-key
// prompt injection) must not round >2^53 integers through float64 — a snowflake
// ID inside a tool_use input object or a message's other fields rides the same
// re-marshal even when the rewrite only touched a text block elsewhere.

const bigID = "9007199254740993" // 2^53+1, not representable as float64

func TestCompressMessagesPreservesLargeIntegersInsideMessages(t *testing.T) {
	bigContent := strings.Repeat("x", MinCompressSize+10)
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"run","input":{"repo_id":` + bigID + `}}]},
		{"role":"tool","tool_call_id":"t1","content":"` + bigContent + `"}
	]}`)

	out, changed := CompressMessages(body)
	if !changed {
		t.Fatal("expected compression to apply")
	}
	if !strings.Contains(string(out), bigID) {
		t.Fatalf("large integer lost precision inside messages: %s", out)
	}
}

func TestInjectKeySystemPromptPreservesLargeIntegers(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","trace":{"span_id":` + bigID + `},"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}]}`)
	out, changed := InjectKeySystemPrompt(body, false, "always answer in French")
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !strings.Contains(string(out), bigID) {
		t.Fatalf("large integer lost precision: %s", out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("re-marshal invalid: %v", err)
	}
	msgs, _ := m["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
}
