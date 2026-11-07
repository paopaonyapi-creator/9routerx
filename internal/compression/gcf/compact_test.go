// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package gcf

import (
	"fmt"
	"strings"
	"testing"

	json "encoding/json/v2"
)

// tabularJSON builds a compact JSON array text from n rows of a homogeneous
// 4-column record, the shape CompactTabular targets. bigID swaps every id for
// an int64 literal beyond float64's exact range.
func tabularJSON(t *testing.T, n int, bigID bool) string {
	t.Helper()
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		id := any(int64(i))
		if bigID {
			id = int64(9007199254740993)
		}
		rows = append(rows, map[string]any{
			"id":     id,
			"name":   fmt.Sprintf("user-%04d", i),
			"role":   "admin",
			"active": true,
		})
	}
	b, err := json.Marshal(rows, json.Deterministic(true))
	if err != nil {
		t.Fatalf("marshal test rows: %v", err)
	}
	return string(b)
}

// extractGCFBlock pulls the encoded block back out of a gcf-generic fence so
// tests can Decode it.
func extractGCFBlock(t *testing.T, text string) string {
	t.Helper()
	_, rest, found := strings.Cut(text, fenceOpen+"\n")
	if !found {
		t.Fatalf("no %s fence in text:\n%s", fenceOpen, text)
	}
	block, _, found := strings.Cut(rest, "\n"+fenceClose)
	if !found {
		t.Fatalf("unterminated %s fence in text:\n%s", fenceOpen, text)
	}
	return block
}

func TestCompactTabularSavingsOn50HomogeneousRows(t *testing.T) {
	jsonText := tabularJSON(t, 50, false)
	out, changed := CompactTabular(jsonText, DefaultMinRows)
	if !changed {
		t.Fatal("CompactTabular() changed = false, want true for a 50-row array")
	}
	if !strings.HasPrefix(out, fenceOpen) {
		t.Errorf("CompactTabular() output does not start with a gcf-generic fence:\n%.80s", out)
	}
	// The task's savings gate: a 50-row homogeneous 4-column array must come
	// out at least 30% smaller than the original JSON.
	if len(out) >= len(jsonText)*7/10 {
		t.Errorf("CompactTabular() = %d bytes, want < 70%% of original %d bytes\n%s", len(out), len(jsonText), out)
	}
	// Losslessness end to end: what the fence carries must decode back to
	// the rows the JSON described.
	block := extractGCFBlock(t, out)
	rows, err := Decode(block)
	if err != nil {
		t.Fatalf("Decode(compacted) error = %v\nblock:\n%s", err, block)
	}
	if len(rows) != 50 {
		t.Fatalf("Decode(compacted) returned %d rows, want 50", len(rows))
	}
	if rows[7]["name"] != "user-0007" || rows[7]["role"] != "admin" || rows[7]["active"] != true {
		t.Errorf("Decode(compacted) row 7 = %#v, want the original record", rows[7])
	}
	if rows[7]["id"] != int64(7) {
		t.Errorf("Decode(compacted) row 7 id = %#v (%T), want int64(7)", rows[7]["id"], rows[7]["id"])
	}
}

func TestCompactTabularPreservesInt64Literals(t *testing.T) {
	// 9007199254740993 is above float64's exact range; a float64-based
	// parse would silently emit ...992 before Encode ever saw the value.
	jsonText := tabularJSON(t, 8, true)
	out, changed := CompactTabular(jsonText, DefaultMinRows)
	if !changed {
		t.Fatal("CompactTabular() changed = false, want true")
	}
	if !strings.Contains(out, "9007199254740993") {
		t.Errorf("CompactTabular() lost the big int64 literal:\n%s", out)
	}
	if strings.Contains(out, "9007199254740992") {
		t.Errorf("CompactTabular() rounded the big int64 through float64:\n%s", out)
	}
}

