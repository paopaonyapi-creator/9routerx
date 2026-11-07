// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"fmt"
	"math"
	"strings"
	"testing"

	json "encoding/json/v2"
)

func TestContextSqueezer(t *testing.T) {
	t.Run("leaves a fitting conversation untouched", func(t *testing.T) {
		msgs := []OpenAIMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		}
		r := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 100000})
		if r.Changed {
			t.Errorf("r.Changed = true, want false")
		}
		if r.Stats.Reason != "fits" {
			t.Errorf("reason = %s, want fits", r.Stats.Reason)
		}
	})

	t.Run("does nothing without a window", func(t *testing.T) {
		msgs := []OpenAIMessage{
			{Role: "user", Content: strings.Repeat("x", 50000)},
		}
		r := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 0})
		if r.Changed {
			t.Errorf("r.Changed = true, want false")
		}
		if r.Stats.Reason != "no-window" {
			t.Errorf("reason = %s, want no-window", r.Stats.Reason)
		}
	})

	t.Run("trims oldest turns, keeps system prompt and newest turn", func(t *testing.T) {
		msgs := make([]OpenAIMessage, 0, 42)
		msgs = append(msgs, OpenAIMessage{Role: "system", Content: "IMPORTANT SYSTEM PROMPT"})
		for i := 0; i < 40; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			msgs = append(msgs, OpenAIMessage{
				Role:    role,
				Content: fmt.Sprintf("turn %d: %s", i, strings.Repeat("y", 3000)),
			})
		}
		msgs = append(msgs, OpenAIMessage{Role: "user", Content: "latest question"})

		before := EstimateConversationTokens(msgs)
		r := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 10000})

		if !r.Changed {
			t.Fatalf("r.Changed = false, want true")
		}
		if r.Stats.TokensBefore != before {
			t.Errorf("tokensBefore = %d, want %d", r.Stats.TokensBefore, before)
		}
		if r.Stats.TokensAfter > r.Stats.TokensBefore {
			t.Errorf("tokensAfter %d > tokensBefore %d", r.Stats.TokensAfter, r.Stats.TokensBefore)
		}
		if r.Stats.DroppedTurns <= 0 {
			t.Errorf("droppedTurns = %d, want > 0", r.Stats.DroppedTurns)
		}
		if r.Messages[0].Content != "IMPORTANT SYSTEM PROMPT" {
			t.Errorf("system prompt not preserved at index 0")
		}
		if r.Messages[len(r.Messages)-1].Content != "latest question" {
			t.Errorf("newest turn not preserved at tail")
		}

		hasRecap := false
		for _, m := range r.Messages {
			if s, ok := m.Content.(string); ok && strings.HasPrefix(s, "[Context Squeezer]") {
				hasRecap = true
				break
			}
		}
		if !hasRecap {
			t.Errorf("recap message missing from squeezed conversation")
		}
	})

	t.Run("never mutates the input", func(t *testing.T) {
		msgs := make([]OpenAIMessage, 0, 31)
		msgs = append(msgs, OpenAIMessage{Role: "system", Content: "sys"})
		for i := 0; i < 30; i++ {
			msgs = append(msgs, OpenAIMessage{
				Role:    "user",
				Content: fmt.Sprintf("z%d: %s", i, strings.Repeat("z", 4000)),
			})
		}

		origBytes, _ := json.Marshal(msgs, json.Deterministic(true))
		SqueezeContext(msgs, SqueezeOptions{ContextWindow: 8000})
		afterBytes, _ := json.Marshal(msgs, json.Deterministic(true))

		if string(origBytes) != string(afterBytes) {
			t.Errorf("input messages slice was mutated by SqueezeContext")
		}
	})

	t.Run("keeps at least minTurns even under a tiny window", func(t *testing.T) {
		msgs := make([]OpenAIMessage, 0, 21)
		msgs = append(msgs, OpenAIMessage{Role: "system", Content: "sys"})
		for i := 0; i < 20; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			msgs = append(msgs, OpenAIMessage{
				Role:    role,
				Content: fmt.Sprintf("q%d: %s", i, strings.Repeat("q", 2000)),
			})
		}

		r := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 1000, MinTurns: 4})
		nonSystemOrRecap := 0
		for _, m := range r.Messages {
			if m.Role != "system" {
				nonSystemOrRecap++
			}
		}
		if nonSystemOrRecap < 4 {
			t.Errorf("non-system turns = %d, want at least 4", nonSystemOrRecap)
		}
	})

	t.Run("recap names the dropped roles", func(t *testing.T) {
		dropped := []OpenAIMessage{
			{Role: "user"},
			{Role: "assistant"},
			{Role: "tool"},
			{Role: "user"},
		}
		recap := BuildRecap(dropped)
		if !strings.Contains(recap, "2 user messages") {
			t.Errorf("recap missing '2 user messages': %s", recap)
		}
		if !strings.Contains(recap, "1 assistant reply") {
			t.Errorf("recap missing '1 assistant reply': %s", recap)
		}
		if !strings.Contains(recap, "1 tool result") {
			t.Errorf("recap missing '1 tool result': %s", recap)
		}
	})

	t.Run("SqueezeContextRequestBody", func(t *testing.T) {
		body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"` + strings.Repeat("a", 20000) + `"}]}`)
		out, changed, stats := SqueezeContextRequestBody(body, SqueezeOptions{ContextWindow: 2000})
		if !changed {
			t.Fatalf("changed = false, want true")
		}
		if stats.Reason != "trimmed" && stats.Reason != "floor" {
			t.Errorf("unexpected reason: %s", stats.Reason)
		}
		var req map[string]any
		if err := json.Unmarshal(out, &req); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		msgs := req["messages"].([]any)
		if len(msgs) == 0 {
			t.Fatalf("messages empty after squeeze")
		}
	})

	t.Run("NaN fillRatio falls back to the default instead of poisoning the budget", func(t *testing.T) {
		msgs := make([]OpenAIMessage, 0, 11)
		msgs = append(msgs, OpenAIMessage{Role: "system", Content: "sys"})
		for i := 0; i < 10; i++ {
			msgs = append(msgs, OpenAIMessage{Role: "user", Content: strings.Repeat("n", 3000)})
		}
		r := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 4000, FillRatio: math.NaN()})
		// A NaN budget (int(math.Floor(NaN)) is undefined) would evict to the
		// floor or worse; the default 0.75 must behave like a plain run.
		want := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 4000, FillRatio: DefaultFillRatio})
		if r.Stats.DroppedTurns != want.Stats.DroppedTurns || r.Stats.Reason != want.Stats.Reason {
			t.Errorf("NaN fillRatio = (%s, %d dropped), want default (%s, %d dropped)",
				r.Stats.Reason, r.Stats.DroppedTurns, want.Stats.Reason, want.Stats.DroppedTurns)
		}
	})

	t.Run("fillRatio above 1 is clamped to the whole window", func(t *testing.T) {
		msgs := []OpenAIMessage{
			{Role: "user", Content: strings.Repeat("m", 5000)},
		}
		// 2.0 on a 100-token window would size the budget at 200 and never
		// fire until the conversation is twice over the model's real limit.
		r := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 100, FillRatio: 2.0})
		if r.Stats.Reason == "fits" {
			t.Errorf("reason = fits; a ratio > 1 must not exceed the window")
		}
	})

	t.Run("mid-conversation system turns survive eviction with a recap in place", func(t *testing.T) {
		// system at 0 AND 3: the second system turn sits after two evictable
		// turns. The recap must land right after the leading system run and
		// the system at index 3 must still be present afterwards.
		msgs := []OpenAIMessage{
			{Role: "system", Content: "leading system"},
			{Role: "user", Content: "q1 " + strings.Repeat("x", 3000)},
			{Role: "assistant", Content: "a1 " + strings.Repeat("y", 3000)},
			{Role: "system", Content: "mid system: always answer in French"},
			{Role: "user", Content: "q2 latest"},
		}
		r := SqueezeContext(msgs, SqueezeOptions{ContextWindow: 1200, MinTurns: 2})
		if !r.Changed {
			t.Fatalf("changed = false, want true")
		}
		var midSystem, recap *OpenAIMessage
		for i := range r.Messages {
			m := &r.Messages[i]
			if s, ok := m.Content.(string); ok {
				if s == "mid system: always answer in French" {
					midSystem = m
				}
				if strings.HasPrefix(s, "[Context Squeezer]") {
					recap = m
				}
			}
		}
		if midSystem == nil {
			t.Fatalf("mid-conversation system turn was evicted: %+v", r.Messages)
		}
		if recap == nil {
			t.Fatalf("recap missing")
		}
		if r.Messages[0].Content != "leading system" {
			t.Errorf("leading system not at index 0")
		}
		if recap != &r.Messages[1] {
			t.Errorf("recap at index %d, want 1 (right after the leading system run)", indexOf(r.Messages, recap))
		}
	})
}

// indexOf returns the slice index the pointer points at, for error messages.
func indexOf(msgs []OpenAIMessage, m *OpenAIMessage) int {
	for i := range msgs {
		if &msgs[i] == m {
			return i
		}
	}
	return -1
}
