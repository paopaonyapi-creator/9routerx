// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"bytes"
	"fmt"
	"strings"

	json "encoding/json/v2"
)

// ToolFallbackMaxLevel is the highest level DegradeToolPayload will apply.
const ToolFallbackMaxLevel = 3

var toolErrorMarkers = []string{
	"tool",
	"function_call",
	"function call",
	"tool_choice",
	"schema",
}

var nonToolErrorMarkers = []string{
	"context length",
	"context_length",
	"too many tokens",
	"maximum context",
	"content policy",
	"content_policy",
}

var fragileSchemaKeywords = map[string]bool{
	"$schema":               true,
	"$id":                   true,
	"$ref":                  true,
	"$defs":                 true,
	"additionalProperties":  true,
	"patternProperties":     true,
	"unevaluatedProperties": true,
	"propertyNames":         true,
	"minProperties":         true,
	"maxProperties":         true,
	"oneOf":                 true,
	"allOf":                 true,
	"not":                   true,
	"if":                    true,
	"then":                  true,
	"else":                  true,
	"dependentSchemas":      true,
	"dependentRequired":     true,
	"const":                 true,
	"examples":              true,
	"default":               true,
	"contentEncoding":       true,
	"contentMediaType":      true,
	"exclusiveMinimum":      true,
	"exclusiveMaximum":      true,
	"multipleOf":            true,
	"uniqueItems":           true,
}

// IsToolCallRejection reports whether an upstream failure status and error message
// indicate a tool schema or definition rejection.
func IsToolCallRejection(status int, message string) bool {
	if status != 400 && status != 404 && status != 406 && status != 422 && status != 500 {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" {
		return false
	}
	for _, m := range nonToolErrorMarkers {
		if strings.Contains(text, m) {
			return false
		}
	}
	for _, m := range toolErrorMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// normalizeArguments normalizes tool arguments into valid JSON string.
func normalizeArguments(args any) string {
	if args == nil {
		return "{}"
	}
	switch v := args.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return "{}"
		}
		// Validation only — the original bytes are returned unchanged, so a
		// client-supplied ID above 2^53 never round-trips through float64.
		var test any
		if err := unmarshalNumbered([]byte(trimmed), &test); err == nil {
			return trimmed
		}
		repaired := RepairTruncatedJSON(trimmed)
		if err := unmarshalNumbered([]byte(repaired), &test); err == nil {
			return repaired
		}
		return "{}"
	default:
		b, err := json.Marshal(v, json.Deterministic(true))
		if err == nil {
			return string(b)
		}
		return "{}"
	}
}

func toolName(tool map[string]any) string {
	if fn, ok := tool["function"].(map[string]any); ok {
		if n, ok := fn["name"].(string); ok && n != "" {
			return n
		}
	}
	if n, ok := tool["name"].(string); ok && n != "" {
		return n
	}
	return ""
}

func toolSchema(tool map[string]any) map[string]any {
	if fn, ok := tool["function"].(map[string]any); ok {
		if params, ok := fn["parameters"].(map[string]any); ok {
			return params
		}
	}
	if params, ok := tool["parameters"].(map[string]any); ok {
		return params
	}
	if schema, ok := tool["input_schema"].(map[string]any); ok {
		return schema
	}
	return nil
}

func stripFragileKeywords(node any) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			if fragileSchemaKeywords[k] {
				delete(n, k)
				continue
			}
			stripFragileKeywords(v)
		}
	case []any:
		for _, item := range n {
			stripFragileKeywords(item)
		}
	}
}

func hasTools(body map[string]any) bool {
	tools, ok := body["tools"].([]any)
	return ok && len(tools) > 0
}

func inlineText(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if s := inlineText(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if t, ok := v["text"].(string); ok {
			return t
		}
		if c, ok := v["content"].(string); ok {
			return c
		}
	}
	return ""
}

const (
	toolResultPrefix = "[Tool result: "
	toolCallPrefix   = "[Called tools: "
)

