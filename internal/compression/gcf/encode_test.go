// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package gcf

import (
	"math"
	"reflect"
	"testing"

	jsonv1 "encoding/json"
)

// normalizeAny canonicalizes decoded values so round-trip comparisons judge
// JSON value equality, not incidental Go typing: jsonv1.Number (what the
// attachment parser returns for numbers) normalizes to int64/float64, and
// nil maps/slices normalize to their empty forms since the wire format has
// no way to distinguish them.
func normalizeAny(v any) any {
	switch x := v.(type) {
	case jsonv1.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		if x == nil {
			return map[string]any{}
		}
		for k, e := range x {
			x[k] = normalizeAny(e)
		}
		return x
	case []any:
		if x == nil {
			return []any{}
		}
		for i, e := range x {
			x[i] = normalizeAny(e)
		}
		return x
	}
	return v
}

// assertRoundTrip encodes rows, decodes the block and deep-compares the
// result against the original, reporting the encoded form on failure so the
// offending wire format is visible.
func assertRoundTrip(t *testing.T, name string, rows []map[string]any) {
	t.Helper()
	enc, err := Encode(rows)
	if err != nil {
		t.Fatalf("%s: Encode() error = %v", name, err)
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("%s: Decode(%q) error = %v", name, enc, err)
	}
	if len(dec) != len(rows) {
		t.Fatalf("%s: decoded %d rows, want %d\nblock:\n%s", name, len(dec), len(rows), enc)
	}
	for i := range rows {
		want := normalizeAny(rows[i])
		got := normalizeAny(dec[i])
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: row %d round-trip mismatch\ngot:  %#v\nwant: %#v\nblock:\n%s", name, i, got, want, enc)
		}
	}
}

