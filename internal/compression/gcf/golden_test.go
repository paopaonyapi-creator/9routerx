// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package gcf

// golden_test.go pins the GCF wire format for a fixed 10-row corpus (FASE 8
// task 1).
//
// The GCF block is a wire format consumed by Decode and read by models, so a
// formatting change (column order, sentinel spelling, float rendering) is a
// protocol change. The golden records the exact block Encode produces for the
// corpus; a diff means the encoder's output changed shape.
//
// Regenerate deliberately after a reviewed wire-format change:
//
//	go test ./internal/compression/gcf/ -run TestEncodeGoldenBlock -update-golden
//
// and commit the new golden together with the codec change. The -update-golden
// flag is registered here (each package's golden test registers its own copy).
//
// The decode-back round trip is asserted alongside the golden on every run —
// including on -update-golden — so a regenerated golden can never record an
// encoder whose output no longer restores the original rows.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "encoding/json/v2"
)

var updateGolden = flag.Bool("update-golden", false,
	"rewrite the golden files in testdata/golden from the current output")

// goldenCorpus is a fixed 10-row corpus exercising every cell shape the
// generic profile defines: plain scalars, a nested object (flattened to
// meta>... columns), a primitive array (.[N]: cell), a null ( - sentinel),
// an absent key (~ sentinel), a float that needs the ".0" identity marker,
// and strings that would parse back as scalars (so they must travel quoted).
func goldenCorpus() []map[string]any {
	roles := []string{"user", "assistant", "tool"}
	rows := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		row := map[string]any{
			"id":     int64(1000 + i),
			"name":   fmt.Sprintf("node-%02d", i),
			"role":   roles[i%3],
			"active": i%2 == 0,
			"score":  float64(i) + 0.5,
			"meta": map[string]any{
				"source": "parity-suite",
				"line":   int64(10 + i),
			},
			"tags": []any{"alpha", fmt.Sprintf("t%d", i)},
			"note": "123", // must travel quoted: parses back as a number
		}
		switch i % 3 {
		case 0:
			row["extra"] = fmt.Sprintf("extra-%d", i) // present only here -> ~ elsewhere
		case 1:
			row["parent"] = nil // null -> - sentinel
		}
		rows = append(rows, row)
	}
	return rows
}

// canonicalJSON marshals deterministically so decoded rows can be compared
// against the corpus without depending on map iteration order or on which of
// int64/float64/json.Number each side happens to carry.
func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestEncodeGoldenBlock(t *testing.T) {
	rows := goldenCorpus()
	block, err := Encode(rows)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	path := filepath.Join("testdata", "golden", "gcf_ten_rows.golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata/golden: %v", err)
		}
		if err := os.WriteFile(path, []byte(block+"\n"), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read golden %s (first run? regenerate with -update-golden): %v", path, err)
		}
		want := strings.TrimRight(string(raw), "\n")
		if want != block {
			t.Errorf("GCF block drifted from golden:\n--- golden\n%s\n--- current\n%s\n"+
				"If the wire-format change is intentional, re-run with -update-golden and commit "+
				"the golden together with the codec change.", want, block)
		}
	}

	// Round trip against the golden text itself: whatever the encoder wrote
	// must restore the corpus rows value-for-value.
	decoded, err := Decode(block)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got, want := canonicalJSON(t, decoded), canonicalJSON(t, rows); got != want {
		t.Errorf("round trip lost information:\n--- corpus\n%s\n--- decoded\n%s", want, got)
	}
}
