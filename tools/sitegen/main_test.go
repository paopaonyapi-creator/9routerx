// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlobURL(t *testing.T) {
	const repo = "yanstd888/9routerx"

	tests := []struct {
		name string
		dest string
		want string
	}{
		{
			name: "repository markdown becomes a blob url",
			dest: "DATABASE.md",
			want: "https://github.com/yanstd888/9routerx/blob/main/DATABASE.md",
		},
		{
			name: "nested path is preserved",
			dest: "docs/TELEGRAM_RELEASE_NOTIFICATIONS.md",
			want: "https://github.com/yanstd888/9routerx/blob/main/docs/TELEGRAM_RELEASE_NOTIFICATIONS.md",
		},
		{
			name: "fragment survives",
			dest: "docs/DATABASE.md#migration",
			want: "https://github.com/yanstd888/9routerx/blob/main/docs/DATABASE.md#migration",
		},
		{
			name: "heading anchor is left alone",
			dest: "#-quick-start",
			want: "#-quick-start",
		},
		{
			name: "absolute url is left alone",
			dest: "https://github.com/decolua/9router",
			want: "https://github.com/decolua/9router",
		},
		{
			name: "scheme relative url is left alone",
			dest: "//example.com/x",
			want: "//example.com/x",
		},
		{
			name: "site absolute path is left alone",
			dest: "/tg",
			want: "/tg",
		},
		{
			name: "mailto is left alone",
			dest: "mailto:someone@example.com",
			want: "mailto:someone@example.com",
		},
		{
			name: "escape above the repository is refused",
			dest: "../elsewhere/FILE.md",
			want: "../elsewhere/FILE.md",
		},
		{
			name: "empty destination is left alone",
			dest: "",
			want: "",
		},
		{
			name: "fragment only link is left alone",
			dest: "#",
			want: "#",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(blobURL(repo, []byte(tt.dest)))
			if got != tt.want {
				t.Errorf("blobURL(%q) = %q, want %q", tt.dest, got, tt.want)
			}
		})
	}
}

func TestIsAsset(t *testing.T) {
	tests := []struct {
		dest string
		want bool
	}{
		{dest: "docs/screenshots/providers.png", want: true},
		{dest: "docs/icon.svg", want: true},
		{dest: "shot.WEBP", want: true},
		{dest: "DATABASE.md", want: false},
		{dest: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.dest, func(t *testing.T) {
			if got := isAsset([]byte(tt.dest)); got != tt.want {
				t.Errorf("isAsset(%q) = %v, want %v", tt.dest, got, tt.want)
			}
		})
	}
}

