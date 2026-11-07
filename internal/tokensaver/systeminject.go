// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package tokensaver

import (
	json "encoding/json/v2"
	"strings"
)

// systeminject.go is the wire-aware per-key system-prompt injector
// (port(serenhope) TASK-1.5, upstream open-sse/rtk/systemInject.js). It appends
// a key's own system prompt into the system channel of the request body as the
// client wrote it, dispatching by wire shape: Claude (labelled by the
// endpoint), then Kiro, Gemini, and finally the OpenAI family where the shape
// sniff decides — instructions string, messages[], input[] or a bare input
// string that must stay untouched.
//
// Two contracts travel with the port. Idempotency is EXACT, not a substring:
// the prompt counts as present only when it is the whole haystack or one full
// blank-line-delimited segment of it (upstream's hasPrompt; the test suite
// there explicitly pins "never uses a first-100 fingerprint"), which is what
// makes a retried injection a no-op. And the whole function fails open: an
// unparseable or frozen body is returned untouched rather than failing the
// request — injection is an enrichment, never a gate.

// keyPromptSEP separates appended prompt segments, upstream's SEP.
const keyPromptSEP = "\n\n"

// hasPromptSegment reports whether the prompt is already present as its own
// segment (or the whole string).
func hasPromptSegment(haystack, prompt string) bool {
	if haystack == "" {
		return false
	}
	if haystack == prompt {
		return true
	}
	for _, seg := range strings.Split(haystack, keyPromptSEP) {
		if seg == prompt {
			return true
		}
	}
	return false
}

// dedupSegmentAppend appends the prompt after a blank line unless it is
// already there.
func dedupSegmentAppend(curr, prompt string) string {
	if curr == "" {
		return prompt
	}
	if hasPromptSegment(curr, prompt) {
		return curr
	}
	return curr + keyPromptSEP + prompt
}

// InjectKeySystemPrompt injects the per-key system prompt into a client body.
// claudeNative marks a /v1/messages body: Anthropic rejects a role:"system"
// turn inside messages[], so the system channel is the top-level "system"
// field. The returned bool reports that the body changed; (body, false) means
// untouched — including every failure.
func InjectKeySystemPrompt(body []byte, claudeNative bool, prompt string) ([]byte, bool) {
	if len(body) == 0 || prompt == "" {
		return body, false
	}
	var req map[string]any
	// Numbered decode: an injected body is re-marshaled whole, and a plain
	// any-decode would round >2^53 integers in it through float64.
	if err := unmarshalNumbered(body, &req); err != nil {
		return body, false
	}
	if req == nil {
		return body, false
	}

	switch {
	case claudeNative:
		if out, did := injectKeyClaudeSystem(req, prompt); did {
			return out, true
		}
		return body, false
	case isKiroRequestBody(req):
		if out, did := injectKeyKiroSystem(req, prompt); did {
			return out, true
		}
		return body, false
	}
	// Gemini shapes are sniffed, not labelled: only a body that already owns
	// one of the system-instruction spellings or a contents[] array takes the
	// Gemini path, so an OpenAI body can never fall into it. Antigravity
	// wraps the same shape one level down in "request".
	if _, ok := req["systemInstruction"]; ok {
		return injectKeyGemini(req, prompt)
	}
	if _, ok := req["system_instruction"]; ok {
		return injectKeyGemini(req, prompt)
	}
	if _, ok := req["contents"]; ok {
		return injectKeyGemini(req, prompt)
	}
	if inner, ok := req["request"].(map[string]any); ok {
		if _, ok := inner["contents"]; ok {
			return injectKeyGemini(req, prompt)
		}
		if _, ok := inner["systemInstruction"]; ok {
			return injectKeyGemini(req, prompt)
		}
		if _, ok := inner["system_instruction"]; ok {
			return injectKeyGemini(req, prompt)
		}
	}

	// OpenAI family, upstream's own dispatch order: the instructions string
	// takes precedence, then messages[], then input[], and a string input is
	// deliberately untouched. A sub-injector answers (nil, false) when it
	// made no change; the original body always comes back then — a caller
	// that ignores the bool still gets usable bytes.
	if s, ok := req["instructions"].(string); ok {
		if out, did := injectKeyInstructionsSystem(req, s, prompt); did {
			return out, true
		}
		return body, false
	}
	if arr := anyToArray(req["messages"]); arr != nil {
		if out, did := injectKeyChatSystem(req, arr, prompt); did {
			return out, true
		}
		return body, false
	}
	if arr := anyToArray(req["input"]); arr != nil {
		if out, did := injectKeyResponsesInputSystem(req, arr, prompt); did {
			return out, true
		}
		return body, false
	}
	if _, ok := req["input"].(string); ok {
		return body, false
	}
	// OpenAI-shaped but nothing injectable: no-op.
	return body, false
}