// isTextLikeBlock reports whether a content block survives an inlineText
// degradation without information loss. Anything carrying more than plain
// text — an image_url / image block, a document, a cache_control marker —
// is not, and must be kept in the content array verbatim instead of being
// flattened away (level-3 degrade would otherwise drop client images
// silently).
func isTextLikeBlock(b any) bool {
	switch v := b.(type) {
	case string:
		return true
	case map[string]any:
		typ, _ := v["type"].(string)
		if typ != "" && typ != "text" && typ != "input_text" && typ != "output_text" {
			return false
		}
		for k, val := range v {
			switch k {
			case "type", "text":
			case "content":
				if _, isStr := val.(string); !isStr {
					return false
				}
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}

// allTextLikeBlocks reports whether every block inline-degrades losslessly.
func allTextLikeBlocks(blocks []any) bool {
	for _, b := range blocks {
		if !isTextLikeBlock(b) {
			return false
		}
	}
	return true
}

// RepairToolPayload repairs tool calls, closes truncated JSON arguments,
// and drops orphaned tool results/outputs.
func RepairToolPayload(body map[string]any) map[string]any {
	if body == nil {
		return nil
	}

	openAIIds := make(map[string]bool)
	claudeIds := make(map[string]bool)
	responsesIds := make(map[string]bool)

	// Collect known tool call IDs and repair arguments
	if msgs, ok := body["messages"].([]any); ok {
		for _, rawMsg := range msgs {
			msg, ok := rawMsg.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)

			// OpenAI tool_calls
			if role == "assistant" {
				if tcs, ok := msg["tool_calls"].([]any); ok {
					validCalls := make([]any, 0, len(tcs))
					for _, rawTC := range tcs {
						tc, ok := rawTC.(map[string]any)
						if !ok {
							continue
						}
						fn, _ := tc["function"].(map[string]any)
						name := ""
						if fn != nil {
							name, _ = fn["name"].(string)
						}
						if name == "" {
							continue
						}
						id, _ := tc["id"].(string)
						if id != "" {
							openAIIds[id] = true
						}
						if fn != nil {
							fn["arguments"] = normalizeArguments(fn["arguments"])
							tc["function"] = fn
						}
						if tc["type"] == nil {
							tc["type"] = "function"
						}
						validCalls = append(validCalls, tc)
					}
					msg["tool_calls"] = validCalls
				}
			}

			// Claude content blocks
			if content, ok := msg["content"].([]any); ok {
				validBlocks := make([]any, 0, len(content))
				for _, rawB := range content {
					b, ok := rawB.(map[string]any)
					if !ok {
						continue
					}
					typ, _ := b["type"].(string)
					if typ == "tool_use" {
						name, _ := b["name"].(string)
						if name == "" {
							continue
						}
						id, _ := b["id"].(string)
						if id != "" {
							claudeIds[id] = true
						}
						if inputStr, ok := b["input"].(string); ok {
							var parsed any
							if err := unmarshalNumbered([]byte(inputStr), &parsed); err == nil {
								b["input"] = parsed
							} else {
								repaired := RepairTruncatedJSON(inputStr)
								if err := unmarshalNumbered([]byte(repaired), &parsed); err == nil {
									b["input"] = parsed
								} else {
									b["input"] = map[string]any{}
								}
							}
						}
					}
					validBlocks = append(validBlocks, b)
				}
				msg["content"] = validBlocks
			}
		}

		// Filter orphaned tool results
		filteredMsgs := make([]any, 0, len(msgs))
		for _, rawMsg := range msgs {
			msg, ok := rawMsg.(map[string]any)
			if !ok {
				filteredMsgs = append(filteredMsgs, rawMsg)
				continue
			}
			role, _ := msg["role"].(string)
			if role == "tool" {
				callID, _ := msg["tool_call_id"].(string)
				if callID != "" && !openAIIds[callID] {
					continue // Orphan dropped
				}
			}
			if content, ok := msg["content"].([]any); ok {
				validBlocks := make([]any, 0, len(content))
				for _, rawB := range content {
					b, ok := rawB.(map[string]any)
					if !ok {
						validBlocks = append(validBlocks, rawB)
						continue
					}
					typ, _ := b["type"].(string)
					if typ == "tool_result" {
						useID, _ := b["tool_use_id"].(string)
						if useID != "" && !claudeIds[useID] {
							continue // Orphan block dropped
						}
					}
					validBlocks = append(validBlocks, b)
				}
				msg["content"] = validBlocks
			}
			filteredMsgs = append(filteredMsgs, msg)
		}
		body["messages"] = filteredMsgs
	}

	// Responses input function_call / function_call_output
	if input, ok := body["input"].([]any); ok {
		for _, rawItem := range input {
			item, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := item["type"].(string)
			if typ == "function_call" {
				if callID, _ := item["call_id"].(string); callID != "" {
					responsesIds[callID] = true
				}
			}
		}
		validInput := make([]any, 0, len(input))
		for _, rawItem := range input {
			item, ok := rawItem.(map[string]any)
			if !ok {
				validInput = append(validInput, rawItem)
				continue
			}
			typ, _ := item["type"].(string)
			if typ == "function_call_output" {
				callID, _ := item["call_id"].(string)
				if callID != "" && !responsesIds[callID] {
					continue // Orphan output dropped
				}
			}
			validInput = append(validInput, item)
		}
		body["input"] = validInput
	}

	// Validate tool_choice
	if tc, ok := body["tool_choice"].(map[string]any); ok {
		named := ""
		if n, ok := tc["name"].(string); ok {
			named = n
		} else if fn, ok := tc["function"].(map[string]any); ok {
			named, _ = fn["name"].(string)
		}
		if named != "" {
			found := false
			if tools, ok := body["tools"].([]any); ok {
				for _, rawT := range tools {
					if t, ok := rawT.(map[string]any); ok && toolName(t) == named {
						found = true
						break
					}
				}
			}
			if !found {
				delete(body, "tool_choice")
			}
		}
	}
	if tcStr, ok := body["tool_choice"].(string); ok {
		if (tcStr == "required" || tcStr == "any") && !hasTools(body) {
			delete(body, "tool_choice")
		}
	}

	return body
}

// repairableMarkers are the byte fragments every construct RepairToolPayload
// can touch carries. A body without any of them cannot be changed by the
// repair pass, so RepairToolPayloadBytes skips its decode/marshal entirely —
// the pass sits on the retry path of every chat request.
var repairableMarkers = [][]byte{
	[]byte(`"tool_calls"`), []byte(`"tool_call_id"`), []byte(`"tool_use"`),
	[]byte(`"tool_result"`), []byte(`"function_call"`), []byte(`"tool_choice"`),
	[]byte(`"tools"`),
}

// RepairToolPayloadBytes runs RepairToolPayload on raw JSON bytes.
func RepairToolPayloadBytes(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	hasMarkers := false
	for _, m := range repairableMarkers {
		if bytes.Contains(body, m) {
			hasMarkers = true
			break
		}
	}
	if !hasMarkers {
		return body, false
	}
	// Numbered decode: this runs on EVERY retried request body, and a plain
	// any-decode would round every >2^53 integer in it through float64.
	var req map[string]any
	if err := unmarshalNumbered(body, &req); err != nil {
		return body, false
	}
	repaired := RepairToolPayload(req)
	out, err := json.Marshal(repaired, json.Deterministic(true))
	if err != nil {
		return body, false
	}
	return out, true
}

func cloneBodyMap(body map[string]any) map[string]any {
	b, err := json.Marshal(body, json.Deterministic(true))
	if err != nil {
		return body
	}
	var out map[string]any
	// The source map may already carry json.Number values (see
	// unmarshalNumbered); decoding into float64 here would undo that.
	_ = unmarshalNumbered(b, &out)
	return out
}

func flattenMessages(messages []any) []any {
	var out []any
	for _, rawMsg := range messages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			out = append(out, rawMsg)
			continue
		}
		role, _ := msg["role"].(string)

		if role == "tool" || role == "function" {
			out = append(out, map[string]any{
				"role":    "assistant",
				"content": fmt.Sprintf("%s%s]", toolResultPrefix, inlineText(msg["content"])),
			})
			continue
		}

		if role == "assistant" {
			if tcs, ok := msg["tool_calls"].([]any); ok && len(tcs) > 0 {
				var names []string
				for _, rawTC := range tcs {
					if tc, ok := rawTC.(map[string]any); ok {
						n := toolName(tc)
						if n == "" {
							n = "tool"
						}
						names = append(names, n)
					}
				}
				copyMsg := make(map[string]any, len(msg))
				for k, v := range msg {
					if k != "tool_calls" {
						copyMsg[k] = v
					}
				}
				note := fmt.Sprintf("%s%s]", toolCallPrefix, strings.Join(names, ", "))
				if blocks, ok := copyMsg["content"].([]any); ok && !allTextLikeBlocks(blocks) {
					// Non-text blocks (images) ride along in the array; the
					// tool-call note joins them as its own text block.
					merged := make([]any, 0, len(blocks)+1)
					merged = append(merged, blocks...)
					merged = append(merged, map[string]any{"type": "text", "text": note})
					copyMsg["content"] = merged
				} else {
					base := inlineText(copyMsg["content"])
					sep := ""
					if base != "" {
						sep = "\n"
					}
					copyMsg["content"] = base + sep + note
				}
				out = append(out, copyMsg)
				continue
			}
		}

		if content, ok := msg["content"].([]any); ok {
			var kept []any
			var results []string
			var used []string

			for _, rawB := range content {
				b, ok := rawB.(map[string]any)
				if !ok {
					kept = append(kept, rawB)
					continue
				}
				typ, _ := b["type"].(string)
				if typ == "tool_result" {
					results = append(results, inlineText(b["content"]))
					continue
				}
				if typ == "tool_use" {
					name, _ := b["name"].(string)
					if name == "" {
						name = "tool"
					}
					used = append(used, name)
					continue
				}
				kept = append(kept, b)
			}

			if len(results) > 0 {
				out = append(out, map[string]any{
					"role":    "assistant",
					"content": fmt.Sprintf("%s%s]", toolResultPrefix, strings.Join(results, "\n")),
				})
			}
			if len(used) > 0 {
				out = append(out, map[string]any{
					"role":    "assistant",
					"content": fmt.Sprintf("%s%s]", toolCallPrefix, strings.Join(used, ", ")),
				})
			}
			if len(kept) > 0 {
				// Text-only blocks degrade to a plain string; anything else
				// (images, documents) is kept verbatim rather than flattened
				// away by inlineText.
				content := any(inlineText(kept))
				if !allTextLikeBlocks(kept) {
					content = kept
				}
				out = append(out, map[string]any{
					"role":    role,
					"content": content,
				})
			}
			continue
		}

		out = append(out, msg)
	}
	return out
}

