// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package gcf

import (
	"strings"
	"testing"

	jsonv1 "encoding/json"
)

func TestDecodeSurplusRowsError(t *testing.T) {
	rows := []map[string]any{
		{"id": int64(1), "name": "ada"},
		{"id": int64(2), "name": "bob"},
	}
	block, err := Encode(rows)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	// A third row appended after a header declaring two means the block was
	// corrupted or truncated in transit; silently accepting it would hand
	// the model rows that were never in the payload.
	corrupted := block + "\n3|carol"
	_, err = Decode(corrupted)
	if err == nil {
		t.Fatal("Decode() with surplus rows: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "surplus") {
		t.Errorf("Decode() surplus error = %q, want it to mention surplus", err.Error())
	}
}

func TestDecodeMissingRowsError(t *testing.T) {
	rows := []map[string]any{
		{"id": int64(1), "name": "ada"},
		{"id": int64(2), "name": "bob"},
	}
	block, err := Encode(rows)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	lines := strings.Split(block, "\n")
	truncated := strings.Join(lines[:len(lines)-1], "\n")
	if _, err := Decode(truncated); err == nil {
		t.Fatal("Decode() with a missing row: expected error, got nil")
	}
}

func TestDecodeRejectsMalformedBlocks(t *testing.T) {
	tests := []struct {
		name  string
		block string
	}{
		{
			name:  "empty block",
			block: "",
		},
		{
			name:  "unknown profile",
			block: "GCF profile=compact\n## [0]{}\n",
		},
		{
			name:  "missing header",
			block: "GCF profile=generic\n",
		},
		{
			name:  "header without row count",
			block: "GCF profile=generic\n## N{a}\n1\n",
		},
		{
			name:  "negative row count",
			block: "GCF profile=generic\n## [-1]{}\n",
		},
		{
			name:  "non-numeric row count",
			block: "GCF profile=generic\n## [two]{a}\n1\n",
		},
		{
			name:  "cell count mismatch",
			block: "GCF profile=generic\n## [1]{a,b}\nx\n",
		},
		{
			name:  "scalar blocks a nested path",
			block: "GCF profile=generic\n## [1]{a,a>b}\n5|1\n",
		},
		{
			name:  "array declares more elements than present",
			block: "GCF profile=generic\n## [1]{t}\n.[3]:a,b\n",
		},
		{
			name:  "array declares fewer elements than present",
			block: "GCF profile=generic\n## [1]{t}\n.[1]:a,b\n",
		},
		{
			name:  "array cell without separator",
			block: "GCF profile=generic\n## [1]{t}\n.[3nonsense\n",
		},
		{
			name:  "empty attachment",
			block: "GCF profile=generic\n## [1]{o}\n^\n",
		},
		{
			name:  "attachment with invalid json",
			block: "GCF profile=generic\n## [1]{o}\n^{not json\n",
		},
		{
			name:  "attachment with trailing data",
			block: "GCF profile=generic\n## [1]{o}\n^{} junk\n",
		},
		{
			name:  "unterminated quoted cell",
			block: "GCF profile=generic\n## [1]{s}\n\"unterminated\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := Decode(tt.block)
			if err == nil {
				t.Fatalf("Decode(%q) = %v, want error", tt.block, rows)
			}
		})
	}
}

func TestDecodeToleratesFenceLayoutWhitespace(t *testing.T) {
	// Blocks travel inside fenced code fences, so leading/trailing blank
	// lines and CR line endings must not fail the parse.
	rows := []map[string]any{
		{"id": int64(1), "name": "ada"},
		{"id": int64(2), "name": "bob"},
	}
	block, err := Encode(rows)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	withLayout := "\r\n" + strings.ReplaceAll(block, "\n", "\r\n") + "\r\n\r\n"
	got, err := Decode(withLayout)
	if err != nil {
		t.Fatalf("Decode() with fence layout error = %v", err)
	}
	if len(got) != 2 || got[1]["name"] != "bob" {
		t.Errorf("Decode() = %v, want the original two rows", got)
	}
}

func TestDecodeQuotedHeaderNames(t *testing.T) {
	// A column name containing a delimiter travels quoted in the header; the
	// decoder must unquote it before path resolution.
	rows, err := Decode("GCF profile=generic\n## [1]{\"a,b\",c}\n5|x\n")
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Decode() returned %d rows, want 1", len(rows))
	}
	v, ok := rows[0]["a,b"]
	if !ok || v != int64(5) {
		t.Errorf("Decode() row = %#v, want key \"a,b\" = int64(5)", rows[0])
	}
	if rows[0]["c"] != "x" {
		t.Errorf("Decode() row[\"c\"] = %#v, want \"x\"", rows[0]["c"])
	}
}

func TestDecodeRestoresAttachmentValues(t *testing.T) {
	// Attachments come back as the original object shape; numbers inside
	// them keep exact literals because the attachment parser preserves raw
	// number text (big int64s stay exact).
	rows, err := Decode("GCF profile=generic\n## [1]{orders,n}\n^[{\"id\":9007199254740993}]|5\n")
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	orders, ok := rows[0]["orders"].([]any)
	if !ok || len(orders) != 1 {
		t.Fatalf("Decode() orders = %#v, want one element", rows[0]["orders"])
	}
	first, ok := orders[0].(map[string]any)
	if !ok {
		t.Fatalf("Decode() orders[0] = %#v, want an object", orders[0])
	}
	id, ok := first["id"].(jsonv1.Number)
	if !ok || id.String() != "9007199254740993" {
		t.Errorf("Decode() orders[0][\"id\"] = %#v, want the exact literal 9007199254740993", first["id"])
	}
	if rows[0]["n"] != int64(5) {
		t.Errorf("Decode() n = %#v, want int64(5)", rows[0]["n"])
	}
}
