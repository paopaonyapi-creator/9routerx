// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package translator

// golden_mask_test.go pins the PII masking output (FASE 8 task 1).
//
// Masking runs through the guardrails engine (internal/guardrails) with the
// minimal mask-action policy: the "pii" detector set with ActionMask. The
// engine is stateless, so the same text always masks to the same output —
// which is exactly what the golden pins. A diff means the detector patterns,
// the [REDACTED] mask, or the span-merge logic changed what users' logs and
// upstream providers actually receive.
//
// Regenerate deliberately after a reviewed detector change:
//
//	go test ./internal/translator/ -run TestPIIMaskGolden -update-golden
//
// and commit the new goldens together with the detector change. The
// -update-golden flag is registered here (each package's golden test registers
// its own copy, so the flag never leaks between test binaries).

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"9routerx/proxy/internal/guardrails"
)

var updateGolden = flag.Bool("update-golden", false,
	"rewrite the golden files in testdata/golden from the current output")

// maskEngine is the minimal policy that turns PII detection on with mask
// semantics — the configuration the guardrails middleware resolves to when an
// operator selects "mask" for the pii detector set.
func maskEngine() *guardrails.Engine {
	return guardrails.NewEngine([]string{"pii"}, guardrails.ActionMask)
}

// Mask cases: email and credit card (Luhn-valid, space-grouped) are the two
// entities the task names; the mixed case also carries clean text, a private
// IP (loopback must NOT be flagged) and a 16-digit git-SHA-looking run (must
// NOT be flagged — it fails Luhn), so the golden also records what masking
// deliberately leaves alone.
func TestPIIMaskGolden(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "email",
			in:   "Contact ada@example.com for the handoff notes.",
		},
		{
			name: "credit_card",
			in:   "Card on file: 4111 1111 1111 1111, expires 12/29.",
		},
		{
			name: "mixed_email_and_card",
			in: "Customer jane.doe@company.io paid with 5500 0000 0000 0004.\n" +
				"Build revision a1b2c3d4e5f6a7b8 (not PII, fails Luhn) shipped from 127.0.0.1 (loopback, not routed).\n" +
				"Reply to support@example.org when done.",
		},
	}

	eng := maskEngine()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := eng.Scan(tc.in)
			if d == nil {
				t.Fatal("Scan returned nil decision")
			}
			if d.Action != guardrails.ActionMask {
				t.Fatalf("action = %q, want mask", d.Action)
			}
			if !d.WasMutated() || d.Mutated == "" {
				t.Fatalf("mask decision carried no mutated text; findings = %+v", d.Findings)
			}
			if d.Mutated == tc.in {
				t.Fatal("masked output equals input; nothing was redacted")
			}

			path := filepath.Join("testdata", "golden", "mask_"+tc.name+".golden")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir testdata/golden: %v", err)
				}
				// The golden records input and output so a reviewer can read
				// exactly what changed without digging through the test.
				content := "INPUT:\n" + tc.in + "\nMASKED:\n" + d.Mutated + "\n"
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s (first run? regenerate with -update-golden): %v", path, err)
			}
			wantMasked := goldenField(string(raw), "MASKED")
			if wantMasked != d.Mutated {
				t.Errorf("masked output drifted from golden:\n--- golden\n%s\n--- current\n%s\n"+
					"If the detector change is intentional, re-run with -update-golden and commit "+
					"the golden together with the detector change.", wantMasked, d.Mutated)
			}
		})
	}

	// Clean text is never touched, even under a mask policy.
	t.Run("clean_text_untouched", func(t *testing.T) {
		clean := "Refactor the router loop and rerun the benchmark suite."
		d := eng.Scan(clean)
		if d.Action != guardrails.ActionAllow || d.WasMutated() {
			t.Fatalf("clean text flagged: action=%q mutated=%q findings=%+v", d.Action, d.Mutated, d.Findings)
		}
	})
}

// goldenField pulls the named section out of a golden file laid out as
// "INPUT:\n...\nMASKED:\n...". Everything after the marker is the value.
func goldenField(content, marker string) string {
	_, rest, _ := strings.Cut(content, marker+":\n")
	return strings.TrimRight(rest, "\n")
}
