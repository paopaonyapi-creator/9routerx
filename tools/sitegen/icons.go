// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import "strings"

// README annotations stay in the HTML renderer so GitHub Markdown stays plain.
// Match the current document, not an obsolete feature-list layout; live-render
// tests pin every heading, list lead and link. Icons are inline SVGs with no
// font or external request.

// iconOpen wraps every injected icon. Deliberately minimal — the stroke
// presentation attributes (fill, stroke, linecap, linejoin) live in the
// stylesheet, so each icon instance costs only its drawing.
const iconOpen = `<svg class=ico viewBox="0 0 24 24" aria-hidden=true>`

// iconPaths holds the inner drawing of every icon, keyed by name. All are
// stroke-style drawings for a 24x24 grid; the stylesheet strokes them with
// currentColor at width 2.
var iconPaths = map[string]string{
	// — section headings (the ids the emoji headings slug to) —
	"help-circle":   `<circle cx="12" cy="12" r="10"/><path d="M9.1 9a3 3 0 0 1 5.8 1c0 2-3 2.5-3 4"/><path d="M12 17.5h.01"/>`,
	"compass":       `<circle cx="12" cy="12" r="10"/><path d="m16 8-2.5 6.5L7 17l2.5-6.5z"/>`,
	"refresh-cw":    `<path d="M21 12a9 9 0 1 1-2.6-6.4L21 8"/><path d="M21 3v5h-5"/>`,
	"zap":           `<path d="M13 2 3 14h9l-1 8 10-12h-9z"/>`,
	"lightbulb":     `<path d="M9 18h6"/><path d="M10 22h4"/><path d="M12 2a7 7 0 0 0-4 12.7c.6.5 1 1.4 1 2.3h6c0-.9.4-1.8 1-2.3A7 7 0 0 0 12 2z"/>`,
	"database":      `<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14c0 1.7 4 3 9 3s9-1.3 9-3V5"/><path d="M3 12c0 1.7 4 3 9 3s9-1.3 9-3"/>`,
	"camera":        `<path d="M4 8h3l2-3h6l2 3h3a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1z"/><circle cx="12" cy="13" r="3.5"/>`,
	"settings":      `<circle cx="12" cy="12" r="3.5"/><path d="M12 1.8v3M12 19.2v3M1.8 12h3M19.2 12h3M4.5 4.5l2.1 2.1M17.4 17.4l2.1 2.1M19.5 4.5l-2.1 2.1M6.6 17.4l-2.1 2.1"/>`,
	"flask-conical": `<path d="M10 2v7.5L4.7 20.5a1 1 0 0 0 .9 1.5h12.8a1 1 0 0 0 .9-1.5L14 9.5V2"/><path d="M8.5 2h7"/><path d="M7 15h10"/>`,
	"key":           `<circle cx="8" cy="16" r="5"/><path d="m11.5 12.5 9-9"/><path d="m17 7 3 3"/>`,
	"users":         `<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.9"/><path d="M16 3.1a4 4 0 0 1 0 7.8"/>`,
	"book-open":     `<path d="M2 4h6a4 4 0 0 1 4 4v13a3 3 0 0 0-3-3H2z"/><path d="M22 4h-6a4 4 0 0 0-4 4v13a3 3 0 0 1 3-3h7z"/>`,
	"heart":         `<path d="M19 14c1.5-1.5 3-3.2 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.8 0-3 .5-4.5 2-1.5-1.5-2.7-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4 3 5.5l7 7z"/>`,
	"file-text":     `<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z"/><path d="M14 2v5h5"/><path d="M9 13h6"/><path d="M9 17h6"/>`,

	// — Key Features list items (keyed on the bold lead the emoji preceded) —
	"compress":           `<path d="M4 14h6v6"/><path d="m3 21 7-7"/><path d="M20 10h-6V4"/><path d="m21 3-7 7"/>`,
	"eraser":             `<path d="m7 21-4.3-4.3a2 2 0 0 1 0-2.8l9.6-9.6a2 2 0 0 1 2.8 0l5.6 5.6a2 2 0 0 1 0 2.8L13 21z"/><path d="M22 21H7"/><path d="m5 11 9 9"/>`,
	"scissors":           `<circle cx="6" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M20 4 8.1 15.9"/><path d="M14.5 14.5 20 20"/><path d="M8.1 8.1 20 4"/>`,
	"wrench":             `<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.8-3.8a6 6 0 0 1-7.9 7.9l-6.9 6.9a2.1 2.1 0 0 1-3-3l6.9-6.9a6 6 0 0 1 7.9-7.9z"/>`,
	"shield":             `<path d="M20 13c0 5-3.5 7.5-7.7 9a1 1 0 0 1-.6 0C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.2-2.7a1.2 1.2 0 0 1 1.6 0C14.5 3.8 17 5 19 5a1 1 0 0 1 1 1z"/>`,
	"filter":             `<path d="M22 3H2l8 9.5V19l4 2v-8.5z"/>`,
	"type":               `<path d="M4 7V4h16v3"/><path d="M9 20h6"/><path d="M12 4v16"/>`,
	"layers":             `<path d="m12 2 10 5-10 5L2 7z"/><path d="m2 12 10 5 10-5"/><path d="m2 17 10 5 10-5"/>`,
	"archive":            `<rect x="2" y="3" width="20" height="5" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>`,
	"sliders-horizontal": `<path d="M21 4h-7M10 4H3M21 12h-9M8 12H3M21 20h-5M12 20H3"/><path d="M14 2v4M8 10v4M16 18v4"/>`,
	"clipboard-list":     `<rect x="8" y="2" width="8" height="4" rx="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><path d="M12 11h4"/><path d="M12 16h4"/><path d="M8 11h.01"/><path d="M8 16h.01"/>`,
	"lock":               `<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>`,
	"braces":             `<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1"/><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1"/>`,
	"triangle-alert":     `<path d="m10.3 3.9-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.7-3l-8-14a2 2 0 0 0-3.4 0z"/><path d="M12 9v4"/><path d="M12 17h.01"/>`,
	"brick-wall":         `<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18"/><path d="M3 15h18"/><path d="M9 3v6"/><path d="M15 3v6"/><path d="M9 15v6"/><path d="M15 15v6"/>`,
	"eye-off":            `<path d="M9.9 9.9a3 3 0 1 0 4.2 4.2"/><path d="M10.7 5.1A10.4 10.4 0 0 1 12 5c7 0 10 7 10 7a13.2 13.2 0 0 1-1.7 2.7"/><path d="M6.6 6.6A13.5 13.5 0 0 0 2 12s3 7 10 7a9.7 9.7 0 0 0 5.4-1.6"/><path d="m2 2 20 20"/>`,
	"cpu":                `<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M15 2v2M15 20v2M9 2v2M9 20v2M2 15h2M2 9h2M20 15h2M20 9h2"/>`,
	"link":               `<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>`,
	"send":               `<path d="m22 2-7 20-4-9-9-4z"/><path d="M22 2 11 13"/>`,
	"bell":               `<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.9 1.9 0 0 0 3.4 0"/>`,
	"arrow-left-right":   `<path d="M8 3 4 7l4 4"/><path d="M4 7h16"/><path d="m16 21 4-4-4-4"/><path d="M20 17H4"/>`,
	"monitor":            `<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8"/><path d="M12 17v4"/>`,
	"cloud":              `<path d="M17.5 19H9a7 7 0 1 1 6.7-9h1.8a4.5 4.5 0 1 1 0 9z"/>`,
	"globe":              `<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>`,
	"languages":          `<path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="M14 18h6"/>`,
	"bar-chart":          `<path d="M3 3v18h18"/><path d="M18 17V9"/><path d="M13 17V5"/><path d="M8 17v-3"/>`,
	"activity":           `<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>`,
	"signal":             `<path d="M2 20h.01"/><path d="M7 20v-4"/><path d="M12 20v-8"/><path d="M17 20V8"/><path d="M22 4v16"/>`,
	"hash":               `<path d="M4 9h16"/><path d="M4 15h16"/><path d="M10 3 8 21"/><path d="M16 3l-2 18"/>`,
	"hard-drive":         `<path d="M22 12H2"/><path d="M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.8 4H7.2a2 2 0 0 0-1.7 1.1z"/><path d="M6 16h.01"/><path d="M10 16h.01"/>`,
	"command":            `<path d="M15 6v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3"/>`,
	"gift":               `<rect x="3" y="8" width="18" height="4" rx="1"/><path d="M12 8v13"/><path d="M19 12v7a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-7"/><path d="M7.5 8a2.5 2.5 0 0 1 0-5C11 3 12 8 12 8s1-5 4.5-5a2.5 2.5 0 0 1 0 5"/>`,
	"repeat":             `<path d="m17 2 4 4-4 4"/><path d="M3 11v-1a4 4 0 0 1 4-4h14"/><path d="m7 22-4-4 4-4"/><path d="M21 13v1a4 4 0 0 1-4 4H3"/>`,
	"timer":              `<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>`,
	"download":           `<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/><path d="M12 15V3"/>`,

	// — the former check/cross list marks —
	"check": `<path d="M20 6 9 17l-5-5"/>`,
	"x":     `<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`,

	// — wiki template chrome (wiki_template.go): the header and sheet
	// controls used to draw themselves with text glyphs (a trigram, a
	// crescent moon and sun with rays, a multiplication x, a midline
	// ellipsis), which are emoji-range characters on every published wiki
	// page. Same inline-SVG mechanism, same 24x24 stroke grid.
	"menu": `<path d="M4 6h16"/><path d="M4 12h16"/><path d="M4 18h16"/>`,
	"moon": `<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>`,
	"sun":  `<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="M4.9 4.9l1.4 1.4"/><path d="M17.7 17.7l1.4 1.4"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="M4.9 19.1l1.4-1.4"/><path d="M17.7 6.3l1.4-1.4"/>`,
	"more": `<path d="M5 12h.01"/><path d="M12 12h.01"/><path d="M19 12h.01"/>`,
}

