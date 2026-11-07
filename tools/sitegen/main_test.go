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

	shot := filepath.Join(root, "docs", "screenshots", "providers.png")
	if err := os.MkdirAll(filepath.Dir(shot), 0o755); err != nil {
		t.Fatalf("MkdirAll(screenshots): %v", err)
	}
	writeFixture(t, shot, "png-bytes")

	out := filepath.Join(root, "_site")
	if err := generate(readme, assets, out, "yanstd888/9routerx"); err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, rel := range []string{
		"index.html",
		"style.css",
		"tg.html",
		"tg/index.html",
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

	if strings.Contains(page, "https://github.com/decolua") {
		t.Error("absolute link was rewritten")
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

	err := generate(readme, assets, filepath.Join(root, "_site"), "yanstd888/9routerx")
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
// SVG data URI, which crawlers never picked up), an absolute og:image, and the
// wiki door ahead of the GitHub links, with the footer's wiki quick links
// pointing at pages the wiki actually publishes.
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
}