func TestHasScheme(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{raw: "https://img.shields.io/badge/x", want: true},
		{raw: "mailto:a@b.c", want: true},
		{raw: "//example.com", want: false},
		{raw: "docs/a.md", want: false},
		{raw: "3d:model", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := hasScheme(tt.raw); got != tt.want {
				t.Errorf("hasScheme(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// generate() is the contract the Pages workflow depends on: the README becomes
// index.html, and the Telegram redirect lands on every path the badge and the
// dashboard link can produce.
func TestGenerate(t *testing.T) {
	root := t.TempDir()

	readme := filepath.Join(root, "README.md")
	writeFixture(t, readme, strings.Join([]string{
		"# Title",
		"",
		"See [the database notes](docs/DATABASE.md) and [upstream](https://example.com).",
		"",
		"![Providers](docs/screenshots/providers.png)",
		"",
		"![Build](https://img.shields.io/badge/build-passing-green)",
		"",
	}, "\n"))

	assets := filepath.Join(root, "site")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatalf("MkdirAll(site): %v", err)
	}
	writeFixture(t, filepath.Join(assets, "style.css"), "body{}")
	writeFixture(t, filepath.Join(assets, "tg.html"), "<a href=\"tg\">tg</a>")
	writeFixture(t, filepath.Join(assets, "logo-9routerx-128.png"), "logo-bytes")
	writeFixture(t, filepath.Join(assets, "llms.txt"), "# 9RouterX\n")

	shot := filepath.Join(root, "docs", "screenshots", "providers.png")
	if err := os.MkdirAll(filepath.Dir(shot), 0o755); err != nil {
		t.Fatalf("MkdirAll(screenshots): %v", err)
	}
	writeFixture(t, shot, "png-bytes")

	out := filepath.Join(root, "_site")
	// Non-zero stats: the hero must render the numbers it is given, so the
	// live-numbers strip is exercised here with known values.
	if err := generate(readme, assets, out, "yanstd888/9routerx", heroStats{Version: "9.9.9", Providers: 123, WikiPages: 7}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, rel := range []string{
		"index.html",
		"style.css",
		"tg.html",
		"tg/index.html",
		"llms.txt",
		"logo-9routerx-128.png",
		filepath.FromSlash("docs/screenshots/providers.png"),
	} {
		if _, err := os.Stat(filepath.Join(out, rel)); err != nil {
			t.Errorf("expected %s in the output: %v", rel, err)
		}
	}

	page := readFile(t, filepath.Join(out, "index.html"))

	for _, want := range []string{
		`href="https://github.com/yanstd888/9routerx/blob/main/docs/DATABASE.md"`,
		`src="docs/screenshots/providers.png"`,
		`src="https://img.shields.io/badge/build-passing-green"`,
		`<h1 id="title">Title</h1>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html is missing %q", want)
		}
	}

	// Absolute links inside the README body must pass through unrewritten.
	// The check is scoped to the rendered README article because the page
	// chrome's own footer legitimately links to the upstream repositories
	// (the attribution line), and those are supposed to stay absolute. The
	// closing tag search starts after bodyStart: the hero's feature cards
	// are <article> elements too, and Index would otherwise find theirs.
	bodyStart := strings.Index(page, `<article id="readme"`)
	if bodyStart < 0 {
		t.Fatal("index.html has no README article to check")
	}
	bodyEnd := bodyStart + strings.Index(page[bodyStart:], "</article>")
	if bodyEnd <= bodyStart {
		t.Fatal("the README article is never closed")
	}
	if strings.Contains(page[bodyStart:bodyEnd], "https://github.com/decolua") {
		t.Error("absolute link was rewritten")
	}

	// The hero renders the stats it was handed — and nothing else. These are
	// the fixture's numbers, proving the strip comes from data, not markup.
	for _, want := range []string{"<dd>123</dd>", "<dd>7</dd>", "<dd>v9.9.9</dd>"} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html is missing the hero stat %q", want)
		}
	}
}

// A README link to a file that is not in the repository must fail the build
// rather than publish a 404 image on the project page.
func TestGenerateFailsOnMissingImage(t *testing.T) {
	root := t.TempDir()
	readme := filepath.Join(root, "README.md")
	writeFixture(t, readme, "![gone](docs/screenshots/gone.png)\n")

	assets := filepath.Join(root, "site")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatalf("MkdirAll(site): %v", err)
	}
	writeFixture(t, filepath.Join(assets, "style.css"), "body{}")
	writeFixture(t, filepath.Join(assets, "tg.html"), "<a></a>")
	writeFixture(t, filepath.Join(assets, "logo-9routerx-128.png"), "logo-bytes")
	writeFixture(t, filepath.Join(assets, "llms.txt"), "# 9RouterX\n")

	err := generate(readme, assets, filepath.Join(root, "_site"), "yanstd888/9routerx", heroStats{})
	if err == nil {
		t.Fatal("generate() succeeded with a missing image, want an error")
	}
	if !strings.Contains(err.Error(), "gone.png") {
		t.Errorf("error does not name the missing file: %v", err)
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}

// copyIcons feeds the favicon links and the og:image meta both templates emit,
// so every listed icon must land in the output — and a missing one must fail
// the build, not skip quietly behind a site-wide 404.
func TestCopyIcons(t *testing.T) {
	root := t.TempDir()
	icons := filepath.Join(root, "web", "public")
	if err := os.MkdirAll(icons, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range brandIcons {
		writeFixture(t, filepath.Join(icons, name), "icon-bytes-"+name)
	}

	out := filepath.Join(root, "_site")
	if err := copyIcons(icons, out); err != nil {
		t.Fatalf("copyIcons: %v", err)
	}
	for _, name := range brandIcons {
		got := readFile(t, filepath.Join(out, name))
		if got != "icon-bytes-"+name {
			t.Errorf("%s was not copied verbatim", name)
		}
	}

	// One icon short of the set — say, a rename in web/public that did not
	// reach the generator — must stop the build with the file named.
	if err := os.Remove(filepath.Join(icons, "logo-9routerx.png")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	err := copyIcons(icons, filepath.Join(root, "_site-2"))
	if err == nil {
		t.Fatal("copyIcons() succeeded with a missing icon, want an error")
	}
	if !strings.Contains(err.Error(), "logo-9routerx.png") {
		t.Errorf("error does not name the missing icon: %v", err)
	}
}

// The page chrome carries the brand: real icon files (not the retired inline
// SVG data URI, which crawlers never picked up), an absolute og:image, the
// real 128px logo in the masthead (the retired blue "9" plate is gone), and
// the wiki door ahead of the GitHub links, with the footer's links pointing
// at pages the wiki actually publishes. The hero's contracts are here too:
// the scramble headline, the copy button, the lazy-sized screenshot, and the
// reduced-motion stand-down.
func TestPageTemplateChrome(t *testing.T) {
	if strings.Contains(pageTemplate, "data:image/svg") {
		t.Error("the page still carries the hardcoded SVG data-URI favicon")
	}
	for _, want := range []string{
		`<link rel="icon" href="favicon.ico" sizes="32x32" />`,
		`<link rel="icon" type="image/png" href="favicon-32x32.png" />`,
		`<link rel="apple-touch-icon" href="apple-touch-icon.png" />`,
	} {
		if !strings.Contains(pageTemplate, want) {
			t.Errorf("page head is missing the favicon link %q", want)
		}
	}
	if !strings.Contains(pageTemplate, `og:image" content="`+ogImageURL+`"`) {
		t.Errorf("page head is missing og:image with the absolute logo URL %s", ogImageURL)
	}

	// Title and wordmark carry the brand spelling: "9RouterX", capital R and
	// capital X — the owner's original complaint was the lowercase wordmark.
	if !strings.Contains(pageTemplate, "<title>9RouterX — ") {
		t.Errorf("the document title does not start with the brand spelling: %q", "9RouterX — ")
	}
	if !strings.Contains(pageTemplate, `<span>9RouterX</span>`) {
		t.Error("the wordmark text is not the capitalized brand spelling 9RouterX")
	}
	if !strings.Contains(pageTemplate, `src="logo-9routerx-128.png"`) {
		t.Error("the masthead does not use the real 128px logo asset")
	}
	if strings.Contains(pageTemplate, `wordmark-mark" style="background:`) {
		t.Error("the masthead still carries the colored-letter plate instead of the logo")
	}

	wikiAt := strings.Index(pageTemplate, `href="installation.html">Wiki</a>`)
	repoAt := strings.Index(pageTemplate, `>Repository</a>`)
	if wikiAt < 0 {
		t.Fatal("the nav has no Wiki link; the 20-page wiki has no door from the homepage")
	}
	if repoAt < 0 {
		t.Fatal("the nav has no Repository link")
	}
	if wikiAt > repoAt {
		t.Error("the Wiki link is not before Repository in the nav")
	}

	// The footer's wiki row is a shortcut into the documentation, so every
	// entry must name a page the wiki generator actually publishes — the same
	// set the pages.yml dead-link assert walks after a build.
	for _, slug := range []string{"installation", "providers", "token-saver", "dashboard", "cli", "security"} {
		if !strings.Contains(pageTemplate, `href="`+slug+`.html"`) {
			t.Errorf("the footer wiki row is missing %s.html", slug)
		}
	}

	// Hero contracts. The headline decode, the copy button, and the
	// reduced-motion stand-down are behaviour pages.yml cannot see from
	// static output — they live only in the template, so the template is what
	// the assertions guard.
	for _, want := range []string{
		`id="hero-title"`,        // the decode target
		`prefers-reduced-motion`, // every effect stands down
		`id="copy-install"`,      // the install block's copy button
		`id="install-cmd"`,       // the typed command
		`class="hero-chips"`,     // the feature chip strip
		`class="hero-stats"`,     // the live-numbers strip
		`class="hero-shot"`,      // the framed dashboard screenshot
		`src="docs/screenshots/providers.png"`,
	} {
		if !strings.Contains(pageTemplate, want) {
			t.Errorf("the page template is missing the hero contract %q", want)
		}
	}

	// The hero screenshot obeys the same lazy+sized contract the README
	// gallery is held to by pages.yml.
	shot := strings.Index(pageTemplate, `src="docs/screenshots/providers.png"`)
	if shot < 0 {
		t.Fatal("the hero has no screenshot")
	}
	tagEnd := strings.Index(pageTemplate[shot:], ">")
	tag := pageTemplate[shot : shot+tagEnd]
	if !strings.Contains(tag, `loading="lazy"`) || !strings.Contains(tag, `width="1280"`) || !strings.Contains(tag, `height="800"`) {
		t.Errorf("the hero screenshot is not lazy and sized: %s", tag)
	}

	// The masthead logo obeys it too — the lazy assert in pages.yml counts
	// every <img> on every page, masthead included.
	logoAt := strings.Index(pageTemplate, `src="logo-9routerx-128.png"`)
	if logoAt < 0 {
		t.Fatal("the masthead has no logo")
	}
	logoEnd := strings.Index(pageTemplate[logoAt:], ">")
	logoTag := pageTemplate[logoAt : logoAt+logoEnd]
	if !strings.Contains(logoTag, `loading="lazy"`) || !strings.Contains(logoTag, `width="128"`) || !strings.Contains(logoTag, `height="128"`) {
		t.Errorf("the masthead logo is not lazy and sized: %s", logoTag)
	}

	// The hero numbers come from build-time data, never from markup: every
	// stat the template renders is guarded by an `if` on the Stats field, so
	// a build that cannot compute a number omits it instead of inventing one.
	for _, want := range []string{`{{ if .Stats.Providers }}`, `{{ if .Stats.WikiPages }}`, `{{ if .Stats.Version }}`} {
		if !strings.Contains(pageTemplate, want) {
			t.Errorf("the hero stats are not guarded by %q — the hero could render a number the build did not compute", want)
		}
	}
	if strings.Contains(pageTemplate, "<dd>140</dd>") || strings.Contains(pageTemplate, "<dd>20</dd>") {
		t.Error("the template hardcodes a provider or wiki count in the markup")
	}
}

// The hero's provider count is a line scan over the generated registry, so it
// is tested against a fixture with known contents — including the traps the
// scan must not fall into (nested string arrays, the HiddenKinds field, the
// last entry being hidden).
func TestCountProvidersIn(t *testing.T) {
	// Interpreted string literal on purpose: the scan depends on real tab
	// indentation, which a raw literal would not carry.
	fixture := "package providers\n" +
		"\n" +
		"var ProviderRegistry = map[string]ProviderMeta{\n" +
		"\t\"agnes\": {\n" +
		"\t\tID:       \"agnes\",\n" +
		"\t\tName:     \"Agnes\",\n" +
		"\t\tCategory: CategoryAPIKey,\n" +
		"\t},\n" +
		"\t\"gitlab\": {\n" +
		"\t\tID:          \"gitlab\",\n" +
		"\t\tName:        \"GitLab\",\n" +
		"\t\tCategory:    CategoryAPIKey,\n" +
		"\t\tHidden:      true,\n" +
		"\t\tHiddenKinds: []string{\"chat\"},\n" +
		"\t},\n" +
		"\t\"selfhosted-stt\": {\n" +
		"\t\tID:           \"selfhosted-stt\",\n" +
		"\t\tName:         \"Self-Hosted STT\",\n" +
		"\t\tCategory:     CategoryAPIKey,\n" +
		"\t\tServiceKinds: []string{\"stt\"},\n" +
		"\t},\n" +
		"}\n"
	if got := countProvidersIn([]byte(fixture)); got != 2 {
		t.Errorf("countProvidersIn = %d, want 2 (3 entries, 1 hidden)", got)
	}
}

// countWikiPages must mirror generateWiki's published set exactly: every
// wiki/*.md except index.md, whose slug belongs to the README homepage.
func TestCountWikiPages(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"installation.md", "cli.md", "index.md", "team.md"} {
		writeFixture(t, filepath.Join(root, name), "# x")
	}
	if got := countWikiPages(root); got != 3 {
		t.Errorf("countWikiPages = %d, want 3 (4 files, index.md unpublished)", got)
	}
	if got := countWikiPages(filepath.Join(root, "missing")); got != 0 {
		t.Errorf("countWikiPages on a missing dir = %d, want 0", got)
	}
}
