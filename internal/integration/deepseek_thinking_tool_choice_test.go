//go:build integration

// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package integration

import (
	json "encoding/json/v2"
	"net/http"
	"testing"
)

// TestDeepSeekThinkingRelaxesForcedToolChoice pins the DeepSeek chat-lane
// behavior for the upstream 400 "Thinking mode does not support this
// tool_choice": V4 models think by default, and the API rejects required/named
// tool_choice while thinking is on. The gateway must downgrade the pin to
// "auto" before dispatch. A request that explicitly disabled thinking keeps
// its forced choice. A request that declares no tools at all has its dangling
// "required" dropped entirely by the tool-payload repair pass
// (translator.RepairToolPayload drops a required/any choice with no tools —
// the same policy every other provider on this lane is served under); the
// DeepSeek relaxation itself leaves no-tools bodies untouched, so the drop is
// the repair's doing, not a thinking-specific downgrade.
func TestDeepSeekThinkingRelaxesForcedToolChoice(t *testing.T) {
	tests := []struct {
		name        string
		toolChoice  any
		thinking    any
		noTools     bool
		wantForward any
	}{
		{name: "forced required becomes auto", toolChoice: "required", wantForward: "auto"},
		{name: "named object becomes auto", toolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "probe"}}, wantForward: "auto"},
		{name: "explicit disabled thinking keeps forced choice", toolChoice: "required", thinking: map[string]any{"type": "disabled"}, wantForward: "required"},
		{name: "none passes through", toolChoice: "none", wantForward: "none"},
		{name: "auto passes through", toolChoice: "auto", wantForward: "auto"},
		{name: "no tools drops the dangling required", toolChoice: "required", noTools: true, wantForward: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			upstream := env.NewUpstream(t, chatCompletionResponder())
			env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

			body := map[string]any{
				"model":       "deepseek/deepseek-v4-flash",
				"messages":    []map[string]any{{"role": "user", "content": "hi"}},
				"tool_choice": tt.toolChoice,
			}
			if !tt.noTools {
				body["tools"] = []map[string]any{{
					"type":     "function",
					"function": map[string]any{"name": "probe", "parameters": map[string]any{"type": "object"}},
				}}
			}
			if tt.thinking != nil {
				body["thinking"] = tt.thinking
			}

			res := env.Post(t, "/v1/chat/completions", body)
			if res.Status != http.StatusOK {
				t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
			}

			sent := upstream.Last(t)
			var forwarded map[string]any
			if err := json.Unmarshal(sent.Body, &forwarded); err != nil {
				t.Fatalf("decode forwarded body: %v", err)
			}
			if got := forwarded["tool_choice"]; got != tt.wantForward {
				t.Errorf("tool_choice on the wire = %#v, want %#v (body: %s)", got, tt.wantForward, sent.Body)
			}
		})
	}
}

// TestDeepSeekComboRelaxesForcedToolChoice pins the same contract through the
// combo path, which is how the reported 400 reached the user: the request
// named a combo ("smart-combo"), and the combo's member was this deepseek
// model. The combo forwarder calls tryForwardWithConnection directly, so the
// relaxation must survive that route too.
func TestDeepSeekComboRelaxesForcedToolChoice(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")
	env.AddCombo(t, "combo-deepseek", "smart-combo", []string{"deepseek/deepseek-v4-flash"})

	body := map[string]any{
		"model":    "smart-combo",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"tools": []map[string]any{{
			"type":     "function",
			"function": map[string]any{"name": "probe", "parameters": map[string]any{"type": "object"}},
		}},
		"tool_choice": "required",
	}

	res := env.Post(t, "/v1/chat/completions", body)
	if res.Status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions via combo = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	sent := upstream.Last(t)
	var forwarded map[string]any
	if err := json.Unmarshal(sent.Body, &forwarded); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if got := forwarded["tool_choice"]; got != "auto" {
		t.Errorf("tool_choice on the wire through the combo = %#v, want auto (body: %s)", got, sent.Body)
	}
}
