// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	json "encoding/json/v2"

	"9routerx/proxy/internal/translator"
)

// Constants matching open-sse/executors/webCookieToolBridge.js
const (
	ToolMarker       = "<<TOOL_CALL>>"
	ToolResultMarker = "<<TOOL_RESULT>>"
	ToolFenceOpen    = "```tool_call"
	ToolFenceClose   = "```"
)

// WebCookieToolCall holds a parsed tool call from a web-cookie model output.
type WebCookieToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolChoiceRule contains resolved tool choice mode and forced tool name.
type ToolChoiceRule struct {
	Mode   string // "none" | "auto" | "required"
	Forced string
}

// ParseToolChoiceRule parses OpenAI-style tool_choice.
func ParseToolChoiceRule(raw any) ToolChoiceRule {
	if raw == nil {
		return ToolChoiceRule{Mode: "auto"}
	}
	switch v := raw.(type) {
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		if s == "none" {
			return ToolChoiceRule{Mode: "none"}
		}
		if s == "required" || s == "any" {
			return ToolChoiceRule{Mode: "required"}
		}
		return ToolChoiceRule{Mode: "auto"}
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && strings.TrimSpace(name) != "" {
				return ToolChoiceRule{Mode: "required", Forced: strings.TrimSpace(name)}
			}
		}
		if name, ok := v["name"].(string); ok && strings.TrimSpace(name) != "" {
			return ToolChoiceRule{Mode: "required", Forced: strings.TrimSpace(name)}
		}
	}
	return ToolChoiceRule{Mode: "auto"}
}

type toolDefItem struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

func selectableToolDefs(rawTools []any, forced string) []toolDefItem {
	var out []toolDefItem
	for _, raw := range rawTools {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fn, _ := m["function"].(map[string]any)
		if fn == nil {
			fn = m
		}
		name, _ := fn["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if forced != "" && name != forced {
			continue
		}
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]any)
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, toolDefItem{
			Name:        name,
			Description: desc,
			Parameters:  params,
		})
	}
	return out
}

func exampleToolBlock(style string) string {
	exampleJSON := "{\n  \"name\": \"run_command\",\n  \"arguments\": {\n    \"command\": \"ls -la\"\n  }\n}"
	if style == "fenced" {
		return fmt.Sprintf("%s\n%s\n%s", ToolFenceOpen, exampleJSON, ToolFenceClose)
	}
	return fmt.Sprintf("%s %s %s", ToolMarker, exampleJSON, ToolMarker)
}

// BuildWebCookieToolInstructions formats the leading system instructions with tool schemas.
func BuildWebCookieToolInstructions(tools []any, toolChoice any, style string) string {
	rule := ParseToolChoiceRule(toolChoice)
	if rule.Mode == "none" {
		return ""
	}
	defs := selectableToolDefs(tools, rule.Forced)
	if len(defs) == 0 {
		return ""
	}

	instruction := "Only use tool_call blocks when needed."
	if rule.Forced != "" {
		instruction = fmt.Sprintf("You MUST call the tool %q. Reply with the tool_call block and nothing else - do not answer the question yourself, even if you know the answer.", rule.Forced)
	} else if rule.Mode == "required" {
		instruction = "You MUST call one of the tools above. Reply with the tool_call block and nothing else - do not answer the question yourself, even if you know the answer."
	}

	defsBytes, _ := json.Marshal(defs, json.Deterministic(true))

	if style == "fenced" {
		return fmt.Sprintf("[System instruction]: You have access to tools. To call a tool, respond with:\n%s\n{\"name\": \"func_name\", \"arguments\": {...}}\n%s\n%s\n\nAvailable tools:\n%s",
			ToolFenceOpen, ToolFenceClose, instruction, string(defsBytes))
	}
	return fmt.Sprintf("[System instruction]: You have access to tools. To call a tool, respond with:\n%s {\"name\": \"func_name\", \"arguments\": {...}} %s\n%s\n\nAvailable tools:\n%s",
		ToolMarker, ToolMarker, instruction, string(defsBytes))
}

// BuildWebCookieToolReminder formats the trailing reminder ensuring models emit execution blocks.
func BuildWebCookieToolReminder(style string, rule ToolChoiceRule) string {
	forcedLine := ""
	if rule.Forced != "" {
		forcedLine = fmt.Sprintf("\nThis turn is FORCED: the client set tool_choice to the tool %q. Emit exactly one tool_call block calling %q with valid arguments and nothing else - answering in prose is a protocol violation, even if you already know the answer.", rule.Forced, rule.Forced)
	} else if rule.Mode == "required" {
		forcedLine = "\nThis turn is FORCED: the client set tool_choice to require a tool. Emit one tool_call block and nothing else - answering in prose is a protocol violation."
	}

	lines := []string{
		"[System instruction - highest priority]: To run a command or read a file you MUST output a tool_call block - this is the ONLY execution channel. A code block is NOT executed, it is only shown to the user; never use one to run anything, and never claim you lack tools.",
		"Act, do not explain: when the request needs a tool, your reply is ONE tool_call block and nothing else - do not list the commands you 'would' run, do not add prose. Emit several blocks only when the user asks for several independent things at once (one block per call, nothing else); when the steps depend on each other, do the FIRST one now and you will be called again with its result.",
		"Know when to STOP: once a tool result shows a step succeeded (for example it exited with code 0, even with no printed output), that step is DONE - do not run it again and do not retry the same thing a different way. When the whole request is accomplished, stop calling tools and reply with a short final answer instead of another tool_call.",
		`Example of a correct turn - user asks "list the files here and tell me the size of README.md", you reply with exactly one block and nothing else:`,
		exampleToolBlock(style),
		"(In your block use one of the real tool names listed above with its own arguments.) When the request needs a tool, emit the tool_call block now.",
	}
	if forcedLine != "" {
		lines = append(lines, forcedLine)
	}
	return strings.Join(lines, "\n")
}

