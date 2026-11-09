// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
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
