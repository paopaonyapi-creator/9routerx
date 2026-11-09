// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCrawlFiles(t *testing.T) {
	out := t.TempDir()
	for _, path := range []string{"index.html", "providers.html", "security.html", "tg/index.html"} {
		if err := writeFile(filepath.Join(out, filepath.FromSlash(path)), []byte("<h1>Test</h1>")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeCrawlFiles(out, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc sitemapDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Entries) != 6 {
		t.Fatalf("sitemap entries = %d, want 6", len(doc.Entries))
	}
	for _, entry := range doc.Entries {
		if !strings.HasPrefix(entry.Location, siteBaseURL) || entry.Lastmod != "2026-11-09" {
			t.Fatalf("invalid sitemap entry: %+v", entry)
		}
	}
	robots, err := os.ReadFile(filepath.Join(out, "robots.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(robots) != "User-agent: *\nAllow: /\nSitemap: "+siteBaseURL+"sitemap.xml\n" {
		t.Fatalf("unexpected robots.txt: %s", robots)
	}
}

func TestLLMFiles(t *testing.T) {
	root := t.TempDir()
	readme := filepath.Join(root, "README.md")
	wiki := filepath.Join(root, "wiki")
	assets := filepath.Join(root, "site")
	out := filepath.Join(root, "output")
	writeFixture(t, readme, "<div>\n# Readme\n</div>\n\n```html\n<h1>Keep code</h1>\n```\n")
	for path, body := range map[string]string{
		"index.md":   "---\r\ntitle: Wiki Home\r\n---\r\n# Home\r\n",
		"install.md": "---\ntitle: Install\norder: 2\nsection: Start\n---\n# Install\ninstall body",
		"api.md":     "---\ntitle: API\norder: 3\nsection: Start\n---\n# API\napi body",
	} {
		if err := os.MkdirAll(wiki, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(wiki, path), body)
	}
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(assets, "llms.txt"), "# Context\n")
	if err := writeLLMFiles(readme, assets, wiki, out); err != nil {
		t.Fatal(err)
	}
	corpus := readFile(t, filepath.Join(out, "llms-full.txt"))
	if strings.Contains(corpus, "title: Wiki Home") || strings.Contains(corpus, "<div>") {
		t.Fatal("corpus contains frontmatter or a prose HTML wrapper")
	}
	if !strings.Contains(corpus, "<h1>Keep code</h1>") {
		t.Fatal("corpus changed a fenced code sample")
	}
	if strings.Index(corpus, "# Install") > strings.Index(corpus, "# API") {
		t.Fatal("corpus does not follow navigation order")
	}
	brief := readFile(t, filepath.Join(out, "llms.txt"))
	if !strings.Contains(brief, "install.html") || !strings.Contains(brief, "api.html") {
		t.Fatal("documentation map is incomplete")
	}
}

func TestCorpusInlineCode(t *testing.T) {
	src := "<p>Use `.changes/<pr-number>-<slug>.md` and ``<literal>`value``.</p>"
	want := "Use `.changes/<pr-number>-<slug>.md` and ``<literal>`value``."
	if got := cleanCorpus(src); got != want {
		t.Fatalf("corpus inline code = %q, want %q", got, want)
	}
}

func TestStructuredData(t *testing.T) {
	body := []byte(`<h1 id="readme">Readme</h1><h2>Frequently Asked Questions (FAQ)</h2><details><summary>Can I use &quot;quotes&quot;?</summary><p>Yes, with &lt;/script&gt; escaped.</p></details><h2>Other</h2><details><summary>Not an FAQ</summary><p>Exclude this answer.</p></details>`)
	for _, version := range []string{"9.9.9", ""} {
		output, err := renderPage(body, "yanstd888/9routerx", heroStats{Version: version})
		if err != nil {
			t.Fatal(err)
		}
		blocks := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindAllSubmatch(output, -1)
		if len(blocks) != 2 {
			t.Fatalf("JSON-LD blocks = %d, want 2", len(blocks))
		}
		var software map[string]any
		if err := json.Unmarshal(blocks[0][1], &software); err != nil {
			t.Fatal(err)
		}
		if version == "" {
			if _, ok := software["softwareVersion"]; ok {
				t.Fatal("empty version was not omitted")
			}
		} else if software["softwareVersion"] != version {
			t.Fatalf("version = %v, want %s", software["softwareVersion"], version)
		}
		var faq map[string]any
		if err := json.Unmarshal(blocks[1][1], &faq); err != nil {
			t.Fatal(err)
		}
		if faq["@type"] != "FAQPage" || len(faq["mainEntity"].([]any)) != 1 {
			t.Fatalf("invalid FAQ schema: %v", faq)
		}
	}
}