// RenderWebCookieToolTranscript encodes previous assistant tool calls and tool outputs into readable prose.
func RenderWebCookieToolTranscript(messages []translator.OpenAIMessage, style string) string {
	var parts []string
	for _, m := range messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var calls []string
			for _, tc := range m.ToolCalls {
				fnName := tc.Function.Name
				argsJSON := "{}"
				if tc.Function.Arguments != "" {
					argsJSON = tc.Function.Arguments
				}
				payload := fmt.Sprintf(`{"name": %q, "arguments": %s}`, fnName, argsJSON)
				if style == "fenced" {
					calls = append(calls, fmt.Sprintf("%s\n%s\n%s", ToolFenceOpen, payload, ToolFenceClose))
				} else {
					calls = append(calls, fmt.Sprintf("%s %s %s", ToolMarker, payload, ToolMarker))
				}
			}
			text := ""
			if s, ok := m.Content.(string); ok {
				text = s
			}
			if text != "" {
				parts = append(parts, fmt.Sprintf("[Assistant]: %s\n%s", text, strings.Join(calls, "\n")))
			} else {
				parts = append(parts, fmt.Sprintf("[Assistant]: %s", strings.Join(calls, "\n")))
			}
		} else if m.Role == "tool" {
			text := ""
			if s, ok := m.Content.(string); ok {
				text = s
			}
			if text != "" {
				parts = append(parts, fmt.Sprintf("%s tool: %s %s", ToolResultMarker, text, ToolResultMarker))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

var (
	fencedToolRe = regexp.MustCompile("(?s)```(?:tool_call|json)?\\s*(\\{.*?\\})\\s*```")
	// balancedBraceRe locates a top-level JSON object that opens on {"name" —
	// the shape a model emits in raw prose without any fence. The balanced
	// scan below (not the regex) is what actually cuts it out; the regex only
	// finds candidate starts.
	balancedBraceRe = regexp.MustCompile(`\{"name"\s*:`)
	// answerFenceRe unwraps an <answer> envelope; compiled once because the
	// cleanup it belongs to runs per response.
	answerFenceRe = regexp.MustCompile(`(?i)<answer>([\s\S]*?)</answer>`)
)

// balancedBraceEnd returns the length of the balanced {...} object starting
// at the beginning of s (which must start on the opening brace), or !ok when
// the braces never balance inside s. String literals are skipped so a brace
// inside a value does not close the object early.
func balancedBraceEnd(s string) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// ParseWebCookieToolCallReply parses model response text and extracts embedded tool calls.
func ParseWebCookieToolCallReply(text string) (content string, calls []WebCookieToolCall) {
	raw := strings.TrimSpace(text)
	var candidates []string

	// 1. Marker candidates
	parts := strings.Split(raw, ToolMarker)
	for i := 1; i < len(parts); i += 2 {
		candidates = append(candidates, parts[i])
	}

	// 2. Fenced candidates
	for _, m := range fencedToolRe.FindAllStringSubmatch(raw, -1) {
		if len(m) > 1 {
			candidates = append(candidates, m[1])
		}
	}

	// 3. Raw JSON brace candidate with "name". A regex cannot match balanced
	// braces, so the match start is found first and the closing brace is
	// located with a depth scan that skips string literals.
	if len(candidates) == 0 {
		if idx := strings.Index(raw, `{"name"`); idx >= 0 {
			if end, ok := balancedBraceEnd(raw[idx:]); ok {
				candidates = append(candidates, raw[idx:idx+end])
			}
		}
	}

	for _, cand := range candidates {
		cand = strings.TrimSpace(cand)
		var callObj struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(cand), &callObj); err == nil && callObj.Name != "" {
			if callObj.Arguments == nil {
				callObj.Arguments = map[string]any{}
			}
			calls = append(calls, WebCookieToolCall{
				Name:      callObj.Name,
				Arguments: callObj.Arguments,
			})
			break
		}
	}

	// Remove tool call blocks from content
	cleaned := raw
	if len(calls) > 0 {
		cleaned = strings.ReplaceAll(cleaned, ToolMarker, "\n")
		cleaned = fencedToolRe.ReplaceAllString(cleaned, "")
		cleaned = balancedBraceRe.ReplaceAllStringFunc(cleaned, func(m string) string {
			// Only strip the brace blob when it parses as a tool call; a
			// non-tool JSON object in the prose must survive untouched.
			var probe struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(m), &probe); err == nil && probe.Name != "" {
				return ""
			}
			return m
		})
		cleaned = strings.TrimSpace(cleaned)
	}

	// Extract <answer>...</answer> if wrapped
	if m := answerFenceRe.FindStringSubmatch(cleaned); len(m) > 1 {
		cleaned = strings.TrimSpace(m[1])
	}

	return cleaned, calls
}

// GenerateWebCookieToolCallID generates a unique tool call ID.
func GenerateWebCookieToolCallID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("call_%x%x", time.Now().UnixMilli(), hex.EncodeToString(b))
}
