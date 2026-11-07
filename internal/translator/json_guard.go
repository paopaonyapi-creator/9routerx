// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"regexp"
	"strings"

	json "encoding/json/v2"
)

// json_guard.go provides JSON repair and schema validation (port(serenhope) TASK-2.4,
// upstream open-sse/translator/concerns/jsonGuard.js). It repairs markdown fences,
// python literals, single quotes, trailing commas, and truncated payloads, and filters
// tool arguments against declared schemas.

var fencedBlockRe = regexp.MustCompile(`(?i)` + "```" + `(?:json|jsonc|json5)?[ \t]*\r?\n([\s\S]*?)` + "```")
var trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)
var singleQuoteRe = regexp.MustCompile(`'([^'\\]*(?:\\.[^'\\]*)*)'`)

// ScanBalancedJSON finds the first balanced JSON container ({...} or [...]) in text,
// ignoring braces inside string literals. Returns (matched, true) or ("", false).
func ScanBalancedJSON(text string) (string, bool) {
	start := -1
	for i, ch := range text {
		if ch == '{' || ch == '[' {
			start = i
			break
		}
	}
	if start == -1 {
		return "", false
	}

	closerFor := map[rune]rune{'{': '}', '[': ']'}
	var stack []rune
	inString := false
	escaped := false

	runes := []rune(text)
	startRune := len([]rune(text[:start]))

	for i := startRune; i < len(runes); i++ {
		ch := runes[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' || ch == '[' {
			stack = append(stack, closerFor[ch])
			continue
		}
		if ch == '}' || ch == ']' {
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return "", false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return string(runes[startRune : i+1]), true
			}
		}
	}
	return "", false
}

// FencedBlock extracts contents of the first ```json ... ``` code fence.
func FencedBlock(text string) (string, bool) {
	match := fencedBlockRe.FindStringSubmatch(text)
	if len(match) > 1 {
		return strings.TrimSpace(match[1]), true
	}
	return "", false
}

func mapOutsideStrings(text string, fn func(string) string) string {
	var out strings.Builder
	var segment strings.Builder
	inString := false
	escaped := false

	flush := func() {
		if segment.Len() > 0 {
			out.WriteString(fn(segment.String()))
			segment.Reset()
		}
	}

	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inString {
			out.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			flush()
			out.WriteByte(ch)
			inString = true
			continue
		}
		segment.WriteByte(ch)
	}
	flush()
	return out.String()
}

// repairLiterals rewrites one outside-string segment. `-Infinity` runs BEFORE
// the bare `Infinity` rule: `\bInfinity\b` already matches the tail of
// `-Infinity` (the `-` is a word boundary), so the reverse order turned
// `-Infinity` into `-null`, which no later pass can parse.
func repairLiterals(seg string) string {
	s := seg
	s = regexp.MustCompile(`\bTrue\b`).ReplaceAllString(s, "true")
	s = regexp.MustCompile(`\bFalse\b`).ReplaceAllString(s, "false")
	s = regexp.MustCompile(`\bNone\b`).ReplaceAllString(s, "null")
	s = regexp.MustCompile(`\bNaN\b`).ReplaceAllString(s, "null")
	s = regexp.MustCompile(`-Infinity\b`).ReplaceAllString(s, "null")
	s = regexp.MustCompile(`\bInfinity\b`).ReplaceAllString(s, "null")
	return s
}

// RepairJSONText rewrites Python/JS literals outside string literals, converts
// single-quoted payloads to double quotes, and removes trailing commas.
func RepairJSONText(text string) string {
	if text == "" {
		return text
	}
	out := mapOutsideStrings(text, repairLiterals)

	// The conversion only fires on text with NO double quotes at all, so a
	// double-quoted payload containing apostrophes ("it's") can never be
	// rewritten. An escaped apostrophe inside a single-quoted Python string
	// ('it\'s') becomes a bare one, because \' is not a legal JSON escape.
	if !strings.Contains(out, `"`) && strings.Contains(out, "'") {
		out = singleQuoteRe.ReplaceAllStringFunc(out, func(m string) string {
			inner := strings.ReplaceAll(m[1:len(m)-1], `\'`, `'`)
			return `"` + inner + `"`
		})
	}

	return trailingCommaRe.ReplaceAllString(out, "$1")
}

