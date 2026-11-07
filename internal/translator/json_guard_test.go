// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

import (
	"reflect"
	"testing"

	jsonv1 "encoding/json"
	json "encoding/json/v2"
)

// jsonNumberIs reports whether v holds the numeric value want in any of the
// representations the repair ladder can produce: float64 (plain decode),
// int64, or json.Number (unmarshalNumbered — the number preservation applied
// so >2^53 IDs survive the re-marshal of a repaired payload).
func jsonNumberIs(v any, want float64) bool {
	switch n := v.(type) {
	case float64:
		return n == want
	case int64:
		return float64(n) == want
	case jsonv1.Number:
		f, err := n.Float64()
		return err == nil && f == want
	}
	return false
}

func TestJSONGuardCoerceOutput(t *testing.T) {
	t.Run("leaves_valid_json_untouched", func(t *testing.T) {
		r := CoerceJSONOutput(`{"a":1,"b":[1,2]}`)
		if r == nil || r.Repaired {
			t.Fatalf("expected repaired = false, got %+v", r)
		}
	})

	t.Run("extracts_fenced_payload_out_of_prose", func(t *testing.T) {
		text := "Sure! Here it is:\n```json\n{\"ok\":true}\n```\nHope that helps."
		r := CoerceJSONOutput(text)
		if r == nil || !r.Repaired {
			t.Fatalf("expected repaired = true, got %+v", r)
		}
		m, ok := r.Value.(map[string]any)
		if !ok || m["ok"] != true {
			t.Errorf("value = %v, want map[ok:true]", r.Value)
		}
	})

	t.Run("extracts_balanced_value_from_prose", func(t *testing.T) {
		text := `The result is {"name":"x","tags":["a","b"]} as requested.`
		r := CoerceJSONOutput(text)
		if r == nil || !r.Repaired {
			t.Fatalf("expected repaired = true, got %+v", r)
		}
		m := r.Value.(map[string]any)
		if m["name"] != "x" {
			t.Errorf("name = %v, want x", m["name"])
		}
	})

	t.Run("repairs_python_literals_single_quotes_and_trailing_commas", func(t *testing.T) {
		r1 := CoerceJSONOutput(`{"a":True,"b":False,"c":None}`)
		if r1 == nil {
			t.Fatal("r1 is nil")
		}
		m1 := r1.Value.(map[string]any)
		if m1["a"] != true || m1["b"] != false || m1["c"] != nil {
			t.Errorf("m1 = %+v", m1)
		}

		r2 := CoerceJSONOutput("{'name': 'budi'}")
		if r2 == nil {
			t.Fatal("r2 is nil")
		}
		if r2.Value.(map[string]any)["name"] != "budi" {
			t.Errorf("m2 = %+v", r2.Value)
		}

		r3 := CoerceJSONOutput(`{"a":1,"b":2,}`)
		if r3 == nil {
			t.Fatal("r3 is nil")
		}
		if !jsonNumberIs(r3.Value.(map[string]any)["a"], 1) {
			t.Errorf("m3 = %+v", r3.Value)
		}
	})

	t.Run("closes_payload_cut_off_by_output_limit", func(t *testing.T) {
		text := `{"path":"/tmp/x","content":"abc","opt`
		r := CoerceJSONOutput(text)
		if r == nil || !r.Truncated {
			t.Fatalf("expected truncated = true, got %+v", r)
		}
		m := r.Value.(map[string]any)
		if m["path"] != "/tmp/x" || m["content"] != "abc" {
			t.Errorf("unexpected value: %+v", m)
		}
	})

	t.Run("keeps_complete_trailing_value_when_closing", func(t *testing.T) {
		text := `{"outer":{"inner":[1,2,3`
		r := CoerceJSONOutput(text)
		if r == nil {
			t.Fatal("r is nil")
		}
		m := r.Value.(map[string]any)
		outer := m["outer"].(map[string]any)
		inner := outer["inner"].([]any)
		if len(inner) != 3 {
			t.Errorf("inner len = %d, want 3", len(inner))
		}
	})

	t.Run("refuses_plain_prose_and_garbage", func(t *testing.T) {
		if r := CoerceJSONOutput("I cannot answer that question."); r != nil {
			t.Errorf("expected nil for prose, got %+v", r)
		}
		if r := CoerceJSONOutput("{{{{{ not json"); r != nil {
			t.Errorf("expected nil for garbage, got %+v", r)
		}
		if r := CoerceJSONOutput(""); r != nil {
			t.Errorf("expected nil for empty")
		}
	})

	t.Run("never_rewrites_values_inside_string_literals", func(t *testing.T) {
		repaired := RepairJSONText(`{"msg":"the answer is True here"}`)
		var m map[string]any
		_ = json.Unmarshal([]byte(repaired), &m)
		if m["msg"] != "the answer is True here" {
			t.Errorf("value inside quotes was corrupted: %v", m["msg"])
		}
	})
}