// anyToArray widens a decoded JSON value to an array, or nil.
func anyToArray(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	return arr
}

// remarshal encodes the mutated map; a failure is fail-open (nil, false).
func remarshal(req map[string]any) ([]byte, bool) {
	out, err := json.Marshal(req, json.Deterministic(true))
	if err != nil {
		return nil, false
	}
	return out, true
}

// ---- OpenAI instructions string ----

func injectKeyInstructionsSystem(req map[string]any, curr, prompt string) ([]byte, bool) {
	next := dedupSegmentAppend(curr, prompt)
	if next == curr {
		return nil, false
	}
	req["instructions"] = next
	return remarshal(req)
}

// ---- OpenAI Chat messages[] ----

func injectKeyChatSystem(req map[string]any, arr []any, prompt string) ([]byte, bool) {
	if keyMessagesCarryPrompt(arr, prompt) {
		return nil, false
	}
	for _, m := range arr {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			next := dedupSegmentAppend(content, prompt)
			if next == content {
				return nil, false
			}
			msg["content"] = next
		case []any:
			if keyBlocksCarryText(content, prompt) {
				return nil, false
			}
			msg["content"] = append(content, map[string]any{"type": "text", "text": prompt})
		default:
			msg["content"] = prompt
		}
		return remarshal(req)
	}
	// No system turn: one is unshifted at index 0.
	req["messages"] = append([]any{map[string]any{"role": "system", "content": prompt}}, arr...)
	return remarshal(req)
}

// keyMessagesCarryPrompt scans the system/developer turns for the prompt,
// string or block-array content alike.
func keyMessagesCarryPrompt(arr []any, prompt string) bool {
	for _, m := range arr {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			if hasPromptSegment(content, prompt) {
				return true
			}
		case []any:
			if keyBlocksCarryText(content, prompt) {
				return true
			}
		}
	}
	return false
}

// keyBlocksCarryText reports whether one block in the array carries the prompt
// as its whole text.
func keyBlocksCarryText(blocks []any, prompt string) bool {
	for _, block := range blocks {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := b["text"].(string); ok && hasPromptSegment(t, prompt) {
			return true
		}
	}
	return false
}

// ---- OpenAI Responses input[] ----

func injectKeyResponsesInputSystem(req map[string]any, arr []any, prompt string) ([]byte, bool) {
	if keyResponsesInputCarryPrompt(arr, prompt) {
		return nil, false
	}
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		role, _ := m["role"].(string)
		if typ != "message" || (role != "system" && role != "developer") {
			continue
		}
		switch content := m["content"].(type) {
		case string:
			next := dedupSegmentAppend(content, prompt)
			if next != content {
				m["content"] = next
			}
		case []any:
			if keyBlocksCarryText(content, prompt) {
				return nil, false
			}
			m["content"] = append(content, map[string]any{"type": "input_text", "text": prompt})
		default:
			m["content"] = []any{map[string]any{"type": "input_text", "text": prompt}}
		}
		return remarshal(req)
	}
	// No system item: one is unshifted at index 0, in the item shape the
	// Responses schema wants — a block array, never a bare string.
	req["input"] = append([]any{map[string]any{
		"type": "message", "role": "system",
		"content": []any{map[string]any{"type": "input_text", "text": prompt}},
	}}, arr...)
	return remarshal(req)
}

func keyResponsesInputCarryPrompt(arr []any, prompt string) bool {
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		role, _ := m["role"].(string)
		if typ != "message" || (role != "system" && role != "developer") {
			continue
		}
		switch content := m["content"].(type) {
		case string:
			if hasPromptSegment(content, prompt) {
				return true
			}
		case []any:
			if keyBlocksCarryText(content, prompt) {
				return true
			}
		}
	}
	return false
}

