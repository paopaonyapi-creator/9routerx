// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package testingparity

import (
	"fmt"
	"strings"
	"testing"

	"9routerx/proxy/internal/translator"
)

// TestSerenehopeSqueezeContext ports the squeezeContext table from serenhope
// tests/plugins-json-guard.test.js (describe "contextSqueezer: squeezeContext")
// one row per `it()`. Serenhope-parity: same inputs, same verdicts, same
// stats fields.

func msg(role, content string) translator.OpenAIMessage {
	return translator.OpenAIMessage{Role: role, Content: content}
}

// TestSerenehopeSqueezeFittingUntouched ports "leaves a fitting conversation
// untouched": trimming is a repair, not an optimisation.
func TestSerenehopeSqueezeFittingUntouched(t *testing.T) {
	msgs := []translator.OpenAIMessage{
		msg("system", "sys"),
		msg("user", "hi"),
		msg("assistant", "hello"),
	}
	r := translator.SqueezeContext(msgs, translator.SqueezeOptions{ContextWindow: 100000})
	if r.Changed {
		t.Error("serenhope-parity: changed = true, want false")
	}
	if r.Stats.Reason != "fits" {
		t.Errorf("serenhope-parity: reason = %q, want \"fits\"", r.Stats.Reason)
	}
}

// TestSerenehopeSqueezeNoWindow ports "does nothing without a window".
func TestSerenehopeSqueezeNoWindow(t *testing.T) {
	msgs := []translator.OpenAIMessage{msg("user", strings.Repeat("x", 50000))}
	r := translator.SqueezeContext(msgs, translator.SqueezeOptions{ContextWindow: 0})
	if r.Changed {
		t.Error("serenhope-parity: changed = true, want false")
	}
	if r.Stats.Reason != "no-window" {
		t.Errorf("serenhope-parity: reason = %q, want \"no-window\"", r.Stats.Reason)
	}
}

// TestSerenehopeSqueezeTrimsOldestKeepsSystemAndLatest ports "trims the oldest
// turns, keeps the system prompt and the newest turn".
func TestSerenehopeSqueezeTrimsOldestKeepsSystemAndLatest(t *testing.T) {
	msgs := []translator.OpenAIMessage{msg("system", "IMPORTANT SYSTEM PROMPT")}
	for i := 0; i < 40; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, msg(role, fmt.Sprintf("turn %d: %s", i, strings.Repeat("y", 3000))))
	}
	msgs = append(msgs, msg("user", "latest question"))

	before := translator.EstimateConversationTokens(msgs)
	r := translator.SqueezeContext(msgs, translator.SqueezeOptions{ContextWindow: 10000})
	if !r.Changed {
		t.Fatal("serenhope-parity: changed = false, want true")
	}
	if r.Stats.TokensBefore != before {
		t.Errorf("serenhope-parity: tokensBefore = %d, want %d", r.Stats.TokensBefore, before)
	}
	if r.Stats.TokensAfter > r.Stats.TokensBefore {
		t.Errorf("serenhope-parity: tokensAfter = %d exceeds tokensBefore = %d", r.Stats.TokensAfter, r.Stats.TokensBefore)
	}
	if r.Stats.DroppedTurns <= 0 {
		t.Errorf("serenhope-parity: droppedTurns = %d, want > 0", r.Stats.DroppedTurns)
	}
	if first, _ := r.Messages[0].Content.(string); first != "IMPORTANT SYSTEM PROMPT" {
		t.Errorf("serenhope-parity: first message = %q, want the system prompt kept", first)
	}
	last := r.Messages[len(r.Messages)-1]
	if lastContent, _ := last.Content.(string); lastContent != "latest question" {
		t.Errorf("serenhope-parity: last message = %q, want \"latest question\"", lastContent)
	}
	hasRecap := false
	for _, m := range r.Messages {
		if c, ok := m.Content.(string); ok && strings.HasPrefix(c, "[Context Squeezer]") {
			hasRecap = true
			break
		}
	}
	if !hasRecap {
		t.Error("serenhope-parity: no [Context Squeezer] recap message inserted")
	}
}

// TestSerenehopeSqueezeNeverMutatesInput ports "never mutates the input": the
// caller decides when the trimmed copy replaces the original.
func TestSerenehopeSqueezeNeverMutatesInput(t *testing.T) {
	msgs := []translator.OpenAIMessage{msg("system", "sys")}
	for i := 0; i < 30; i++ {
		msgs = append(msgs, msg("user", fmt.Sprintf("z%d: %s", i, strings.Repeat("z", 4000))))
	}
	snapshot := canonicalJSON(t, msgs)
	translator.SqueezeContext(msgs, translator.SqueezeOptions{ContextWindow: 8000})
	if after := canonicalJSON(t, msgs); after != snapshot {
		t.Error("serenhope-parity: input messages were mutated by SqueezeContext")
	}
}

// TestSerenehopeSqueezeMinTurnsFloor ports "keeps at least minTurns even under
// a tiny window".
func TestSerenehopeSqueezeMinTurnsFloor(t *testing.T) {
	msgs := []translator.OpenAIMessage{msg("system", "sys")}
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, msg(role, fmt.Sprintf("q%d: %s", i, strings.Repeat("q", 2000))))
	}
	r := translator.SqueezeContext(msgs, translator.SqueezeOptions{ContextWindow: 1000, MinTurns: 4})
	turns := 0
	for _, m := range r.Messages {
		c, _ := m.Content.(string)
		if m.Role != "system" || !strings.HasPrefix(c, "[Context Squeezer]") {
			turns++
		}
	}
	if turns < 4 {
		t.Errorf("serenhope-parity: kept %d turns, want >= 4", turns)
	}
}

// TestSerenehopeSqueezeRecapNamesRoles ports "recap names the dropped roles".
func TestSerenehopeSqueezeRecapNamesRoles(t *testing.T) {
	recap := translator.BuildRecap([]translator.OpenAIMessage{
		{Role: "user"}, {Role: "assistant"}, {Role: "tool"}, {Role: "user"},
	})
	if !strings.Contains(recap, "2 user messages") {
		t.Errorf("serenhope-parity: recap missing \"2 user messages\": %s", recap)
	}
	if !strings.Contains(recap, "1 tool result") {
		t.Errorf("serenhope-parity: recap missing \"1 tool result\": %s", recap)
	}
}