func TestCompactTabularNoRegressionGate(t *testing.T) {
	tiny := tabularJSON(t, 2, false)
	out, changed := CompactTabular(tiny, DefaultMinRows)
	if changed || out != tiny {
		t.Errorf("CompactTabular(2 rows) changed = %v, want unchanged text", changed)
	}

	// Eight rows whose values are cheaper in JSON than in GCF: the fence,
	// profile line and header cost more than the rows save, so the gate
	// must keep the original.
	dense := `[{"a":[]},{"a":[]},{"a":[]},{"a":[]},{"a":[]},{"a":[]},{"a":[]},{"a":[]}]`
	out, changed = CompactTabular(dense, DefaultMinRows)
	if changed || out != dense {
		t.Errorf("CompactTabular(dense small rows) changed = %v, want the original kept\nout:\n%s", changed, out)
	}
}

func TestCompactTabularFencedJSONBlockInProse(t *testing.T) {
	jsonText := tabularJSON(t, 10, false)
	text := "Here are the results:\n\n```json\n" + jsonText + "\n```\n\nSummary above."
	out, changed := CompactTabular(text, DefaultMinRows)
	if !changed {
		t.Fatal("CompactTabular() changed = false, want the fenced block replaced")
	}
	if !strings.Contains(out, "Here are the results:") || !strings.Contains(out, "Summary above.") {
		t.Errorf("CompactTabular() lost surrounding prose:\n%s", out)
	}
	if !strings.Contains(out, fenceOpen) {
		t.Errorf("CompactTabular() output has no gcf-generic fence:\n%s", out)
	}
	if strings.Contains(out, jsonFence) {
		t.Errorf("CompactTabular() left the original json fence in place:\n%s", out)
	}
	// fenceOpen starts with the same backticks as fenceClose, so count the
	// distinctive info string instead.
	if got := strings.Count(out, fenceOpen); got != 1 {
		t.Errorf("CompactTabular() produced %d gcf fences, want 1:\n%s", got, out)
	}
	block := extractGCFBlock(t, out)
	rows, err := Decode(block)
	if err != nil {
		t.Fatalf("Decode(replaced fence) error = %v", err)
	}
	if len(rows) != 10 {
		t.Errorf("Decode(replaced fence) returned %d rows, want 10", len(rows))
	}
}

func TestCompactTabularReplacesEveryJSONBlock(t *testing.T) {
	first := tabularJSON(t, 9, false)
	second := tabularJSON(t, 9, false)
	text := "```json\n" + first + "\n```\nmiddle\n```json\n" + second + "\n```"
	out, changed := CompactTabular(text, DefaultMinRows)
	if !changed {
		t.Fatal("CompactTabular() changed = false, want both blocks replaced")
	}
	if got := strings.Count(out, fenceOpen); got != 2 {
		t.Errorf("CompactTabular() produced %d gcf fences, want 2:\n%s", got, out)
	}
	if !strings.Contains(out, "middle") {
		t.Errorf("CompactTabular() lost the text between blocks:\n%s", out)
	}
}