// tryParseAny parses text as JSON with number literals preserved as
// json.Number. The parsed value is re-marshaled by callers (ApplyJSONGuard,
// CoerceToolArguments), so a plain float64 decode would round >2^53 integers
// — tool-argument IDs included — on exactly the payloads this exists to save.
func tryParseAny(text string) (any, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, false
	}
	var v any
	if err := unmarshalNumbered([]byte(trimmed), &v); err == nil {
		return v, true
	}
	return nil, false
}

// CloseTruncatedJSON closes incomplete JSON objects and arrays cut off mid-stream.
func CloseTruncatedJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}

	var openerPos []int
	inString := false
	escaped := false
	lastComma := -1
	lastOpener := -1

	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			continue
		}
		if ch == '{' || ch == '[' {
			openerPos = append(openerPos, i)
			lastOpener = i
			lastComma = -1
			continue
		}
		if ch == '}' || ch == ']' {
			if len(openerPos) == 0 {
				return trimmed
			}
			openerPos = openerPos[:len(openerPos)-1]
			lastComma = -1
			continue
		}
		if ch == ',' {
			lastComma = i
		}
	}

	if len(openerPos) == 0 {
		return trimmed
	}

	var closersBuilder strings.Builder
	for i := len(openerPos) - 1; i >= 0; i-- {
		if trimmed[openerPos[i]] == '{' {
			closersBuilder.WriteByte('}')
		} else {
			closersBuilder.WriteByte(']')
		}
	}
	closers := closersBuilder.String()

	var candidates []string
	if !inString {
		candidates = append(candidates, trimmed+closers)
	}
	if lastComma >= 0 {
		candidates = append(candidates, trimmed[:lastComma]+closers)
	}
	if lastOpener >= 0 {
		candidates = append(candidates, trimmed[:lastOpener+1]+closers)
	}

	for _, cand := range candidates {
		if _, ok := tryParseAny(cand); ok {
			return cand
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return trimmed
}

// CoercedJSON holds the output of CoerceJSONOutput.
type CoercedJSON struct {
	Value     any
	Repaired  bool
	Truncated bool
	Source    string // "direct", "fence", "balanced", "repair", "truncated"
}

// CoerceJSONOutput applies the repair ladder to extract and fix JSON from model answers.
func CoerceJSONOutput(text string) *CoercedJSON {
	if text == "" {
		return nil
	}
	if val, ok := tryParseAny(text); ok {
		return &CoercedJSON{Value: val, Repaired: false, Truncated: false, Source: "direct"}
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}

	// 1. Fenced block
	if fence, ok := FencedBlock(trimmed); ok {
		if val, ok := tryParseAny(fence); ok {
			return &CoercedJSON{Value: val, Repaired: true, Truncated: false, Source: "fence"}
		}
		if val, ok := tryParseAny(RepairJSONText(fence)); ok {
			return &CoercedJSON{Value: val, Repaired: true, Truncated: false, Source: "fence"}
		}
	}

	// 2. Balanced JSON in prose
	if balanced, ok := ScanBalancedJSON(trimmed); ok {
		if val, ok := tryParseAny(balanced); ok {
			return &CoercedJSON{Value: val, Repaired: true, Truncated: false, Source: "balanced"}
		}
	}

	// 3. Rewrite coding slips
	repaired := RepairJSONText(trimmed)
	if val, ok := tryParseAny(repaired); ok {
		return &CoercedJSON{Value: val, Repaired: true, Truncated: false, Source: "repair"}
	}

	// 4. Close truncated payload
	closed := CloseTruncatedJSON(repaired)
	if val, ok := tryParseAny(closed); ok {
		return &CoercedJSON{Value: val, Repaired: true, Truncated: true, Source: "truncated"}
	}

	return nil
}

// CoerceToolArguments coercies raw tool arguments into map[string]any.
func CoerceToolArguments(raw any) (args map[string]any, repaired bool, truncated bool, unparseable bool) {
	if raw == nil {
		return map[string]any{}, false, false, false
	}
	if m, ok := raw.(map[string]any); ok {
		return m, false, false, false
	}

	var text string
	switch v := raw.(type) {
	case string:
		text = v
	default:
		b, err := json.Marshal(v, json.Deterministic(true))
		if err != nil {
			return nil, false, false, true
		}
		text = string(b)
	}

	coerced := CoerceJSONOutput(text)
	if coerced == nil {
		return nil, false, false, true
	}
	m, ok := coerced.Value.(map[string]any)
	if !ok || m == nil {
		return nil, coerced.Repaired, coerced.Truncated, false
	}
	return m, coerced.Repaired, coerced.Truncated, false
}

// FilterResult holds output of FilterArguments.
type FilterResult struct {
	Args    map[string]any
	Dropped []string
	Filled  []string
	Missing []string
}

// FilterArguments cleans tool arguments against their declared JSON schema:
// drops undeclared properties, fills safe defaults for missing required properties.
func FilterArguments(args map[string]any, schema map[string]any) FilterResult {
	if args == nil || schema == nil {
		return FilterResult{Args: args}
	}

	properties, _ := schema["properties"].(map[string]any)
	allowed := make(map[string]bool)
	if properties != nil {
		for k := range properties {
			allowed[k] = true
		}
	}

	var required []string
	if reqArr, ok := schema["required"].([]any); ok {
		for _, item := range reqArr {
			if s, ok := item.(string); ok && s != "" {
				required = append(required, s)
			}
		}
	}

	result := make(map[string]any)
	var dropped []string
	for k, v := range args {
		if len(allowed) > 0 && !allowed[k] {
			dropped = append(dropped, k)
			continue
		}
		result[k] = v
	}

	var filled []string
	var missing []string
	for _, reqKey := range required {
		if _, exists := result[reqKey]; exists {
			continue
		}
		prop, _ := properties[reqKey].(map[string]any)
		if prop != nil {
			if d, ok := prop["default"]; ok {
				result[reqKey] = d
				filled = append(filled, reqKey)
				continue
			}
			typ, _ := prop["type"].(string)
			switch typ {
			case "boolean":
				result[reqKey] = false
				filled = append(filled, reqKey)
				continue
			case "number", "integer":
				result[reqKey] = 0
				filled = append(filled, reqKey)
				continue
			case "array":
				result[reqKey] = []any{}
				filled = append(filled, reqKey)
				continue
			}
		}
		missing = append(missing, reqKey)
	}

	return FilterResult{
		Args:    result,
		Dropped: dropped,
		Filled:  filled,
		Missing: missing,
	}
}

// JSONGuardStats tracks telemetry for ApplyJSONGuard.
type JSONGuardStats struct {
	TextRepaired int `json:"textRepaired"`
	ArgsRepaired int `json:"argsRepaired"`
	ArgsDropped  int `json:"argsDropped"`
	ArgsCleaned  int `json:"argsCleaned"`
	ArgsFilled   int `json:"argsFilled"`
	ArgsMissing  int `json:"argsMissing"`
}

func wantsJSONAnswer(text string, payload map[string]any) bool {
	if text == "" {
		return false
	}
	if rf, ok := payload["response_format"].(map[string]any); ok {
		if typ, ok := rf["type"].(string); ok && typ == "json_object" {
			return true
		}
	}
	if fencedBlockRe.MatchString(text) {
		return true
	}
	probe := strings.TrimSpace(text)
	opener := -1
	for i, ch := range probe {
		if ch == '{' || ch == '[' {
			opener = i
			break
		}
	}
	if opener == -1 {
		return false
	}
	_, ok := ScanBalancedJSON(probe[opener:])
	return ok
}

func indexToolSchemas(tools []any) map[string]map[string]any {
	if len(tools) == 0 {
		return nil
	}
	schemas := make(map[string]map[string]any)
	for _, raw := range tools {
		t, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := toolName(t)
		if name != "" {
			if s := toolSchema(t); s != nil {
				schemas[name] = s
			}
		}
	}
	if len(schemas) == 0 {
		return nil
	}
	return schemas
}

// ApplyJSONGuard inspects completion payload, repairing JSON in message text and tool_calls arguments.
func ApplyJSONGuard(payload map[string]any, tools []any) (bool, JSONGuardStats) {
	var stats JSONGuardStats
	if payload == nil {
		return false, stats
	}

	schemas := indexToolSchemas(tools)

	if choices, ok := payload["choices"].([]any); ok {
		for _, rawC := range choices {
			choice, ok := rawC.(map[string]any)
			if !ok {
				continue
			}
			msg, ok := choice["message"].(map[string]any)
			if !ok {
				continue
			}

			// 1. Text content JSON repair if requested or formatted
			if content, ok := msg["content"].(string); ok && strings.TrimSpace(content) != "" && wantsJSONAnswer(content, payload) {
				coerced := CoerceJSONOutput(content)
				if coerced != nil && coerced.Repaired {
					b, err := json.Marshal(coerced.Value, json.Deterministic(true))
					if err == nil {
						msg["content"] = string(b)
						stats.TextRepaired++
					}
				}
			}

			// 2. tool_calls argument repair and schema filtering
			if tcs, ok := msg["tool_calls"].([]any); ok {
				var validTCs []any
				for _, rawTC := range tcs {
					tc, ok := rawTC.(map[string]any)
					if !ok {
						continue
					}
					fn, _ := tc["function"].(map[string]any)
					if fn == nil {
						continue
					}
					coercedArgs, repaired, _, unparseable := CoerceToolArguments(fn["arguments"])
					if unparseable || coercedArgs == nil {
						stats.ArgsDropped++
						continue
					}
					if repaired {
						b, _ := json.Marshal(coercedArgs, json.Deterministic(true))
						fn["arguments"] = string(b)
						stats.ArgsRepaired++
					}
					if schemas != nil {
						fnName, _ := fn["name"].(string)
						if sch := schemas[fnName]; sch != nil {
							cleaned := FilterArguments(coercedArgs, sch)
							if len(cleaned.Dropped) > 0 || len(cleaned.Filled) > 0 {
								stats.ArgsCleaned++
								stats.ArgsFilled += len(cleaned.Filled)
								stats.ArgsMissing += len(cleaned.Missing)
								b, _ := json.Marshal(cleaned.Args, json.Deterministic(true))
								fn["arguments"] = string(b)
							}
						}
					}
					validTCs = append(validTCs, tc)
				}
				msg["tool_calls"] = validTCs
			}
		}
	}

	// Claude native tool_use block input repair
	if content, ok := payload["content"].([]any); ok {
		for _, rawB := range content {
			b, ok := rawB.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := b["type"].(string)
			if typ == "tool_use" {
				if inputStr, ok := b["input"].(string); ok {
					coercedArgs, repaired, _, _ := CoerceToolArguments(inputStr)
					if coercedArgs != nil {
						b["input"] = coercedArgs
						if repaired {
							stats.ArgsRepaired++
						}
					}
				}
			}
		}
	}

	changed := stats.TextRepaired+stats.ArgsRepaired+stats.ArgsDropped+stats.ArgsCleaned > 0
	return changed, stats
}
