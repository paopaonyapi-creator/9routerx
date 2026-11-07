// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package testingparity

import (
	"strings"
	"testing"

	"9routerx/proxy/internal/tokensaver/antislop"
)

// parity_antislop_test.go pins the Anti-slop prompt to the serenhope reference
// literals in open-sse/rtk/antislopPrompt.js (FASE 8 task 2). The prompt is an
// asset injected on every request; this is the contract that the port ships
// the same bytes upstream ships.
//
// The LITE constant below is the exact LITE string extracted from
// open-sse/rtk/antislopPrompt.js (verified 913 UTF-16 units / 913 bytes, no
// CR, no backticks). FULL and ULTRA are 42k/130k characters, far too large to
// embed whole; the upstream generator defines both as one runtime-note header
// followed by the core text cut at "## What This Is" (dropping the upstream
// "First-Run Install Wizard" and "Two Usage Modes" sections), so those
// properties are what the parity rows assert.

const serenhopeAntislopLITE = `You use antislop: rules for AI coding agents. (from the antislop rules, https://github.com/miqdadbadjuber/anti-slop)
It is a FILTER, not a style guide. It stops generic AI slop in generated UI, copy and code. It never invents direction: if the project has no DESIGN.md or stated direction, say so plainly instead of inventing taste.

Before any visual or copy technique, answer: what does this serve? If the only answer is "it looks AI" or "it looks safe", cut it or rework it. If the answer names a hierarchy, identity or readability goal, keep it and write the reason down.

Scan your output for: blue-purple gradient washes, blur everywhere, every element pill-shaped, oversized soft shadows, generic hero + 3 equal cards + centered CTA, "Welcome to X" / "Elevate" / "Unlock the power of" filler, emoji as icons, nav that goes nowhere, missing empty/error states. One of these is fine when it serves a purpose.`

// runtimeNote is the verbatim header the upstream generator writes in front of
// the core text; level is the "(full)" / "(ultra)" intensity word.
func runtimeNote(level string) string {
	return "> Runtime note: this text is injected by 9Router's Anti Slop plugin. Ignore the skill's own\n" +
		"> install wizard and mode resolution - there is nothing to install, and the intensity is\n" +
		"> already chosen (" + level + "). Do not ask which mode to use; apply the rules and continue."
}

// coreStartMarker is where the upstream generator cuts the core skill text,
// and what must therefore open the core section in full/ultra.
const coreStartMarker = "## What This Is (and What It Isn't)"

// cutSections are the upstream-only sections the generator drops; their
// presence would mean the cut regressed.
var cutSections = []string{"## First-Run Install Wizard", "## Two Usage Modes"}

// TestSerenehopeAntislopLiteCharacterIdentical asserts the Go lite prompt is
// character-identical to the serenhope LITE literal.
func TestSerenehopeAntislopLiteCharacterIdentical(t *testing.T) {
	got := antislop.GetPrompt("lite")
	if got != serenhopeAntislopLITE {
		if len(got) != len(serenhopeAntislopLITE) {
			t.Fatalf("serenhope-parity: lite prompt length = %d, want %d", len(got), len(serenhopeAntislopLITE))
		}
		for i := 0; i < len(got) && i < len(serenhopeAntislopLITE); i++ {
			if got[i] != serenhopeAntislopLITE[i] {
				t.Fatalf("serenhope-parity: lite prompt first differs at byte %d: got %q want %q",
					i, got[i:i+min(24, len(got)-i)], serenhopeAntislopLITE[i:i+min(24, len(serenhopeAntislopLITE)-i)])
			}
		}
		t.Fatal("serenhope-parity: lite prompt differs from the upstream LITE literal")
	}
}

// TestSerenehopeAntislopFullCoreCut asserts the full prompt carries the
// upstream runtime note for "full" followed by the core text cut at
// "## What This Is", with the upstream install/mode sections absent.
func TestSerenehopeAntislopFullCoreCut(t *testing.T) {
	got := antislop.GetPrompt("full")
	// The upstream generator writes "# antislop", one blank line, the runtime
	// note, one blank line, then the core.
	wantPrefix := "# antislop\n\n" + runtimeNote("full") + "\n\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("serenhope-parity: full prompt does not open with the upstream \"full\" runtime note; got prefix %q", firstN(got, len(wantPrefix)))
	}
	if !strings.Contains(got, coreStartMarker) {
		t.Errorf("serenhope-parity: full prompt missing the core cut marker %q", coreStartMarker)
	}
	for _, section := range cutSections {
		if strings.Contains(got, section) {
			t.Errorf("serenhope-parity: full prompt contains upstream-only section %q (the cut regressed)", section)
		}
	}
}

// TestSerenehopeAntislopUltraCoreCut asserts the same core cut for ultra plus
// the five concern skills the upstream generator appends.
func TestSerenehopeAntislopUltraCoreCut(t *testing.T) {
	got := antislop.GetPrompt("ultra")
	// The upstream generator writes "# antislop", TWO blank lines, the runtime
	// note, two blank lines, then the core (verified against the JS ULTRA
	// literal's opening: "# antislop\n\n\n\n> Runtime note...").
	wantPrefix := "# antislop\n\n\n\n" + runtimeNote("ultra") + "\n\n\n\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("serenhope-parity: ultra prompt does not open with the upstream \"ultra\" runtime note; got prefix %q", firstN(got, len(wantPrefix)))
	}
	if !strings.Contains(got, coreStartMarker) {
		t.Errorf("serenhope-parity: ultra prompt missing the core cut marker %q", coreStartMarker)
	}
	for _, section := range cutSections {
		if strings.Contains(got, section) {
			t.Errorf("serenhope-parity: ultra prompt contains upstream-only section %q (the cut regressed)", section)
		}
	}
	for _, skill := range []string{
		"# antislop-ui", "# antislop-copywriting", "# antislop-human",
		"# antislop-layoutmobile", "# antislop-code",
	} {
		if !strings.Contains(got, skill) {
			t.Errorf("serenhope-parity: ultra prompt missing concern skill %q", skill)
		}
	}
}

func firstN(s string, n int) string {
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}