func TestCompactTabularSkipsUnsuitablePayloads(t *testing.T) {
	bigArray := "[" + strings.TrimSuffix(strings.Repeat(`{"a":1},`, 10), ",") + "]"
	tests := []struct {
		name string
		text string
	}{
		{"array of non-objects", "[" + strings.TrimSuffix(strings.Repeat("1,", 10), ",") + "]"},
		{"array mixing objects and scalars", `[{"a":1},2,{"a":3},{"a":4},{"a":5},{"a":6},{"a":7},{"a":8},{"a":9},{"a":10}]`},
		{"plain prose", "there is no data here, just a sentence with, commas"},
		{"invalid json", "results: [1,2,3"},
		{"unterminated json fence", "```json\n" + bigArray + "\nprose continues"},
		{"json object instead of array", `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, changed := CompactTabular(tt.text, DefaultMinRows)
			if changed || out != tt.text {
				t.Errorf("CompactTabular(%q) changed = %v, want text unchanged", tt.text, changed)
			}
		})
	}
}

func TestCompactTabularDefaultMinRows(t *testing.T) {
	if DefaultMinRows != 8 {
		t.Errorf("DefaultMinRows = %d, want 8 (OmniRoute default)", DefaultMinRows)
	}
	// minRows <= 0 must fall back to the default: seven rows stay untouched,
	// eight rows fold.
	seven := tabularJSON(t, 7, false)
	if out, changed := CompactTabular(seven, 0); changed || out != seven {
		t.Errorf("CompactTabular(7 rows, minRows 0) changed = %v, want unchanged", changed)
	}
	eight := tabularJSON(t, 8, false)
	if _, changed := CompactTabular(eight, 0); !changed {
		t.Error("CompactTabular(8 rows, minRows 0) changed = false, want the array folded")
	}
}

func TestCompactTabularWholeTextBecomesSingleFencedBlock(t *testing.T) {
	jsonText := tabularJSON(t, 12, false)
	out, changed := CompactTabular(jsonText, DefaultMinRows)
	if !changed {
		t.Fatal("CompactTabular() changed = false, want true")
	}
	if !strings.HasPrefix(out, fenceOpen+"\nGCF profile=generic\n") {
		t.Errorf("CompactTabular() output does not open with the fence and profile line:\n%.80s", out)
	}
	if !strings.HasSuffix(out, "\n"+fenceClose) {
		t.Errorf("CompactTabular() output does not close the fence:\n%s", out)
	}
}

// A text that carries both a bare JSON array and an already-folded gcf block
// must reach a fixed point after one pass: the gcf fence is not a ```json
// fence, so the second pass must find nothing to fold, and re-running
// CompactTabular on its own output must return those bytes unchanged.
func TestCompactTabularIdempotentOnMixedText(t *testing.T) {
	fenced := tabularJSON(t, 12, false)
	bare := tabularJSON(t, 10, false)
	text := "Preface.\n\n```json\n" + fenced + "\n```\n\nMiddle.\n\n" + bare + "\n\nTrailer."

	once, changed := CompactTabular(text, DefaultMinRows)
	if !changed {
		t.Fatal("CompactTabular() changed = false, want the fenced block folded")
	}
	if got := strings.Count(once, fenceOpen); got != 1 {
		t.Fatalf("after one pass the text has %d gcf fences, want 1:\n%s", got, once)
	}

	twice, changed2 := CompactTabular(once, DefaultMinRows)
	if changed2 {
		t.Fatalf("CompactTabular applied twice changed the text again:\n%s", twice)
	}
	if twice != once {
		t.Fatalf("second pass returned different bytes:\nonce:\n%s\ntwice:\n%s", once, twice)
	}

	// The same holds when the bare array sits INSIDE a string value of the
	// folded rows: the encoder quotes newlines away, so no raw json-fence
	// line can appear inside a block body to trip the fenced-block scanner.
	block, err := Encode([]map[string]any{
		{"note": "```json\n" + fenced + "\n```", "id": int64(1)},
		{"note": "b", "id": int64(2)},
	})
	if err != nil {
		t.Fatalf("Encode tricky rows: %v", err)
	}
	// The substring may appear inside a quoted cell; what must never happen
	// is a LINE that reads as a json fence open, which is what the scanner
	// keys on (TrimSpace(line) == "```json").
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == jsonFence {
			t.Fatalf("encoder leaked a raw json fence line into the block:\n%s", block)
		}
	}
	onceMore, changed := CompactTabular(fenceOpen+"\n"+block+"\n"+fenceClose, DefaultMinRows)
	if changed {
		t.Fatalf("CompactTabular refolded a block whose string value embeds a fenced array:\n%s", onceMore)
	}

	// Decode still restores the embedded-fence rows byte-for-byte in value.
	rows, err := Decode(block)
	if err != nil {
		t.Fatalf("Decode(embedded fence block): %v", err)
	}
	if len(rows) != 2 || rows[0]["id"] != int64(1) {
		t.Fatalf("Decode(embedded fence block) rows = %+v", rows)
	}
	if !strings.Contains(rows[0]["note"].(string), "```json") {
		t.Fatalf("embedded fence text lost: %+v", rows[0]["note"])
	}
}
