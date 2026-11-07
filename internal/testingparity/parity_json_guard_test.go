// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package testingparity holds contract tests that pin 9RouterX's output to the
// serenhope reference implementation (FASE 8 task 2): the same input must
// produce the same output here as `open-sse` produces upstream. Each table
// names the upstream test case it ports, so a divergence can be traced back to
// the exact vitest row that defines the contract.
//
// The package is deliberately separate from every product package so its
// helpers can never leak into production test binaries, and so the parity
// suite can be run (or failed) as a unit: `go test ./internal/testingparity/`.
package testingparity

import (
	"strings"
	"testing"

	json "encoding/json/v2"

	"9routerx/proxy/internal/translator"
)

// canonicalJSON marshals deterministically so parsed values can be compared
// without depending on map iteration order or Go number typing (the upstream
// assertions use JS ===/toEqual semantics, where 1 and 1.0 are the same value).
func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatalf("marshal %#v: %v", v, err)
	}
	return string(b)
}

// parseCanonical unmarshals an expected JSON literal through the same decoder
// CoerceJSONOutput uses, so both sides of a comparison carry identical typing.
func parseCanonical(t *testing.T, literal string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(literal), &v); err != nil {
		t.Fatalf("unmarshal expected literal %q: %v", literal, err)
	}
	return v
}

// TestSerenehopeCoerceJSONOutput ports the coerceJsonOutput table from
// serenhope tests/plugins-json-guard.test.js (describe "jsonGuard:
// coerceJsonOutput") one row per `it()`. Serenhope-parity: same inputs, same
// expected values, same repaired/truncated flags.
func TestSerenehopeCoerceJSONOutput(t *testing.T) {
	type row struct {
		upstream  string // the upstream it() title this row ports
		in        string
		wantValue string // expected JSON literal, "" when coercion must fail
		repaired  bool
		truncated bool
	}
	rows := []row{
		{
			upstream:  "leaves valid JSON untouched",
			in:        `{"a":1,"b":[1,2]}`,
			wantValue: `{"a":1,"b":[1,2]}`,
		},
		{
			upstream:  "extracts a fenced payload out of prose",
			in:        "Sure! Here it is:\n```json\n{\"ok\":true}\n```\nHope that helps.",
			wantValue: `{"ok":true}`,
			repaired:  true,
		},
		{
			upstream:  "extracts a balanced value from surrounding prose",
			in:        `The result is {"name":"x","tags":["a","b"]} as requested.`,
			wantValue: `{"name":"x","tags":["a","b"]}`,
			repaired:  true,
		},
		{
			upstream:  "repairs Python literals, single quotes and trailing commas (True/False/None)",
			in:        `{"a":True,"b":False,"c":None}`,
			wantValue: `{"a":true,"b":false,"c":null}`,
			repaired:  true,
		},
		{
			upstream:  "repairs Python literals, single quotes and trailing commas (single quotes)",
			in:        `{'name': 'budi'}`,
			wantValue: `{"name":"budi"}`,
			repaired:  true,
		},
		{
			upstream:  "repairs Python literals, single quotes and trailing commas (trailing comma)",
			in:        `{"a":1,"b":2,}`,
			wantValue: `{"a":1,"b":2}`,
			repaired:  true,
		},
		{
			upstream:  "closes a payload the output limit cut off",
			in:        `{"path":"/tmp/x","content":"abc","opt`,
			wantValue: `{"path":"/tmp/x","content":"abc"}`,
			repaired:  true,
			truncated: true,
		},
		{
			// The 3 is a finished value, not a torn one - closing must keep it.
			upstream:  "keeps a complete trailing value when closing",
			in:        `{"outer":{"inner":[1,2,3`,
			wantValue: `{"outer":{"inner":[1,2,3]}}`,
			repaired:  true,
			truncated: true,
		},
		{
			upstream: "refuses plain prose",
			in:       "I cannot answer that question.",
		},
		{
			upstream: "refuses unbalanced garbage",
			in:       "{{{{{ not json",
		},
		{
			upstream: "refuses the empty string",
			in:       "",
		},
	}

	for _, r := range rows {
		t.Run(strings.ReplaceAll(r.upstream, " ", "_"), func(t *testing.T) {
			got := translator.CoerceJSONOutput(r.in)

			if r.wantValue == "" {
				// Upstream expects null. The JS wrapper also feeds a literal
				// null input in the same it() row; the Go signature takes a
				// string, so the null-input arm of that row is the empty
				// string here, which must fail the same way.
				if got != nil {
					t.Fatalf("serenhope-parity %q: expected nil, got value=%s repaired=%v",
						r.upstream, canonicalJSON(t, got.Value), got.Repaired)
				}
				return
			}

			if got == nil {
				t.Fatalf("serenhope-parity %q: coercion failed, want %s", r.upstream, r.wantValue)
			}
			if got.Repaired != r.repaired {
				t.Errorf("serenhope-parity %q: repaired = %v, want %v", r.upstream, got.Repaired, r.repaired)
			}
			if got.Truncated != r.truncated {
				t.Errorf("serenhope-parity %q: truncated = %v, want %v", r.upstream, got.Truncated, r.truncated)
			}
			if want, have := parseCanonical(t, r.wantValue), got.Value; canonicalJSON(t, have) != canonicalJSON(t, want) {
				t.Errorf("serenhope-parity %q: value = %s, want %s", r.upstream, canonicalJSON(t, have), canonicalJSON(t, want))
			}
		})
	}
}

// TestSerenehopeRepairJSONTextPreservesStringLiterals ports the upstream row
// "never rewrites values inside string literals": a coding slip spelled inside
// a JSON string is content, not a slip, and must survive repair verbatim.
func TestSerenehopeRepairJSONTextPreservesStringLiterals(t *testing.T) {
	in := `{"msg":"the answer is True here"}`
	out := translator.RepairJSONText(in)
	v := parseCanonical(t, out)
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("repair produced %T, want object", v)
	}
	if msg, _ := m["msg"].(string); msg != "the answer is True here" {
		t.Errorf("serenhope-parity: msg = %q, want the literal untouched", msg)
	}
}
