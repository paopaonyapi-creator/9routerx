// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package antislop

// golden_test.go pins the Anti-slop prompt assets (FASE 8 task 1).
//
// The prompts are ASSETS, not code: they are injected verbatim into every
// request at the chosen level, so an accidental edit to core.md, a concern
// skill, or the hand-written lite condensation silently changes what every
// user's model sees. A character drift of one byte changes these hashes.
//
// Each golden records the SHA-256 of the level's prompt under
// testdata/golden/. Regenerate deliberately after a reviewed prompt change:
//
//	go test ./internal/tokensaver/antislop/ -run TestAntislopPromptGolden -update-golden
//
// and commit the new hashes together with the prompt change. The -update-golden
// flag is registered here (each package's golden test registers its own copy,
// so the flag never leaks between test binaries).
//
// Parity anchor: the serenhope reference (open-sse/rtk/antislopPrompt.js)
// pins the same asset by char count (ANTISLOP_LEVEL_CHARS); the contract test
// in internal/testingparity pins the lite prompt byte-for-byte against the
// upstream literal, so this golden only has to catch local drift.

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false,
	"rewrite the golden files in testdata/golden from the current output")

func TestAntislopPromptGolden(t *testing.T) {
	for _, level := range []string{LevelLite, LevelFull, LevelUltra} {
		t.Run(level, func(t *testing.T) {
			prompt := GetPrompt(level)
			if prompt == "" {
				t.Fatalf("GetPrompt(%q) returned an empty prompt", level)
			}
			sum := sha256.Sum256([]byte(prompt))
			got := hex.EncodeToString(sum[:])

			path := filepath.Join("testdata", "golden", "antislop_"+level+".sha256")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir testdata/golden: %v", err)
				}
				if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s (first run? regenerate with -update-golden): %v", path, err)
			}
			want := strings.TrimSpace(string(raw))
			if want != got {
				t.Errorf("prompt hash drifted for level %q:\n  golden: %s\n  current: %s\n"+
					"If the change is intentional, re-run with -update-golden and commit the hash "+
					"together with the prompt change; otherwise revert the asset.", level, want, got)
			}
		})
	}
}
