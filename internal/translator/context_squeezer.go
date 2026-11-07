// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	json "encoding/json/v2"
)

// Constants matching open-sse/translator/concerns/contextSqueezer.js
const (
	DefaultFillRatio = 0.75
	LongContentChars = 4000
	MinKeepChars     = 400
	DefaultMinTurns  = 4
)

// SqueezeOptions parameterizes conversation trimming.
type SqueezeOptions struct {
	ContextWindow int     `json:"contextWindow"`
	FillRatio     float64 `json:"fillRatio,omitempty"`
	MinTurns      int     `json:"minTurns,omitempty"`
}

// SqueezeStats reports metrics and reason for the trim operation.
type SqueezeStats struct {
	TokensBefore      int    `json:"tokensBefore"`
	TokensAfter       int    `json:"tokensAfter"`
	DroppedTurns      int    `json:"droppedTurns"`
	ShortenedMessages int    `json:"shortenedMessages"`
	RecapChars        int    `json:"recapChars"`
	Reason            string `json:"reason"` // "no-window", "fits", "floor", "trimmed"
}

// SqueezeResult returns the resulting message list and telemetry.
type SqueezeResult struct {
	Messages []OpenAIMessage `json:"messages"`
	Changed  bool            `json:"changed"`
	Stats    SqueezeStats    `json:"stats"`
}

// EstimateTokens provides a lightweight ~4 chars per token estimate.
func EstimateTokens(value any) int {
	if value == nil {
		return 0
	}
	var text string
	switch v := value.(type) {
	case string:
		text = v
	default:
		b, err := json.Marshal(v)
		if err == nil {
			text = string(b)
		}
	}
	return int(math.Ceil(float64(len(text)) / 4.0))
}

// EstimateMessageTokens estimates token cost of one message including tool calls and content blocks.
func EstimateMessageTokens(m OpenAIMessage) int {
	var parts []any
	switch c := m.Content.(type) {
	case string:
		parts = append(parts, c)
	case []any:
		// Field extraction only. Marshaling the whole array here counted
		// every text twice AND charged base64 image payloads as text tokens,
		// inflating the estimate enough to evict turns that would have fit.
		// Nil entries fail the map assertion and are skipped.
		for _, blk := range c {
			bMap, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := bMap["text"].(string); ok {
				parts = append(parts, t)
			}
			if in, ok := bMap["input"]; ok {
				parts = append(parts, in)
			}
			if cs, ok := bMap["content"].(string); ok {
				parts = append(parts, cs)
			}
		}
	case []OpenAIContentBlock:
		for _, b := range c {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
	}
	if m.ReasoningContent != "" {
		parts = append(parts, m.ReasoningContent)
	}
	if len(m.ToolCalls) > 0 {
		parts = append(parts, m.ToolCalls)
	}
	if m.ToolCallID != "" {
		parts = append(parts, m.ToolCallID)
	}
	return EstimateTokens(parts) + 4 // role + frame
}

// EstimateConversationTokens sums token estimates for every message.
func EstimateConversationTokens(messages []OpenAIMessage) int {
	sum := 0
	for _, m := range messages {
		sum += EstimateMessageTokens(m)
	}
	return sum
}

// BuildRecap constructs the system recap line naming dropped turns.
func BuildRecap(dropped []OpenAIMessage) string {
	userCount := 0
	assistantCount := 0
	toolCount := 0
	otherCount := 0
	for _, m := range dropped {
		switch m.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		case "tool":
			toolCount++
		default:
			if m.ToolCallID != "" {
				toolCount++
			} else {
				otherCount++
			}
		}
	}
	var parts []string
	if userCount > 0 {
		s := "s"
		if userCount == 1 {
			s = ""
		}
		parts = append(parts, fmt.Sprintf("%d user message%s", userCount, s))
	}
	if assistantCount > 0 {
		repl := "replies"
		if assistantCount == 1 {
			repl = "reply"
		}
		parts = append(parts, fmt.Sprintf("%d assistant %s", assistantCount, repl))
	}
	if toolCount > 0 {
		s := "s"
		if toolCount == 1 {
			s = ""
		}
		parts = append(parts, fmt.Sprintf("%d tool result%s", toolCount, s))
	}
	if otherCount > 0 {
		s := "s"
		if otherCount == 1 {
			s = ""
		}
		parts = append(parts, fmt.Sprintf("%d other turn%s", otherCount, s))
	}
	what := "earlier turns"
	if len(parts) > 0 {
		what = strings.Join(parts, ", ")
	}
	s := "s"
	if len(dropped) == 1 {
		s = ""
	}
	return fmt.Sprintf("[Context Squeezer] %d earlier turn%s were dropped to fit this model's context window: %s. The turns below are verbatim and current. If the dropped history matters, ask the user to restate it.",
		len(dropped), s, what)
}

