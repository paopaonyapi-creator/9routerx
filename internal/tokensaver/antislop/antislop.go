// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package antislop

import (
	"embed"
	"fmt"
	"strings"
	"sync"

	"9routerx/proxy/internal/tokensaver"
)

//go:embed src/*.md
var srcFS embed.FS

// Antislop level constants.
const (
	LevelOff   = "off"
	LevelLite  = "lite"
	LevelFull  = "full"
	LevelUltra = "ultra"
)

// Lite prompt is the hand-written condensation, 913 characters, for cheap always-on filtering.
const promptLite = `You use antislop: rules for AI coding agents. (from the antislop rules, https://github.com/miqdadbadjuber/anti-slop)
It is a FILTER, not a style guide. It stops generic AI slop in generated UI, copy and code. It never invents direction: if the project has no DESIGN.md or stated direction, say so plainly instead of inventing taste.

Before any visual or copy technique, answer: what does this serve? If the only answer is "it looks AI" or "it looks safe", cut it or rework it. If the answer names a hierarchy, identity or readability goal, keep it and write the reason down.

Scan your output for: blue-purple gradient washes, blur everywhere, every element pill-shaped, oversized soft shadows, generic hero + 3 equal cards + centered CTA, "Welcome to X" / "Elevate" / "Unlock the power of" filler, emoji as icons, nav that goes nowhere, missing empty/error states. One of these is fine when it serves a purpose.`

var (
	buildOnce   sync.Once
	promptFull  string
	promptUltra string
)

func modeNote(level string) string {
	return fmt.Sprintf(`> Runtime note: this text is injected by 9Router's Anti Slop plugin. Ignore the skill's own
> install wizard and mode resolution - there is nothing to install, and the intensity is
> already chosen (%s). Do not ask which mode to use; apply the rules and continue.`, level)
}

func stripFrontmatter(md string) string {
	// Normalize Windows line endings if any
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(md, "---") {
		return strings.TrimSpace(md)
	}
	end := strings.Index(md[3:], "\n---")
	if end < 0 {
		return strings.TrimSpace(md)
	}
	content := md[3+end+4:]
	return strings.TrimSpace(strings.TrimLeft(content, " \t\n"))
}

func subSkill(filename, title string) string {
	data, err := srcFS.ReadFile("src/" + filename)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("# %s\n\n%s", title, stripFrontmatter(string(data)))
}

func initPrompts() {
	buildOnce.Do(func() {
		coreData, err := srcFS.ReadFile("src/core.md")
		if err != nil {
			return
		}
		coreStr := strings.ReplaceAll(string(coreData), "\r\n", "\n")
		const coreStart = "## What This Is"
		at := strings.Index(coreStr, coreStart)
		if at < 0 {
			at = 0
		}
		core := strings.TrimSpace(coreStr[at:])

		promptFull = fmt.Sprintf("# antislop\n\n%s\n\n%s", modeNote("full"), core)

		ultraParts := []string{
			"# antislop",
			"",
			modeNote("ultra"),
			"",
			core,
			"",
			"---",
			"",
			"The five concern skills below are part of the same system. When the task touches one of\nthem, follow that skill's section as strictly as the core.",
			"",
			subSkill("ui.md", "antislop-ui"),
			"",
			subSkill("copywriting.md", "antislop-copywriting"),
			"",
			subSkill("human.md", "antislop-human"),
			"",
			subSkill("layoutmobile.md", "antislop-layoutmobile"),
			"",
			subSkill("code.md", "antislop-code"),
		}
		promptUltra = strings.Join(ultraParts, "\n\n")
	})
}

// GetPrompt returns the generated Anti-slop prompt for the given level.
// Missing or unrecognized level falls back to "full". "off" returns empty string.
func GetPrompt(level string) string {
	initPrompts()
	switch strings.ToLower(strings.TrimSpace(level)) {
	case LevelOff:
		return ""
	case LevelLite:
		return promptLite
	case LevelUltra:
		return promptUltra
	case LevelFull:
		return promptFull
	default:
		// Fallback to full per reference open-sse/rtk/antislop.js
		return promptFull
	}
}

// ApplyAntiSlop injects the antislop prompt into the request body wire-format.
func ApplyAntiSlop(body []byte, claudeNative bool, level string) ([]byte, bool) {
	prompt := GetPrompt(level)
	if prompt == "" {
		return body, false
	}
	return tokensaver.InjectKeySystemPrompt(body, claudeNative, prompt)
}
