// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package tokensaver

import (
	"strings"
	"testing"
)

func TestGetTerseProsePrompt(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		expected string
	}{
		{
			name:     "lite level",
			level:    "lite",
			expected: TerseProseLite,
		},
		{
			name:     "full level",
			level:    "full",
			expected: TerseProseFull,
		},
		{
			name:     "ultra level",
			level:    "ultra",
			expected: TerseProseUltra,
		},
		{
			name:     "empty level defaults to full",
			level:    "",
			expected: TerseProseFull,
		},
		{
			name:     "unknown level defaults to full",
			level:    "shouty",
			expected: TerseProseFull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetTerseProsePrompt(tt.level)
			if got != tt.expected {
				t.Fatalf("GetTerseProsePrompt(%q) = %q; want %q", tt.level, got, tt.expected)
			}
		})
	}
}

func TestGetLessCodePrompt(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		expected string
	}{
		{
			name:     "lite level",
			level:    "lite",
			expected: LessCodeLite,
		},
		{
			name:     "full level",
			level:    "full",
			expected: LessCodeFull,
		},
		{
			name:     "ultra level",
			level:    "ultra",
			expected: LessCodeUltra,
		},
		{
			name:     "empty level defaults to full",
			level:    "",
			expected: LessCodeFull,
		},
		{
			name:     "unknown level defaults to full",
			level:    "yagni-max",
			expected: LessCodeFull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLessCodePrompt(tt.level)
			if got != tt.expected {
				t.Fatalf("GetLessCodePrompt(%q) = %q; want %q", tt.level, got, tt.expected)
			}
		})
	}
}

func TestPromptLevelSetsAreDistinctAndNonEmpty(t *testing.T) {
	tests := []struct {
		name    string
		prompts [3]string
	}{
		{name: "terse prose levels", prompts: [3]string{TerseProseLite, TerseProseFull, TerseProseUltra}},
		{name: "less code levels", prompts: [3]string{LessCodeLite, LessCodeFull, LessCodeUltra}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := make(map[string]bool, len(tt.prompts))
			for i, p := range tt.prompts {
				if p == "" {
					t.Errorf("level %d is empty", i)
				}
				if seen[p] {
					t.Errorf("level %d duplicates another level's text", i)
				}
				seen[p] = true
			}
		})
	}
}

func TestLessCodeSafetyBoundaries(t *testing.T) {
	// SAFETY_BOUNDARIES travels verbatim on the levels that actively rewrite
	// code (full, ultra); lite only names the lazier option and never
	// rewrites, so it must not carry the appended floor.
	if !strings.Contains(LessCodeFull, SAFETY_BOUNDARIES) {
		t.Error("LessCodeFull missing SAFETY_BOUNDARIES")
	}
	if !strings.Contains(LessCodeUltra, SAFETY_BOUNDARIES) {
		t.Error("LessCodeUltra missing SAFETY_BOUNDARIES")
	}
	if strings.Contains(LessCodeLite, SAFETY_BOUNDARIES) {
		t.Error("LessCodeLite should not carry SAFETY_BOUNDARIES")
	}
	if !strings.Contains(LessCodeFull, "YAGNI") || !strings.Contains(LessCodeUltra, "YAGNI") {
		t.Error("less-code full/ultra missing the YAGNI ladder")
	}
}

func TestTerseProseContent(t *testing.T) {
	// Lite is the one-line rule.
	if !strings.Contains(TerseProseLite, "Drop filler, articles and hedging; keep technical substance exact") {
		t.Error("TerseProseLite missing the core terse-prose rule")
	}
	// Full covers the four doctrines the port requires.
	for _, want := range []string{"filler words", "hedging", "preamble", "summary"} {
		if !strings.Contains(TerseProseFull, want) {
			t.Errorf("TerseProseFull missing %q coverage", want)
		}
	}
	// Ultra adds the aggressive constraints.
	for _, want := range []string{"No restatement of the question", "No transition sentences", "Bullets over prose", "Max one sentence of context"} {
		if !strings.Contains(TerseProseUltra, want) {
			t.Errorf("TerseProseUltra missing %q constraint", want)
		}
	}
}
