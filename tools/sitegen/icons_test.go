// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// emojiPattern matches every Unicode emoji block the owner's no-emoji rule
// covers. Box-drawing (U+2500-257F) and text arrows (U+2190-21FF) are
// deliberately outside it: the README's "How It Works" diagram and its
// Subscription → Cheap → Free arrows are intentional text art, not emoji.
var emojiPattern = regexp.MustCompile(
	`[\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}\x{2B00}-\x{2BFF}\x{FE0F}\x{1F1E6}-\x{1F1FF}\x{2300}-\x{23FF}]`)

// renderReadme renders the repository's real README through the same pipeline
// the deploy uses (markdown → goldmark → wrapTables → injectIcons). The icon
// needles are byte-exact prefixes of that markup, so every test here runs
// against the live document rather than a synthetic fixture: a markdown edit
// that moves a section, a lead, or an id fails the build here instead of
// silently dropping its icon on the published page.
func renderReadme(t *testing.T) string {
	t.Helper()
	readme := filepath.Join("..", "..", "README.md")
	if _, err := os.Stat(readme); err != nil {
		t.Skipf("repository README not found from %s: %v", cwdOrError(), err)
	}
	body, err := renderMarkdown(readme, t.TempDir(), "yanstd888/9routerx")
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	return string(body)
}

func cwdOrError() string {
	dir, err := os.Getwd()
	if err != nil {
		return err.Error()
	}
	return dir
}

// TestInjectIconsCoversEveryMarker proves the injected output carries one
// icon for every entry the maps name, the current benefits list is tagged
// and fully marked, and the README article holds no emoji.
func TestInjectIconsCoversEveryMarker(t *testing.T) {
	body := renderReadme(t)

	for id, name := range headingIcons {
		want := `<h2 id="` + id + `">` + iconSVG(name)
		if _, ok := iconPaths[name]; !ok {
			t.Errorf("heading %s names icon %q which iconPaths does not define", id, name)
		}
		if !strings.Contains(body, want) &&
			!strings.Contains(body, `<h3 id="`+id+`">`+iconSVG(name)) {
			t.Errorf("heading %q carries no %s icon", id, name)
		}
	}

	for needle, name := range listIcons {
		if _, ok := iconPaths[name]; !ok {
			t.Errorf("list lead %q names icon %q which iconPaths does not define", needle, name)
		}
		mark := "<li>" + iconSVG(name) + needle[4:]
		if !strings.Contains(body, mark) {
			t.Errorf("list lead %q carries no %s icon", needle, name)
		}
	}

	for needle, name := range linkIcons {
		if _, ok := iconPaths[name]; !ok {
			t.Errorf("link %q names icon %q which iconPaths does not define", needle, name)
		}
		mark := ">" + iconSVG(name) + needle[1:]
		if !strings.Contains(body, mark) {
			t.Errorf("link %q carries no %s icon", needle, name)
		}
	}

	for _, class := range []string{"md-pros"} {
		start := strings.Index(body, `<ul class=`+class+`>`)
		if start < 0 {
			t.Fatalf("no ul.%s in the rendered README", class)
		}
		end := strings.Index(body[start:], "</ul>")
		list := body[start : start+end]
		items := strings.Count(list, "<li>")
		marks := strings.Count(list, iconOpen)
		if items == 0 || marks != items {
			t.Errorf("ul.%s has %d items but %d marks; every item needs one", class, items, marks)
		}
	}

	if emojiPattern.MatchString(body) {
		t.Errorf("rendered README still contains emoji: %q", emojiPattern.FindString(body))
	}
}

// Every current section must remain annotated; deleting stale registry entries
// alone must not turn a rewritten README into an unmarked page.
func TestEveryCurrentReadmeSectionHasAnIcon(t *testing.T) {
	body := renderReadme(t)
	headings := regexp.MustCompile(`<h2 id="([^"]+)">`).FindAllStringSubmatch(body, -1)
	if len(headings) == 0 {
		t.Fatal("README has no sections")
	}
	for _, heading := range headings {
		if _, ok := headingIcons[heading[1]]; !ok {
			t.Errorf("current README section %q has no icon mapping", heading[1])
		}
	}
}