// Wiki chrome icons, spliced into wiki_template.go the same way ogImageURL
// is: the template is one raw string, so these sit between `+` breaks where
// a text glyph used to sit. wikiSunIcon and wikiMoonIcon additionally travel
// into the theme switcher script as the two paint states.
var (
	wikiMenuIcon  = iconSVG("menu")
	wikiCloseIcon = iconSVG("x")
	wikiMoonIcon  = iconSVG("moon")
	wikiSunIcon   = iconSVG("sun")
	wikiMoreIcon  = iconSVG("more")
)

// iconSVG renders one icon by name. The maps below only name icons this table
// defines; the tests walk every entry so a renamed drawing fails the build
// instead of shipping a silent gap.
func iconSVG(name string) string {
	return iconOpen + iconPaths[name] + "</svg>"
}

// headingIcons maps every current README section id to its inline icon.
var headingIcons = map[string]string{
	"why-9routerx":                                "help-circle",
	"system-architecture":                         "layers",
	"key-features":                                "lightbulb",
	"quick-start":                                 "zap",
	"sharing-a-database-with-9router":             "database",
	"screenshots":                                 "camera",
	"supported-ai-coding-clients":                 "monitor",
	"free-ai-providers--keyless-models-catalog":   "gift",
	"provider-categories-breakdown-155-providers": "globe",
	"embedded-dashboard-overview":                 "monitor",
	"setup-guide-summary--wiki-documentation":     "settings",
	"architecture-comparison":                     "compass",
	"frequently-asked-questions-faq":              "help-circle",
	"contributing":                                "users",
	"docs":                                        "book-open",
	"acknowledgments":                             "heart",
	"license":                                     "file-text",
	"attribution":                                 "users",
}