func flattenResponses(input []any) []any {
	var out []any
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		typ, _ := item["type"].(string)
		if typ == "function_call" || typ == "function_call_output" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func flattenGemini(contents []any) []any {
	var out []any
	for _, raw := range contents {
		c, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		parts, ok := c["parts"].([]any)
		if !ok {
			out = append(out, c)
			continue
		}
		var kept []any
		for _, rawP := range parts {
			p, ok := rawP.(map[string]any)
			if !ok {
				kept = append(kept, rawP)
				continue
			}
			if p["functionCall"] != nil || p["functionResponse"] != nil {
				continue
			}
			kept = append(kept, p)
		}
		if len(kept) > 0 {
			c["parts"] = kept
			out = append(out, c)
		}
	}
	return out
}

// DegradeToolPayload degrades tool machinery in escalating steps:
// Level 1: Drops tool_choice and removes strict from tools.
// Level 2: Level 1 + strips fragile schema keywords from tool schemas.
// Level 3: Level 2 + removes tools entirely and inlines tool history as prose.
// Returns (degradedBody, true), or (nil, false) when nothing can be degraded or past max level.
func DegradeToolPayload(body map[string]any, level int) (map[string]any, bool) {
	if body == nil || level < 1 || level > ToolFallbackMaxLevel {
		return nil, false
	}

	if level == 1 {
		if !hasTools(body) {
			return nil, false
		}
		next := cloneBodyMap(body)
		delete(next, "tool_choice")
		if tools, ok := next["tools"].([]any); ok {
			for _, rawT := range tools {
				if t, ok := rawT.(map[string]any); ok {
					delete(t, "strict")
					if fn, ok := t["function"].(map[string]any); ok {
						delete(fn, "strict")
					}
				}
			}
		}
		return next, true
	}

	if level == 2 {
		prev, ok := DegradeToolPayload(body, 1)
		if !ok {
			return nil, false
		}
		if tools, ok := prev["tools"].([]any); ok {
			for _, rawT := range tools {
				if t, ok := rawT.(map[string]any); ok {
					if schema := toolSchema(t); schema != nil {
						stripFragileKeywords(schema)
					}
				}
			}
		}
		return prev, true
	}

	if level == 3 {
		prev, ok := DegradeToolPayload(body, 2)
		if !ok {
			return nil, false
		}
		delete(prev, "tools")
		delete(prev, "tool_choice")
		if msgs, ok := prev["messages"].([]any); ok {
			prev["messages"] = flattenMessages(msgs)
		}
		if input, ok := prev["input"].([]any); ok {
			prev["input"] = flattenResponses(input)
		}
		if contents, ok := prev["contents"].([]any); ok {
			prev["contents"] = flattenGemini(contents)
		}
		return prev, true
	}

	return nil, false
}

// DegradeToolPayloadBytes runs DegradeToolPayload on raw JSON request bytes.
func DegradeToolPayloadBytes(body []byte, level int) ([]byte, bool) {
	if len(body) == 0 {
		return nil, false
	}
	// Numbered decode so the re-marshal cannot round >2^53 integers in the
	// request (IDs inside tool_use input objects) through float64.
	var req map[string]any
	if err := unmarshalNumbered(body, &req); err != nil {
		return nil, false
	}
	res, ok := DegradeToolPayload(req, level)
	if !ok || res == nil {
		return nil, false
	}
	out, err := json.Marshal(res, json.Deterministic(true))
	if err != nil {
		return nil, false
	}
	return out, true
}
