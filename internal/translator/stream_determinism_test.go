// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package translator

import (
	"bytes"
	"strings"
	"testing"
)

// The streamed bytes are the product: the same turn must serialize to the
// same SSE frames on every run. json/v2 walks maps in Go's randomized order
// unless marshaled with Deterministic, so any event map that reaches the wire
// through a plain Marshal makes the whole stream byte-nondeterministic.

func TestFormatSSEIsByteDeterministic(t *testing.T) {
	event := map[string]any{
		"type":  "content_block_delta",
		"index": 3,
		"delta": map[string]any{"type": "text_delta", "text": "hi"},
	}
	first := formatSSE(event)
	for i := 0; i < 200; i++ {
		if got := formatSSE(event); got != first {
			t.Fatalf("formatSSE not deterministic:\nfirst: %s\ngot:   %s", first, got)
		}
	}
}

func TestToolCallTerminalFlushIsOrderDeterministic(t *testing.T) {
	// Two parallel tool calls in one delta, then a finish chunk. The terminal
	// flush walks state.ToolCalls — a map — so a plain range would emit the
	// (input_json_delta, content_block_stop) pairs in a random order per run.
	const runs = 40
	var first []byte
	for i := 0; i < runs; i++ {
		key := NewSessionKey("det-test")
		chunk1 := []byte(`{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[` +
			`{"index":0,"id":"call_a","type":"function","function":{"name":"alpha","arguments":""}},` +
			`{"index":1,"id":"call_b","type":"function","function":{"name":"beta","arguments":""}}]},"finish_reason":null}]}`)
		if _, err := TranslateOpenAIToClaudeStreamSession(key, chunk1); err != nil {
			t.Fatalf("chunk1: %v", err)
		}
		finish := []byte(`{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		out, err := TranslateOpenAIToClaudeStreamSession(key, finish)
		if err != nil {
			t.Fatalf("finish: %v", err)
		}
		ClearStreamState(key)
		if i == 0 {
			first = out
			continue
		}
		if !bytes.Equal(out, first) {
			t.Fatalf("terminal flush order differs between runs:\nfirst: %s\ngot:   %s", first, out)
		}
	}
}

func TestTranslateOpenAIToClaudeStreamToolPairsFollowBlockOrder(t *testing.T) {
	key := NewSessionKey("det-order")
	defer ClearStreamState(key)
	chunk1 := []byte(`{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[` +
		`{"index":0,"id":"call_a","type":"function","function":{"name":"alpha","arguments":"A1"}},` +
		`{"index":1,"id":"call_b","type":"function","function":{"name":"beta","arguments":"B2"}}]},"finish_reason":null}]}`)
	if _, err := TranslateOpenAIToClaudeStreamSession(key, chunk1); err != nil {
		t.Fatalf("chunk1: %v", err)
	}
	finish := []byte(`{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
	out, err := TranslateOpenAIToClaudeStreamSession(key, finish)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	s := string(out)
	alphaDelta := strings.Index(s, `"partial_json":"A1"`)
	betaDelta := strings.Index(s, `"partial_json":"B2"`)
	if alphaDelta == -1 || betaDelta == -1 {
		t.Fatalf("missing tool deltas in output: %s", s)
	}
	if alphaDelta > betaDelta {
		t.Fatalf("tool pairs emitted out of block order:\n%s", s)
	}
}