// TestInjectIconsIsIdempotent guards the pipeline contract: injectIcons runs
// after wrapTables on fresh goldmark output every build, but a second run —
// a re-render over injected output, or a future call-site that processes
// twice — must not double-inject.
func TestInjectIconsIsIdempotent(t *testing.T) {
	body := renderReadme(t)
	once := injectIcons(body)
	twice := injectIcons(once)
	if len(once) != len(twice) || once != twice {
		t.Errorf("injectIcons is not idempotent: first run %d bytes, second %d", len(once), len(twice))
	}
}

// TestTemplatesContainNoEmojiGlyphs pins the template chrome to the same
// no-emoji rule the markdown sources follow: the wiki header used to draw
// its menu, theme, close and links controls with text glyphs (menu trigram,
// moon and sun, multiplication x, midline ellipsis), which are emoji-range
// characters on every published page. Both templates and every spliced-in
// icon must grep clean.
func TestTemplatesContainNoEmojiGlyphs(t *testing.T) {
	for name, tmpl := range map[string]string{
		"pageTemplate": pageTemplate,
		"wikiTemplate": wikiTemplate,
	} {
		if emojiPattern.MatchString(tmpl) {
			t.Errorf("%s still contains emoji-range characters: %q", name, emojiPattern.FindString(tmpl))
		}
	}
	for _, icon := range []string{wikiMenuIcon, wikiCloseIcon, wikiMoonIcon, wikiSunIcon, wikiMoreIcon} {
		if !strings.HasPrefix(icon, iconOpen) || !strings.HasSuffix(icon, "</svg>") {
			t.Errorf("wiki chrome icon is not a complete injected svg: %q", icon)
		}
	}
}

// TestIconPathsAreWellFormed walks every drawing: non-empty, a fragment
// (never a full <svg> element — iconOpen supplies the wrapper), and made only
// of ASCII markup, so an injected icon can never smuggle an emoji back in.
func TestIconPathsAreWellFormed(t *testing.T) {
	for name, path := range iconPaths {
		if path == "" {
			t.Errorf("icon %q has an empty drawing", name)
		}
		if strings.Contains(path, "<svg") || strings.Contains(path, "</svg>") {
			t.Errorf("icon %q embeds an svg element; iconPaths holds fragments only", name)
		}
		if emojiPattern.MatchString(path) {
			t.Errorf("icon %q drawing contains emoji-range characters", name)
		}
	}
	if _, ok := iconPaths["check"]; !ok {
		t.Error("iconPaths has no check mark for the former check list")
	}
	if _, ok := iconPaths["x"]; !ok {
		t.Error("iconPaths has no cross for the former cross list")
	}
}

// TestReadmeInternalAnchorsResolve is the dead-anchor guard for the emoji
// removal: GitHub slugged the emoji-prefixed `## Quick Start` to
// `-quick-start` (leading dash — the emoji dropped, the space kept), the
// plain heading slugs to `quick-start`, and every `](#…)` link in the
// sources had to move with it.
// The rendered body must not carry one href="#…" without a matching id.
func TestReadmeInternalAnchorsResolve(t *testing.T) {
	body := renderReadme(t)

	ids := map[string]bool{}
	idRe := regexp.MustCompile(`<h[1-4][^>]*id="([^"]+)"`)
	for _, m := range idRe.FindAllStringSubmatch(body, -1) {
		ids[m[1]] = true
	}
	if len(ids) == 0 {
		t.Fatal("rendered README has no heading ids")
	}

	hrefRe := regexp.MustCompile(`href="#([^"]+)"`)
	missing := map[string]bool{}
	count := 0
	for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
		count++
		if !ids[m[1]] {
			missing[m[1]] = true
		}
	}
	if count == 0 {
		t.Fatal("rendered README has no internal anchor links to check")
	}
	for anchor := range missing {
		t.Errorf("dead anchor: href=\"#%s\" matches no heading id in the render", anchor)
	}
}