func TestJSONGuardHelpers(t *testing.T) {
	t.Run("scanBalancedJSON_ignores_braces_in_strings", func(t *testing.T) {
		raw := `prefix {"text":"a } b { c","n":1} suffix`
		res, ok := ScanBalancedJSON(raw)
		if !ok || res != `{"text":"a } b { c","n":1}` {
			t.Errorf("got %q, want balanced JSON container", res)
		}
	})

	t.Run("fencedBlock_extracts_and_trims", func(t *testing.T) {
		res, ok := FencedBlock("x ```json\n[1,2]\n``` y")
		if !ok || res != "[1,2]" {
			t.Errorf("got %q, want [1,2]", res)
		}
		if _, ok := FencedBlock("no fence here"); ok {
			t.Error("expected false for no fence")
		}
	})

	t.Run("closeTruncatedJSON_idempotent_on_balanced", func(t *testing.T) {
		val := `{"a":1}`
		if got := CloseTruncatedJSON(val); got != val {
			t.Errorf("got %q, want %q", got, val)
		}
	})

	t.Run("negativeInfinity_becomes_null_not_dash_null", func(t *testing.T) {
		// The bare Infinity rule must not eat the tail of -Infinity first:
		// that produced "-null", which nothing downstream can parse.
		r := CoerceJSONOutput(`{"t":-Infinity}`)
		if r == nil {
			t.Fatal("r is nil")
		}
		m, ok := r.Value.(map[string]any)
		if !ok {
			t.Fatalf("value = %T", r.Value)
		}
		if m["t"] != nil {
			t.Errorf("t = %v, want nil", m["t"])
		}
	})

	t.Run("double_quoted_apostrophes_are_untouched", func(t *testing.T) {
		in := `{"note":"It's True, isn't it?"}`
		if got := RepairJSONText(in); got != in {
			t.Errorf("RepairJSONText corrupted a double-quoted string: %q", got)
		}
	})

	t.Run("single_quoted_escaped_apostrophe_parses_after_repair", func(t *testing.T) {
		// \' is not a legal JSON escape, so the naive "..."-swap left an
		// unparseable string behind; the converter now emits a bare '.
		r := CoerceJSONOutput(`{'a':'it\'s', 'b':'ok'}`)
		if r == nil {
			t.Fatal("r is nil")
		}
		m, ok := r.Value.(map[string]any)
		if !ok {
			t.Fatalf("value = %T", r.Value)
		}
		if m["a"] != "it's" || m["b"] != "ok" {
			t.Errorf("m = %+v", m)
		}
	})

	t.Run("literals_inside_double_quoted_strings_are_untouched", func(t *testing.T) {
		in := `{"lit":"True False None NaN"}`
		if got := RepairJSONText(in); got != in {
			t.Errorf("mapOutsideStrings leaked into a string literal: %q", got)
		}
	})

	t.Run("scanBalancedJSON_handles_escaped_quotes", func(t *testing.T) {
		text := `{"a":"say \"hi\"","b":[1,2]} trailing`
		got, ok := ScanBalancedJSON(text)
		if !ok {
			t.Fatal("ok = false")
		}
		if got != `{"a":"say \"hi\"","b":[1,2]}` {
			t.Errorf("got %q", got)
		}
	})
}