// roundTripCases is the losslessness corpus: every case must survive
// decode(encode(x)) deep-equal to x with absent keys omitted.
func roundTripCases() []struct {
	name string
	rows []map[string]any
} {
	return []struct {
		name string
		rows []map[string]any
	}{
		{
			name: "homogeneous rows",
			rows: []map[string]any{
				{"id": int64(1), "name": "ada", "active": true},
				{"id": int64(2), "name": "bob", "active": false},
				{"id": int64(3), "name": "eve", "active": true},
			},
		},
		{
			name: "heterogeneous rows with missing keys",
			rows: []map[string]any{
				{"a": int64(1), "b": "x"},
				{"b": "y", "c": int64(3)},
				{"a": nil},
			},
		},
		{
			name: "nulls and booleans",
			rows: []map[string]any{
				{"n": nil, "ok": true, "off": false},
			},
		},
		{
			name: "nested objects flatten to path columns",
			rows: []map[string]any{
				{"customer": map[string]any{"name": "ada", "address": map[string]any{"city": "london"}}},
				{"customer": map[string]any{"name": "bob"}},
			},
		},
		{
			name: "primitive arrays including empty and null elements",
			rows: []map[string]any{
				{"tags": []any{"a", "b"}, "nums": []any{int64(1), int64(2), int64(3)}, "empty": []any{}, "mixed": []any{int64(1), nil, "x"}},
			},
		},
		{
			name: "unicode",
			rows: []map[string]any{
				{"s": "héllo 世界 🚀", "ar": "مرحبا"},
			},
		},
		{
			name: "strings with delimiters, quotes, spaces and control chars",
			rows: []map[string]any{
				{"s": "pipe|here"}, {"s": "comma,here"}, {"s": "quote\"inside"},
				{"s": " lead"}, {"s": "trail "}, {"s": ""}, {"s": "tab\there"},
				{"s": "nl\nhere"}, {"s": "back\\slash"}, {"s": "mid dle"},
			},
		},
		{
			name: "strings that look like sentinels or literals",
			rows: []map[string]any{
				{"s": "~tilde"}, {"s": "-dash"}, {"s": "^caret"}, {"s": "=eq"},
				{"s": ".[1]:x"}, {"s": "123"}, {"s": "true"}, {"s": "false"},
				{"s": "null"}, {"s": "1.5"}, {"s": "-4.25"}, {"s": "1e3"}, {"s": "007"},
			},
		},
		{
			name: "int64 extremes round-trip exactly",
			rows: []map[string]any{
				{"v": int64(9007199254740993), "max": int64(math.MaxInt64), "min": int64(math.MinInt64)},
			},
		},
		{
			name: "floats keep their float identity",
			rows: []map[string]any{
				{"i": float64(2), "f": 0.1, "neg": -3.14, "big": 1e21, "small": 1.5e-7},
			},
		},
		{
			name: "array of objects becomes an attachment",
			rows: []map[string]any{
				{"orders": []any{map[string]any{"id": int64(1)}, map[string]any{"id": int64(2)}}, "n": int64(5)},
			},
		},
		{
			name: "prototype-unsafe keys become attachments",
			rows: []map[string]any{
				{"__proto__": map[string]any{"x": int64(1)}, "safe": int64(2)},
				{"cfg": map[string]any{"constructor": map[string]any{"y": int64(3)}, "ok": int64(4)}},
				{"prototype": "scalar stays a scalar"},
			},
		},
		{
			name: "empty objects become attachments",
			rows: []map[string]any{
				{"m": map[string]any{}, "n": int64(1)},
			},
		},
		{
			name: "deep nesting",
			rows: []map[string]any{
				{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": "deep"}}}},
			},
		},
		{
			name: "keys containing delimiters",
			rows: []map[string]any{
				{"a,b": int64(1), "c{d}e": "x", "f\"g": "y", "": "empty key"},
			},
		},
		{
			name: "scalar and object sharing one key across rows",
			rows: []map[string]any{
				{"a": map[string]any{"b": int64(1)}},
				{"a": int64(5)},
				{"a": nil},
			},
		},
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, tt := range roundTripCases() {
		t.Run(tt.name, func(t *testing.T) {
			assertRoundTrip(t, tt.name, tt.rows)
		})
	}
}

func TestEncodeWireShape(t *testing.T) {
	rows := []map[string]any{
		{"id": int64(1), "name": "ada", "score": nil},
		{"id": int64(2), "name": "bob", "tags": []any{"x", "y"}},
		{"id": int64(3), "meta": map[string]any{"k": "v"}},
	}
	// A single-key nested object still flattens to a path column; only
	// empty maps, arrays of objects and unsafe keys become attachments.
	want := "GCF profile=generic\n" +
		"## [3]{id,name,score,tags,meta>k}\n" +
		"1|ada|-|~|~\n" +
		"2|bob|~|.[2]:x,y|~\n" +
		"3|~|~|~|v"
	got, err := Encode(rows)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got != want {
		t.Errorf("Encode() =\n%s\nwant:\n%s", got, want)
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	// Go map ranges are randomized per iteration; if column discovery or
	// attachment marshaling ever ranges over a map directly, two encodes of
	// the same rows would differ and the no-regression gate would flake.
	rows := []map[string]any{
		{"zulu": int64(1), "alpha": map[string]any{"k2": int64(2), "k1": int64(3)}, "mike": "x"},
		{"bravo": []any{"p", "q"}, "zulu": int64(2), "kilo": nil},
	}
	first, err := Encode(rows)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := Encode(rows)
		if err != nil {
			t.Fatalf("Encode() iteration %d error = %v", i, err)
		}
		if again != first {
			t.Fatalf("Encode() is not deterministic:\n%s\n---\n%s", first, again)
		}
	}
}

func TestEncodeRejectsUnencodableValues(t *testing.T) {
	// Zero-panic policy: unencodable values must surface as errors, never
	// panic or silently corrupt the block.
	rows := []map[string]any{
		{"bad": func() {}},
	}
	if _, err := Encode(rows); err == nil {
		t.Fatal("Encode() with a func value: expected error, got nil")
	}
}