func shortenMessage(m *OpenAIMessage, charsToCut int) bool {
	str, ok := m.Content.(string)
	if !ok {
		return false
	}
	cut := charsToCut
	maxCut := len(str) - MinKeepChars
	if maxCut < 0 {
		maxCut = 0
	}
	if cut > maxCut {
		cut = maxCut
	}
	if cut <= 0 {
		return false
	}
	keepChars := len(str) - cut
	headChars := int(math.Floor(float64(keepChars) * 0.7))
	if headChars < MinKeepChars {
		headChars = MinKeepChars
	}
	if headChars > len(str) {
		headChars = len(str)
	}
	// Snap to rune boundaries: a raw byte slice through a multi-byte rune
	// would ship invalid UTF-8 to the upstream, which json then silently
	// replaces with U+FFFD.
	for headChars > 0 && headChars < len(str) && !utf8.RuneStart(str[headChars]) {
		headChars--
	}
	head := str[:headChars]
	tailLen := keepChars - headChars
	if tailLen < 0 {
		tailLen = 0
	}
	tailStart := len(str) - tailLen
	for tailStart < len(str) && !utf8.RuneStart(str[tailStart]) {
		tailStart++
	}
	tail := str[tailStart:]
	elided := len(str) - len(head) - len(tail)
	m.Content = fmt.Sprintf("%s\n[... %d characters elided by Context Squeezer ...]\n%s", head, elided, tail)
	return true
}