// listIcons maps bold leads in the current dashboard and contribution lists.
var listIcons = map[string]string{
	`<li><strong>Token Saver &amp; Compression Analytics:</strong>`:      "compress",
	`<li><strong>Connections &amp; 1-Click OAuth Manager:</strong>`:      "key",
	`<li><strong>Per-Key Governance &amp; Custom Usage Ranges:</strong>`: "sliders-horizontal",
	`<li><strong>Bug report</strong>`:                                    "wrench",
	`<li><strong>Feature request</strong>`:                               "lightbulb",
	`<li><strong>Question</strong>`:                                      "help-circle",
}

// linkIcons annotates the existing release and installation references.
var linkIcons = map[string]string{
	`>GitHub Releases</a>`:     "download",
	`>wiki — installation</a>`: "wrench",
}

// The README now has one benefits list and no separate disadvantages list.
const (
	prosListNeedle = `<ul>
<li><strong>Unused Monthly Quotas &amp; Rate-Limit Interruptions → Smart 3-Tier Cascade (<code>Subscription → Cheap → Free</code>):</strong>`
	prosListClass = `<ul class=md-pros>`
)

// injectIcons restores, on the published page, the markers the emoji-free
// markdown sources no longer carry. It runs once per README render on the
// finished HTML, after wrapTables, and is idempotent: every replacement first
// checks that its needle is not already followed by the injected markup, so
// re-running the pipeline over already-injected output is a no-op.
func injectIcons(html string) string {
	html = injectHeadingIcons(html)
	html = injectListIcons(html)
	html = injectLinkIcons(html)
	html = markWhyList(html)
	return html
}