func TestFilterArguments(t *testing.T) {
	t.Run("drops_undeclared_keys", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []any{"path"},
		}
		args := map[string]any{"nope": 1}
		r := FilterArguments(args, schema)
		if !reflect.DeepEqual(r.Dropped, []string{"nope"}) {
			t.Errorf("dropped = %v, want [nope]", r.Dropped)
		}
		if !reflect.DeepEqual(r.Missing, []string{"path"}) {
			t.Errorf("missing = %v, want [path]", r.Missing)
		}
	})

	t.Run("fills_scalar_defaults_from_schema", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"verbose": map[string]any{"type": "boolean"},
				"limit":   map[string]any{"type": "number"},
				"tags":    map[string]any{"type": "array"},
			},
			"required": []any{"path", "verbose", "limit", "tags"},
		}
		args := map[string]any{"path": "a"}
		r := FilterArguments(args, schema)

		if r.Args["path"] != "a" || r.Args["verbose"] != false || r.Args["limit"] != 0 {
			t.Errorf("unexpected args: %+v", r.Args)
		}
		if tags, ok := r.Args["tags"].([]any); !ok || len(tags) != 0 {
			t.Errorf("tags not filled with empty array: %+v", r.Args["tags"])
		}
	})
}

func TestApplyJSONGuardCompletion(t *testing.T) {
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "read",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
					"required": []any{"path"},
				},
			},
		},
	}

	t.Run("repairs_text_and_cleans_tool_calls", func(t *testing.T) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": "```json\n{\"x\":True}\n```",
						"tool_calls": []any{
							map[string]any{
								"function": map[string]any{
									"name":      "read",
									"arguments": `{"path":"a","nope":1,}`,
								},
							},
						},
					},
				},
			},
		}

		changed, stats := ApplyJSONGuard(payload, tools)
		if !changed {
			t.Fatal("expected changed = true")
		}
		if stats.TextRepaired != 1 {
			t.Errorf("textRepaired = %d, want 1", stats.TextRepaired)
		}
		msg := payload["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
		if msg["content"] != `{"x":true}` {
			t.Errorf("content = %v, want {\"x\":true}", msg["content"])
		}
		tcs := msg["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
		var args map[string]any
		_ = json.Unmarshal([]byte(tcs["arguments"].(string)), &args)
		if args["path"] != "a" || args["nope"] != nil {
			t.Errorf("cleaned arguments = %+v, want only path:a", args)
		}
	})

	t.Run("leaves_normal_prose_alone", func(t *testing.T) {
		payload := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{"content": "Just a normal answer.", "tool_calls": []any{}},
				},
			},
		}
		changed, _ := ApplyJSONGuard(payload, nil)
		if changed {
			t.Errorf("changed = true, want false for plain prose")
		}
	})

	t.Run("repairs_claude_tool_use_input", func(t *testing.T) {
		payload := map[string]any{
			"content": []any{
				map[string]any{"type": "tool_use", "input": `{"a":1,}`},
			},
		}
		changed, _ := ApplyJSONGuard(payload, nil)
		if !changed {
			t.Fatal("changed = false, want true for claude tool_use repair")
		}
		blk := payload["content"].([]any)[0].(map[string]any)
		m, ok := blk["input"].(map[string]any)
		if !ok || !jsonNumberIs(m["a"], 1) {
			t.Errorf("input = %+v", blk["input"])
		}
	})

	t.Run("tolerates_empty_payloads", func(t *testing.T) {
		if changed, _ := ApplyJSONGuard(nil, nil); changed {
			t.Error("changed = true for nil")
		}
		if changed, _ := ApplyJSONGuard(map[string]any{}, nil); changed {
			t.Error("changed = true for empty")
		}
	})
}
