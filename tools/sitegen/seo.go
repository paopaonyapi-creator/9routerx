// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"encoding/xml"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const siteBaseURL = "https://yanstd888.github.io/9routerx/"

var pageDescriptions = map[string]string{
	"installation":      "Install 9RouterX through npm, release binaries or GHCR and configure its background service.",
	"client-setup":      "Connect Claude Code, Cursor, Codex, Cline and other clients to the authenticated local gateway.",
	"api":               "Use the OpenAI-compatible, Anthropic-compatible, MCP and A2A gateway endpoints.",
	"migration":         "Move an existing 9Router installation to 9RouterX while preserving its compatible SQLite data.",
	"dashboard":         "Manage provider connections, API keys, combos and request analytics from the embedded dashboard.",
	"providers":         "Explore 155 providers and 248 free models across 36 providers, with authentication requirements explained.",
	"combos":            "Group models into routing combos with fallback, round-robin, sticky, capacity and fusion strategies.",
	"security":          "Protect the local gateway with API key authentication, session controls and credential safeguards.",
	"token-saver":       "Configure RTK compression and per-tool filters to reduce tool-output token consumption.",
	"settings":          "Configure gateway behavior, authentication, display preferences and diagnostic retention.",
	"usage-analytics":   "Inspect request usage, costs, cache activity and compression analytics by time window.",
	"cli":               "Run 9RouterX daemon, status, restart, log and update commands from the terminal.",
	"architecture":      "Understand the Go gateway architecture, provider isolation and subscription-to-free fallback routing.",
	"database":          "Understand SQLite storage, DATA_DIR paths and compatibility with the original 9Router database.",
	"parity":            "Review the feature parity and compatibility boundaries of the native Go port.",
	"advanced-features": "Configure advanced gateway integrations including tunnels, web executors and webhooks.",
	"operations":        "Operate the gateway with deployment, monitoring, backup and troubleshooting procedures.",
	"governance":        "Control per-key budgets, request limits, IP allowlists and other gateway governance policies.",
	"contributing":      "Contribute changes using the repository's development, test and review conventions.",
	"screenshots":       "View dashboard screenshots showing the provider, routing and analytics interfaces.",
	"team":              "Read the project ownership and contributor acknowledgments for 9RouterX.",
}

func describePage(p *page) string {
	if description := pageDescriptions[p.Slug]; description != "" {
		return description
	}
	return "Read the 9RouterX guide to " + html.UnescapeString(p.Title) + "."
}

func writeLLMFiles(readmePath, assetsDir, wikiDir, outDir string) error {
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		return err
	}
	entries, err := filepath.Glob(filepath.Join(wikiDir, "*.md"))
	if err != nil {
		return err
	}
	var pages []*page
	var wikiHome *page
	for _, entry := range entries {
		src, err := os.ReadFile(entry)
		if err != nil {
			return err
		}
		fm, body := splitFrontmatter(strings.ReplaceAll(string(src), "\r\n", "\n"))
		slug := strings.TrimSuffix(filepath.Base(entry), ".md")
		title := fm.Title
		if title == "" {
			title = firstHeading(body)
		}
		if title == "" {
			title = slug
		}
		p := &page{Slug: slug, Title: title, Order: fm.Order, Section: fm.Section, Text: body}
		if strings.EqualFold(slug, "index") {
			wikiHome = p
		} else {
			pages = append(pages, p)
		}
	}
	sort.SliceStable(pages, func(i, j int) bool {
		if pages[i].Order != pages[j].Order {
			return pages[i].Order < pages[j].Order
		}
		return pages[i].Slug < pages[j].Slug
	})
	var corpus strings.Builder
	corpus.WriteString("# 9RouterX README\n\n" + cleanCorpus(string(readme)) + "\n\n")
	if wikiHome != nil {
		corpus.WriteString("# " + html.UnescapeString(wikiHome.Title) + " (wiki source home)\n\n" + cleanCorpus(wikiHome.Text) + "\n\n")
	}
	intro, err := os.ReadFile(filepath.Join(assetsDir, "llms.txt"))
	if err != nil {
		return err
	}
	var brief strings.Builder
	brief.Write(intro)
	brief.WriteString("\n\n## Documentation Map\n\n")
	for _, group := range groupBySection(pages) {
		for _, p := range group.Pages {
			brief.WriteString("- [" + html.UnescapeString(p.Title) + "](" + siteBaseURL + p.Slug + ".html): " + describePage(p) + "\n")
			corpus.WriteString("# " + html.UnescapeString(p.Title) + "\n\n" + cleanCorpus(p.Text) + "\n\n")
		}
	}
	if err := writeFile(filepath.Join(outDir, "llms-full.txt"), []byte(corpus.String())); err != nil {
		return err
	}
	return writeFile(filepath.Join(outDir, "llms.txt"), []byte(brief.String()))
}

var corpusTag = regexp.MustCompile(`<[^>]+>`)

// Keep code examples verbatim while removing prose-only HTML wrappers.
func cleanCorpus(src string) string {
	var lines []string
	fence := ""
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			mark := trimmed[:3]
			if fence == "" {
				fence = mark
			} else if fence == mark {
				fence = ""
			}
			lines = append(lines, line)
			continue
		}
		if fence == "" {
			line = html.UnescapeString(corpusTag.ReplaceAllString(line, ""))
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type sitemapEntry struct {
	Location string `xml:"loc"`
	Lastmod  string `xml:"lastmod"`
	Priority string `xml:"priority"`
}

type sitemapDocument struct {
	XMLName xml.Name       `xml:"urlset"`
	XMLNS   string         `xml:"xmlns,attr"`
	Entries []sitemapEntry `xml:"url"`
}

func writeCrawlFiles(outDir string, builtAt time.Time) error {
	var paths []string
	if err := filepath.WalkDir(outDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".html") {
			rel, err := filepath.Rel(outDir, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("list sitemap pages: %w", err)
	}
	paths = append(paths, "llms.txt", "llms-full.txt")
	sort.Strings(paths)
	doc := sitemapDocument{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, path := range paths {
		priority := "0.8"
		switch path {
		case "index.html":
			priority = "1.0"
		case "installation.html", "client-setup.html", "providers.html", "token-saver.html":
			priority = "0.9"
		}
		doc.Entries = append(doc.Entries, sitemapEntry{
			Location: siteBaseURL + path,
			Lastmod:  builtAt.UTC().Format("2006-01-02"), Priority: priority,
		})
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outDir, "sitemap.xml"), append([]byte(xml.Header), data...)); err != nil {
		return err
	}
	return writeFile(filepath.Join(outDir, "robots.txt"), []byte("User-agent: *\nAllow: /\nSitemap: "+siteBaseURL+"sitemap.xml\n"))
}
