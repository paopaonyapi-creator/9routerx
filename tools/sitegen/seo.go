// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const siteBaseURL = "https://yanstd888.github.io/9routerx/"

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