// SqueezeContext trims messages to fit into contextWindow tokens.
// Port of open-sse/translator/concerns/contextSqueezer.js squeezeContext.
func SqueezeContext(messages []OpenAIMessage, opts SqueezeOptions) SqueezeResult {
	if len(messages) == 0 || opts.ContextWindow <= 0 {
		return SqueezeResult{
			Messages: messages,
			Changed:  false,
			Stats:    SqueezeStats{Reason: "no-window"},
		}
	}

	fillRatio := opts.FillRatio
	// NaN fails the `<= 0` test and would poison the budget into an
	// int-conversion overflow; > 1 would size the budget past the window the
	// model actually has. Both clamp to safe values.
	if math.IsNaN(fillRatio) || fillRatio <= 0 {
		fillRatio = DefaultFillRatio
	} else if fillRatio > 1 {
		fillRatio = 1
	}
	minTurns := opts.MinTurns
	if minTurns <= 0 {
		minTurns = DefaultMinTurns
	}

	budget := int(math.Floor(float64(opts.ContextWindow) * fillRatio))
	tokensBefore := EstimateConversationTokens(messages)
	if tokensBefore <= budget {
		return SqueezeResult{
			Messages: messages,
			Changed:  false,
			Stats: SqueezeStats{
				TokensBefore: tokensBefore,
				TokensAfter:  tokensBefore,
				Reason:       "fits",
			},
		}
	}

	// Deep copy to never mutate input
	working := make([]OpenAIMessage, len(messages))
	copy(working, messages)

	// Leading system messages are never dropped
	firstTurn := 0
	for firstTurn < len(working) && working[firstTurn].Role == "system" {
		firstTurn++
	}

	// Pass 1: Shorten oversized content in kept turns
	shortened := 0
	for i := firstTurn; i < len(working); i++ {
		m := &working[i]
		str, ok := m.Content.(string)
		if !ok || len(str) < LongContentChars {
			continue
		}
		budgetCap := (budget / 4) * 4
		cost := EstimateMessageTokens(*m)
		if cost > budgetCap {
			if shortenMessage(m, (cost-budgetCap)*4) {
				shortened++
			}
		}
	}

	// Pass 2: Evict oldest turns until conversation fits or reaches minTurns floor.
	// A system message anywhere in the window is never evicted: clients may
	// interleave system turns (e.g. system at 0 and 3), and folding one into
	// the recap would silently lose its instructions. Skipped systems stay in
	// place in the tail, and the recap lands right after the leading system
	// run — the chronological position of the first evicted turn.
	tokens := EstimateConversationTokens(working)
	cut := firstTurn
	keepFloor := len(working) - minTurns
	if firstTurn > keepFloor {
		keepFloor = firstTurn
	}
	droppedCount := 0

	for tokens > budget && cut < keepFloor {
		if working[cut].Role == "system" {
			cut++
			continue
		}
		tokens -= EstimateMessageTokens(working[cut])
		cut++
		droppedCount++
	}

	if droppedCount == 0 {
		return SqueezeResult{
			Messages: working,
			Changed:  shortened > 0,
			Stats: SqueezeStats{
				TokensBefore:      tokensBefore,
				TokensAfter:       EstimateConversationTokens(working),
				ShortenedMessages: shortened,
				Reason:            "floor",
			},
		}
	}

	dropped := make([]OpenAIMessage, 0, droppedCount)
	tail := make([]OpenAIMessage, 0, len(working)-firstTurn-droppedCount)
	for i := firstTurn; i < cut; i++ {
		if working[i].Role == "system" {
			tail = append(tail, working[i])
			continue
		}
		dropped = append(dropped, working[i])
	}
	tail = append(tail, working[cut:]...)
	recap := BuildRecap(dropped)

	result := make([]OpenAIMessage, 0, firstTurn+1+len(tail))
	result = append(result, working[:firstTurn]...)
	result = append(result, OpenAIMessage{Role: "system", Content: recap})
	result = append(result, tail...)

	tokensAfter := EstimateConversationTokens(result)
	return SqueezeResult{
		Messages: result,
		Changed:  true,
		Stats: SqueezeStats{
			TokensBefore:      tokensBefore,
			TokensAfter:       tokensAfter,
			DroppedTurns:      len(dropped),
			ShortenedMessages: shortened,
			RecapChars:        len(recap),
			Reason:            "trimmed",
		},
	}
}

// SqueezeContextRequestBody parses request JSON body, applies SqueezeContext if needed, and remarshals.
func SqueezeContextRequestBody(body []byte, opts SqueezeOptions) ([]byte, bool, SqueezeStats) {
	if len(body) == 0 || opts.ContextWindow <= 0 {
		return body, false, SqueezeStats{Reason: "no-window"}
	}

	var req map[string]any
	// Numbered decode: the re-marshal below must not round >2^53 integers in
	// the request through float64.
	if err := unmarshalNumbered(body, &req); err != nil {
		return body, false, SqueezeStats{Reason: "unparseable"}
	}

	rawMsgs, ok := req["messages"].([]any)
	if !ok || len(rawMsgs) == 0 {
		return body, false, SqueezeStats{Reason: "no-messages"}
	}

	// Decode into OpenAIMessage slice
	msgsBytes, err := json.Marshal(rawMsgs, json.Deterministic(true))
	if err != nil {
		return body, false, SqueezeStats{Reason: "marshal-err"}
	}
	var messages []OpenAIMessage
	if err := json.Unmarshal(msgsBytes, &messages); err != nil {
		return body, false, SqueezeStats{Reason: "unmarshal-err"}
	}

	res := SqueezeContext(messages, opts)
	if !res.Changed {
		return body, false, res.Stats
	}

	req["messages"] = res.Messages
	out, err := json.Marshal(req, json.Deterministic(true))
	if err != nil {
		return body, false, res.Stats
	}
	return out, true, res.Stats
}