// ---- Claude ----

func injectKeyClaudeSystem(req map[string]any, prompt string) ([]byte, bool) {
	switch sys := req["system"].(type) {
	case string:
		next := dedupSegmentAppend(sys, prompt)
		if next == sys {
			return nil, false
		}
		req["system"] = next
	case []any:
		if keyBlocksCarryText(sys, prompt) {
			return nil, false
		}
		block := map[string]any{"type": "text", "text": prompt}
		// A block carrying cache_control is a cache breakpoint: the new block
		// goes BEFORE the last one so the breakpoint stays the final element
		// and the prompt rides inside the cached prefix.
		lastCache := -1
		for i := len(sys) - 1; i >= 0; i-- {
			if b, ok := sys[i].(map[string]any); ok && b["cache_control"] != nil {
				lastCache = i
				break
			}
		}
		if lastCache >= 0 {
			res := make([]any, 0, len(sys)+1)
			res = append(res, sys[:lastCache]...)
			res = append(res, block)
			res = append(res, sys[lastCache:]...)
			req["system"] = res
		} else {
			req["system"] = append(sys, block)
		}
	case nil:
		req["system"] = prompt
	default:
		// A system field of an unexpected shape is left alone — the fail-open
		// contract beats overwriting whatever the client sent.
		return nil, false
	}
	return remarshal(req)
}

// ---- Gemini ----

func injectKeyGemini(req map[string]any, prompt string) ([]byte, bool) {
	// Antigravity wraps the Gemini body one level down in "request".
	target := req
	if inner, ok := req["request"].(map[string]any); ok {
		target = inner
	}
	// Whatever spelling the body already uses wins; a body with neither gets
	// the camelCase field the Gemini API documents.
	key := "systemInstruction"
	if _, ok := target["system_instruction"]; ok {
		key = "system_instruction"
	}
	existing, _ := target[key].(map[string]any)
	if parts := anyToArray(existing["parts"]); parts != nil {
		if keyPartsCarryText(parts, prompt) {
			return nil, false
		}
		existing["parts"] = append(parts, map[string]any{"text": prompt})
		return remarshal(req)
	}
	target[key] = map[string]any{"parts": []any{map[string]any{"text": prompt}}}
	return remarshal(req)
}

func keyPartsCarryText(parts []any, prompt string) bool {
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := pm["text"].(string); ok && hasPromptSegment(t, prompt) {
			return true
		}
	}
	return false
}

// ---- Kiro ----

// isKiroRequestBody sniffs the Kiro conversationState turn shape — the same
// gate upstream uses after its top-level systemPrompt marker stopped being
// emitted (kiro.dev rejects that field with REQUEST_BODY_INVALID).
func isKiroRequestBody(req map[string]any) bool {
	cs, ok := req["conversationState"].(map[string]any)
	if !ok {
		return false
	}
	for _, item := range anyToArray(cs["history"]) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := m["userInputMessage"]; ok {
			return true
		}
		if _, ok := m["assistantResponseMessage"]; ok {
			return true
		}
	}
	if cur, ok := cs["currentMessage"].(map[string]any); ok {
		if _, ok := cur["userInputMessage"]; ok {
			return true
		}
	}
	return false
}

// injectKeyKiroSystem appends the prompt to the first user turn's content —
// the place the Kiro translator mirrors system text — and deliberately never
// writes a top-level systemPrompt, which the upstream gateway rejects.
func injectKeyKiroSystem(req map[string]any, prompt string) ([]byte, bool) {
	cs, _ := req["conversationState"].(map[string]any)
	if cs == nil {
		return nil, false
	}
	var targetMsg map[string]any
	for _, item := range anyToArray(cs["history"]) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if um, ok := m["userInputMessage"].(map[string]any); ok {
			targetMsg = um
			break
		}
	}
	if targetMsg == nil {
		if cur, ok := cs["currentMessage"].(map[string]any); ok {
			targetMsg, _ = cur["userInputMessage"].(map[string]any)
		}
	}
	if targetMsg == nil {
		return nil, false
	}
	content, _ := targetMsg["content"].(string)
	next := dedupSegmentAppend(content, prompt)
	if next == content {
		return nil, false
	}
	targetMsg["content"] = next
	return remarshal(req)
}