// injectHeadingIcons puts the section icon inside the heading, before its
// text: `<h2 id="quick-start">Quick Start</h2>` renders with the icon inline
// ahead of "Quick Start", where the emoji used to sit.
func injectHeadingIcons(html string) string {
	for id, name := range headingIcons {
		for _, tag := range []string{"h2", "h3"} {
			needle := "<" + tag + ` id="` + id + `">`
			mark := needle + iconSVG(name)
			if !strings.Contains(html, needle) || strings.Contains(html, mark) {
				continue
			}
			html = strings.Replace(html, needle, mark, 1)
		}
	}
	return html
}

// injectListIcons puts each feature's icon between the <li> and its bold
// lead, where the emoji used to sit. Every occurrence is marked: the two
// leads that also open items in the "What's different" list get the same
// icon there, so the two lists stay visually consistent. Idempotent because
// the mark breaks the needle's `<li><strong>` adjacency, so the pattern no
// longer exists in the marked output.
func injectListIcons(html string) string {
	for needle, name := range listIcons {
		mark := "<li>" + iconSVG(name) + needle[4:]
		if strings.Contains(html, mark) {
			continue
		}
		html = strings.ReplaceAll(html, needle, mark)
	}
	return html
}

// injectLinkIcons puts an icon inside the two nav links, ahead of the label.
func injectLinkIcons(html string) string {
	for needle, name := range linkIcons {
		mark := ">" + iconSVG(name) + needle[1:]
		if strings.Contains(html, mark) {
			continue
		}
		html = strings.Replace(html, needle, mark, 1)
	}
	return html
}

// markWhyList annotates only the current benefits list, not unrelated lists.
func markWhyList(html string) string {
	return markOneList(html, prosListNeedle, prosListClass, "check")
}

func markOneList(html, needle, class, markIcon string) string {
	start := strings.Index(html, needle)
	if start < 0 {
		return html
	}
	end := strings.Index(html[start:], "</ul>")
	if end < 0 {
		return html
	}
	block := html[start : start+end]
	// Swap the opening <ul> for the classed one, then give each item its
	// mark. Neither list nests another list, so a flat replace is exact.
	block = strings.Replace(block, "<ul>", class, 1)
	block = strings.ReplaceAll(block, "<li>", "<li>"+iconSVG(markIcon))
	return html[:start] + block + html[start+end:]
}
