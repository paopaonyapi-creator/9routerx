// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"os"
	"path/filepath"
	"strings"
)

// ogImageURL is the absolute URL of the brand logo, shared by both templates'
// og:image meta. The string is baked into the page markup (the wiki template
// cannot be handed extra data — generateWiki in wiki.go executes it with
// wikiData only), so it lives as one constant here and both templates splice
// it in with a Go constant expression; TestPageTemplateChrome and
// TestWikiTemplateChrome fail if either template ever drifts from it.
//
// Absolute, not relative: og:image is read by crawlers that fetch the image
// out of context — a relative URL would resolve against the crawler's own
// origin, not the /9routerx/ subpath this site is published under.
const ogImageURL = "https://yanstd888.github.io/9routerx/logo-9routerx.png"

// heroStats carries the numbers the homepage hero is allowed to claim. Every
// field is computed from the repository at build time; a field the build could
// not compute is a zero, and the template omits that stat outright — the hero
// never renders a number the build could not back with a file.
type heroStats struct {
	// Version is the VERSION file at the repository root — the same value the
	// release build embeds into the binary.
	Version string
	// Providers counts the connectable entries in the provider catalog
	// (internal/providers/provider_registry.go, the map the dashboard reads
	// through GET /api/providers/catalog), excluding the entries the catalog
	// itself marks hidden.
	Providers int
	// WikiPages counts the pages generateWiki will publish: every wiki/*.md
	// except index.md, whose slug belongs to this homepage.
	WikiPages int
}

// collectHeroStats gathers all three numbers. Paths are relative to the
// repository root because both the Pages workflow and a local render run the
// generator from there. A missing source (test fixtures render from a temp
// dir) degrades to a zero, never an error: the hero renders without that stat
// rather than inventing one.
func collectHeroStats(wikiDir string) heroStats {
	return heroStats{
		Version:   readVersion(),
		Providers: countProviders(),
		WikiPages: countWikiPages(wikiDir),
	}
}

// readVersion reads the repository's VERSION file — the value `make build`
// embeds and the releases tag.
func readVersion() string {
	data, err := os.ReadFile("VERSION")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// countProviders counts the connectable entries in the generated provider
// registry. The registry is a single Go map literal (provider_registry.go is
// machine-generated from the upstream registry — "DO NOT EDIT"), so a line
// scan is exact: top-level keys are the only lines indented one tab and shaped
// `"id": {`, and an entry is hidden when its own ProviderMeta sets
// `Hidden: true`. Hidden entries are excluded because the dashboard's catalog
// never offers them — counting them would overstate what a visitor can
// actually connect.
func countProviders() int {
	data, err := os.ReadFile(filepath.Join("internal", "providers", "provider_registry.go"))
	if err != nil {
		return 0
	}
	return countProvidersIn(data)
}

// countProvidersIn is the pure line-scan countProviders runs, split out so the
// scan itself is unit-tested against a fixture rather than against the live
// registry (whose size legitimately changes as providers are added).
func countProvidersIn(data []byte) int {
	total, hidden := 0, 0
	seen := false
	curHidden := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "\t\"") && strings.HasSuffix(trimmed, ": {") {
			if seen && curHidden {
				hidden++
			}
			seen = true
			curHidden = false
			total++
			continue
		}
		// Match the field name exactly, so HiddenKinds never counts as Hidden.
		if name, value, ok := cutYAMLishField(trimmed); ok && name == "Hidden" {
			curHidden = value == "true"
		}
	}
	if seen && curHidden {
		hidden++
	}
	return total - hidden
}

// cutYAMLishField splits a struct-field line like `Hidden:       true,` into
// its name and value.
func cutYAMLishField(line string) (name, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	value = strings.TrimSpace(line[idx+1:])
	value = strings.TrimSuffix(value, ",")
	return strings.TrimSpace(line[:idx]), value, true
}

// countWikiPages mirrors generateWiki's published set: every wiki/*.md except
// index.md (see the ordering comment in main.go).
func countWikiPages(wikiDir string) int {
	entries, err := filepath.Glob(filepath.Join(wikiDir, "*.md"))
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if strings.EqualFold(filepath.Base(entry), "index.md") {
			continue
		}
		count++
	}
	return count
}

// pageTemplate is the whole chrome around the rendered README: a masthead with
// the real brand mark, the hero + landing sections the homepage exists for,
// the full README (the Pages contract: the project page is the README), and a
// footer pointing back at the repository.
//
// The landing copy is this repository's own — feature names, commands and FAQ
// answers come from README.md and the wiki, never invented here.
//
// Paths are relative because the site is served from the /9routerx/ project
// subpath, where a root-absolute /style.css would 404. The favicon paths are
// no exception: the icon files are copied into the site root by copyIcons, so
// a bare favicon.ico resolves on every page the same way style.css does.
const pageTemplate = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>9RouterX — Local AI gateway &amp; token saver</title>
    <meta
      name="description"
      content="9RouterX is a single-binary AI gateway and token saver in Go. Serve OpenAI, Claude and Gemini compatible APIs to your CLI tools with automatic fallback across 40+ providers."
    />
    <!-- The dashboard's glyph pairing (web/src/index.css): Manrope for display
         and body, Geist Mono for code and technical labels. Google Fonts
         serves the same variable-font weight ranges the dashboard vendors
         (Manrope 200-800, Geist Mono 100-900); the CSS stacks fall back to
         Inter and system faces if the fonts never arrive. -->
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link
      rel="stylesheet"
      href="https://fonts.googleapis.com/css2?family=Geist+Mono:wght@100..900&family=Manrope:wght@200..800&display=swap"
    />
    <link rel="stylesheet" href="style.css" />` + `
    <!-- Icon sizing for the svg.ico markers injected by icons.go. -->
    <style>
      .prose svg.ico {
        fill: none;
        stroke: currentColor;
        stroke-width: 2;
        stroke-linecap: round;
        stroke-linejoin: round;
        width: 1em;
        height: 1em;
        vertical-align: -0.15em;
      }
      .prose h2 svg.ico,
      .prose h3 svg.ico {
        width: 1.15em;
        height: 1.15em;
        vertical-align: -0.18em;
        margin-right: 0.45em;
      }
      .prose a svg.ico {
        vertical-align: -0.12em;
        margin-right: 0.3em;
      }
      .prose li svg.ico {
        margin-right: 0.4em;
      }
      .prose ul.md-pros,
      .prose ul.md-cons {
        list-style: none;
        padding-left: 1.5rem;
      }
      .prose ul.md-pros svg.ico {
        color: var(--ok);
      }
      .prose ul.md-cons svg.ico {
        color: var(--err);
      }
    </style>` + `
    <!-- The real icon files copied from web/public (see copyIcons), in the order
         Google's favicon guidelines ask for. The previous inline SVG data URI
         was never picked up by crawlers — an .ico at the site root is. -->
    <link rel="icon" href="favicon.ico" sizes="32x32" />
    <link rel="icon" type="image/png" href="favicon-32x32.png" />
    <link rel="apple-touch-icon" href="apple-touch-icon.png" />
    <meta property="og:image" content="` + ogImageURL + `" />
  <script type="application/ld+json">
  {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    "name": "9RouterX",
    "applicationCategory": "DeveloperApplication",
    "operatingSystem": "Linux, macOS, Windows, FreeBSD",
    "description": "Single-binary Go AI router, local LLM gateway and token saver. Route Claude Code, Cursor, Codex and other AI coding agents across 155 providers, 28 OAuth integrations and 248 free models (24 keyless) with 20-40% token savings.",
    "url": "https://yanstd888.github.io/9routerx/",
    "downloadUrl": "https://github.com/yanstd888/9routerx/releases",
    "softwareVersion": "1.1.3",
    "author": { "@type": "Person", "name": "Muhammad Dian", "url": "https://yanstd888.github.io/" },
    "offers": { "@type": "Offer", "price": "0", "priceCurrency": "USD" }
  }
  </script>
  </head>
  <body>
    <a class="skip-link" href="#main">Skip to content</a>

    <header class="masthead">
      <div class="masthead-inner">
        <a class="wordmark" href="index.html">
          <!-- The real logo (site/logo-9routerx-128.png, a 128px resize of
               web/public/logo-9routerx.png) inside a solid dark tile. The
               asset's background is transparent, and a floating transparent
               flame on the pale light theme would have no edge — the dashboard
               presents the same mark on a dark panel, so the masthead does
               too. The tile stays dark in BOTH themes. -->
          <span class="wordmark-mark" aria-hidden="true"
            ><img src="logo-9routerx-128.png" alt="9RouterX — Local AI Gateway and Token Saver Logo" width="128" height="128" loading="lazy" decoding="async"
          /></span>
          <span>9RouterX</span>
        </a>
        <nav aria-label="Project">
          <!-- The wiki is 20 pages of documentation and this page is the only
               door to it, so it leads the nav. It points at installation.html
               rather than a wiki index: wiki/index.md is unpublished (its slug
               would collide with this page's index.html), and Installation is
               the first published page in the wiki's own sidebar order — every
               wiki page carries the full sidebar, so it doubles as the table
               of contents. -->
          <a href="installation.html">Wiki</a>
          <a href="https://github.com/{{ .Repo }}">Repository</a>
          <a href="https://github.com/{{ .Repo }}/releases">Releases</a>
          <a href="tg">Telegram group</a>
        </nav>
      </div>
    </header>

    <main id="main">
      <!-- The hero: sharp one-line tagline, subheadline, install block, chip
           strip, live numbers, and the real dashboard screenshot. The
           background reuses the dashboard's routing-map underlay (gridline +
           slow accent auras, CSS only); the headline decodes with the same
           text-scramble the dashboard's login headline runs
           (web/src/lib/scramble.ts). Everything respects
           prefers-reduced-motion. -->
      <section class="hero" aria-label="9RouterX at a glance">
        <div class="hero-inner">
          <p class="hero-eyebrow">Local AI gateway &middot; single binary &middot; Go + Svelte</p>
          <h1 id="hero-title">9RouterX — One Endpoint. Every AI Model. Tokens Saved Automatically.</h1>
          <p class="hero-sub">
            9RouterX is a local AI gateway and LLM router in <strong>one Go binary</strong>. Point
            Claude Code, Codex, Cursor, or any OpenAI- or Claude-compatible CLI at one endpoint and
            get automatic fallback across subscription, cheap and free tiers &mdash; with a
            token-saver suite that cuts tool-output tokens by 20&ndash;40% and the management
            dashboard embedded.
          </p>

          <!-- The primary element: the README's own install line, typed once on
               load and wired to a copy button. The full command is in the
               markup, so no-JS readers and search engines see it either way.
               npm is the primary path everywhere (README, wiki, this landing):
               the launcher fetches the right platform binary by itself; the
               script installers and Docker stay as alternatives in the
               quickstart below. -->
          <div class="hero-install">
            <div class="install-head">
              <span class="install-label">Install &mdash; npm (any OS)</span>
              <button class="install-copy" id="copy-install" type="button">Copy</button>
            </div>
            <pre><code id="install-cmd" data-cmd="npm install -g 9routerx">npm install -g 9routerx</code><span class="install-caret" aria-hidden="true"></span></pre>
          </div>

          <p class="hero-actions">
            <a class="btn btn-primary" href="https://github.com/{{ .Repo }}/releases/latest"
              >GitHub Release</a
            >
            <a class="btn btn-ghost" href="installation.html">Documentation</a>
          </p>

          <!-- Feature chips, from the README's own feature names. -->
          <ul class="hero-chips">
            <li>Token Saver</li>
            <li>Semantic Cache</li>
            <li>GCF Compression</li>
            <li>Guardrails</li>
            <li>Multi-provider Routing</li>
            <li>MCP / A2A</li>
          </ul>

          <!-- Live numbers, only when the build computed them. -->
          <dl class="hero-stats">
            {{ if .Stats.Providers }}
            <div class="hero-stat">
              <dd>{{ .Stats.Providers }}</dd>
              <dt>providers in the catalog</dt>
            </div>
            {{ end }} {{ if .Stats.WikiPages }}
            <div class="hero-stat">
              <dd>{{ .Stats.WikiPages }}</dd>
              <dt>wiki pages of docs</dt>
            </div>
            {{ end }} {{ if .Stats.Version }}
            <div class="hero-stat">
              <dd>v{{ .Stats.Version }}</dd>
              <dt>version</dt>
            </div>
            {{ end }}
          </dl>

          <!-- The real Providers screenshot from docs/screenshots (the same
               capture the README gallery ships), in a framed panel. Lazy and
               sized, like every image on this site. -->
          <figure class="hero-shot">
            <img
              src="docs/screenshots/providers.png"
              alt="Providers page of the 9RouterX dashboard in dark theme — the provider catalog and connection list"
              width="1280"
              height="800"
              loading="lazy"
              decoding="async"
            />
            <figcaption>
              The Providers page &mdash; the dashboard embedded in the same binary you just installed.
            </figcaption>
          </figure>
        </div>
      </section>

      <!-- Landing sections. Every claim, command and answer below is
           README/wiki text, condensed but not invented. -->
      <div class="landing">
        <section aria-labelledby="features-title">
          <p class="landing-kicker">What you get</p>
          <h2 id="features-title">Four systems between your CLI and the bill</h2>
          <p class="landing-lede">
            The condensed version of the feature list &mdash; the README below carries the full
            inventory, and the wiki documents every knob.
          </p>
          <div class="feature-grid">
            <article class="feature-card">
              <span class="feature-num">01</span>
              <h3>RTK Token Saver</h3>
              <p>
                Auto-compresses <code>tool_result</code> content &mdash; git diffs, grep output,
                file listings &mdash; before it reaches the model: 20&ndash;40% fewer tokens on
                agentic workloads. Anti-slop injector, context squeezer, per-tool output filters
                and JSON guard ride along.
              </p>
            </article>
            <article class="feature-card">
              <span class="feature-num">02</span>
              <h3>Semantic cache + prompt cache</h3>
              <p>
                Repeated identical requests are served from cache instead of the provider, cutting
                cost and latency for multi-agent loops and CLI re-runs. Semantic hits are
                attributed in usage analytics; the prompt cache is a bounded LRU on the exact
                prompt.
              </p>
            </article>
            <article class="feature-card">
              <span class="feature-num">03</span>
              <h3>Guardrails &amp; per-key governance</h3>
              <p>
                Daily cost ceilings with hard cutoff, request limits, IP whitelists, per-key system
                prompts and endpoint permissions &mdash; enforced per API key, with a security
                event log, SSRF guard and audit log v2 behind them.
              </p>
            </article>
            <article class="feature-card">
              <span class="feature-num">04</span>
              <h3>Multi-provider routing</h3>
              <p>
                A 3-tier circuit breaker across the subscription &rarr; cheap &rarr; free chain,
                with per-combo strategies: fallback, round-robin, sticky, capacity, fusion,
                cache-optimized, auto, quota-weighted and reset-aware.
              </p>
            </article>
          </div>
        </section>

        <section aria-labelledby="quickstart-title">
          <p class="landing-kicker">Quickstart</p>
          <h2 id="quickstart-title">Install, run, point your CLI</h2>
          <p class="landing-lede">
            Defaults are enough for most people &mdash; port <code>20128</code>, data in
            <code>~/.9router</code>, no flags needed.
          </p>
          <ol class="steps">
            <li class="step">
              <h3>Install</h3>
              <p>
                One line, any OS &mdash; npm installs the right binary for your platform
                automatically (Windows, macOS Intel/Apple Silicon, Linux x64/ARM64). Requires
                Node.js 18+; the gateway binary itself has no runtime dependencies.
              </p>
              <div class="codeblock">
                <div class="install-head">
                  <span class="install-label">npm &mdash; any OS</span>
                </div>
                <pre><code>npm install -g 9routerx</code></pre>
              </div>
              <div class="codeblock">
                <div class="install-head">
                  <span class="install-label">alternative &mdash; script installer (same binaries)</span>
                </div>
                <pre><code>curl -fsSL https://raw.githubusercontent.com/yanstd888/9routerx/main/install.sh | bash</code></pre>
              </div>
              <div class="codeblock">
                <div class="install-head">
                  <span class="install-label">alternative &mdash; container</span>
                </div>
                <pre><code>docker run -d --name 9routerx --restart unless-stopped \
  -p 20128:20128 -v "$HOME/.9router:/data" \
  -e PORT=20128 -e DATA_DIR=/data \
  ghcr.io/yanstd888/9routerx:latest</code></pre>
              </div>
            </li>
            <li class="step">
              <h3>Run it</h3>
              <p>
                Then open the dashboard at <code>http://localhost:20128</code> &mdash; the default
                local password is <code>123456</code>; change it in Settings &rarr; Profile.
              </p>
              <div class="codeblock">
                <div class="install-head">
                  <span class="install-label">shell</span>
                </div>
                <pre><code>9routerx
# Dashboard: http://localhost:20128 (Default password: 123456)</code></pre>
              </div>
            </li>
            <li class="step">
              <h3>Point your CLI at it</h3>
              <p>
                Anything that speaks OpenAI Chat Completions, Claude Messages, Responses, or
                Ollama-compatible chat. The
                <a href="client-setup.html">wiki's client setup page</a> has per-tool snippets.
                Create a local key in Settings &rarr; API Keys &rarr; Create Key first:
                with 0 keys, requests return <code>401 Authentication required</code>.
              </p>
              <div class="codeblock">
                <div class="install-head">
                  <span class="install-label">client settings</span>
                </div>
                <pre><code>Endpoint: http://localhost:20128/v1
API Key:  [Settings &rarr; API Keys &rarr; Create Key]
Model:    antigravity/gemini-3.7-flash-medium
# Antigravity: sign in with Google once (free)
# Zero-login alternative: oc/big-pickle (OpenCode Free)</code></pre>
              </div>
            </li>
          </ol>
        </section>

        <section aria-labelledby="faq-title">
          <p class="landing-kicker">FAQ</p>
          <h2 id="faq-title">Questions the docs answer most</h2>
          <p class="landing-lede">
            The README's own FAQ &mdash; every answer below is documented in full there and in the
            wiki.
          </p>
          <div class="faq-list">
            <details>
              <summary>Is 9RouterX really free?</summary>
              <p>
                Yes &mdash; MIT-licensed and free to run. The gateway itself costs nothing; you
                bring your own provider accounts, and its whole design pushes traffic toward the
                free and cheap tiers (the free-tier catalog, the circuit breaker, and per-key
                budget ceilings all serve that goal).
              </p>
            </details>
            <details>
              <summary>Does it work with Claude Code, Codex, Cursor, or Cline?</summary>
              <p>
                Yes &mdash; anything that speaks the OpenAI Chat Completions API, the Claude
                Messages API, the Responses API, or Ollama-compatible chat points at
                <code>http://localhost:20128/v1</code> with an API key minted in the dashboard. The
                wiki's <a href="client-setup.html">client setup page</a> has per-tool snippets.
              </p>
            </details>
            <details>
              <summary>How is this different from running Ollama?</summary>
              <p>
                Ollama serves local models. 9RouterX is a router in front of cloud and subscription
                providers &mdash; it fails over between your subscription quota, cheap paid models,
                and free tiers, and adds token compression, caching, and per-key governance on
                top. The two compose fine: an Ollama endpoint can be one of the providers.
              </p>
            </details>
            <details>
              <summary>Do I have to migrate my data from 9Router?</summary>
              <p>
                No. Point 9routerx at the same <code>DATA_DIR</code> and it opens the same SQLite
                database &mdash; providers, combos, keys, and history carry over as-is. There is
                no import step and no migration.
              </p>
            </details>
            <details>
              <summary>Can I limit what a shared API key can do?</summary>
              <p>
                Yes &mdash; that is the per-key governance suite: daily cost ceilings with hard
                cutoff, request limits, IP whitelists, per-key system prompts, and endpoint
                permissions, optionally templated via plans and stamped across many keys.
              </p>
            </details>
          </div>
        </section>
      </div>

      <!-- The Pages contract: the README is the homepage. The hero and the
           landing sections are chrome ABOVE it; the README itself renders
           complete, exactly as GitHub renders it for the repository. -->
      <div class="readme-head">
        <p class="landing-kicker">Full README</p>
        <p>
          Rendered from <a href="{{ blob "README.md" }}">README.md</a> on every push to
          <code>main</code>
        </p>
      </div>

      <!-- Rendered from README.md by tools/sitegen, so the two can never disagree.
           The HTML passes through unescaped because it is this repository's own
           README: the same file GitHub renders for everyone reading the repo. -->
      <article id="readme" class="prose">{{ .Body }}</article>
    </main>

    <footer class="colophon">
      <ul class="colophon-links">
        <li><a href="installation.html">Wiki</a></li>
        <li><a href="providers.html">Providers</a></li>
        <li><a href="token-saver.html">Token Saver</a></li>
        <li><a href="dashboard.html">Dashboard</a></li>
        <li><a href="cli.html">CLI</a></li>
        <li><a href="security.html">Security</a></li>
        <li><a href="https://github.com/{{ .Repo }}">Repository</a></li>
        <li><a href="https://github.com/{{ .Repo }}/releases">Releases</a></li>
        <li><a href="{{ blob "LICENSE" }}">License: MIT</a></li>
        <li><a href="tg">Telegram group</a></li>
        <li><a href="{{ blob "ATTRIBUTION.md" }}">Attribution</a></li>
      </ul>
      <p class="attribution">
        9RouterX &mdash; a Go + Svelte source port by
        <a href="https://github.com/yanstd888/">yanstd888</a>
        (<a href="tg">t.me/aiclub_id</a>), continuing
        <a href="https://github.com/decolua/9router">9Router</a> and
        <a href="https://github.com/luqman-v1/9router-go">9router-go</a>; the full ownership
        inventory lives in <a href="{{ blob "ATTRIBUTION.md" }}">ATTRIBUTION.md</a>. Content
        licensed MIT.
      </p>
    </footer>

    <script>
      // Hero behaviour: headline decode, install-command typing, copy button.
      // Small, inline, dependency-free — and every effect stands down under
      // prefers-reduced-motion.
      (function () {
        var reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

        // 1) Headline text-scramble / decode. Same glyph set, same <=500ms
        //    window and same timer-based settle net as the dashboard's
        //    web/src/lib/scramble.ts: letters cycle random mono glyphs and
        //    settle left to right, one shot, never looping. The settle timer
        //    guarantees the authored text even if rAF pauses (hidden tab).
        var GLYPHS = "x%@o8s#>";
        var title = document.getElementById("hero-title");
        if (title && !reduced) {
          var original = title.textContent;
          var duration = 280 + Math.random() * 200;
          var start = performance.now();
          var raf = 0;
          var settle = function () {
            if (raf) cancelAnimationFrame(raf);
            raf = 0;
            title.textContent = original;
          };
          var settleTimer = setTimeout(settle, duration + 120);
          var step = function (now) {
            var progress = Math.min(1, (now - start) / duration);
            var out = "";
            for (var i = 0; i < original.length; i++) {
              var ch = original[i];
              if (ch === " " || Math.random() < progress * progress) {
                out += ch;
              } else {
                out += GLYPHS[(Math.random() * GLYPHS.length) | 0];
              }
            }
            title.textContent = out;
            if (progress < 1) {
              raf = requestAnimationFrame(step);
            } else {
              clearTimeout(settleTimer);
              settle();
            }
          };
          raf = requestAnimationFrame(step);
        }

        // 2) Install command: typed once, fast (~500ms). The markup already
        //    holds the full command, so the effect only ever ADDS motion —
        //    no-JS readers, search engines and reduced-motion readers all see
        //    the command without running a line of script.
        var cmd = document.getElementById("install-cmd");
        if (cmd && !reduced) {
          var full = cmd.getAttribute("data-cmd") || cmd.textContent;
          var typed = 0;
          cmd.textContent = "";
          var typeTimer = setInterval(function () {
            typed += 2;
            if (typed >= full.length) {
              cmd.textContent = full;
              clearInterval(typeTimer);
            } else {
              cmd.textContent = full.slice(0, typed);
            }
          }, 24);
        }

        // 3) Copy button. Clipboard API on the secure Pages origin, with an
        //    execCommand fallback; feedback lands on the button itself so the
        //    success never needs a toast.
        var copyBtn = document.getElementById("copy-install");
        if (copyBtn && cmd) {
          copyBtn.addEventListener("click", function () {
            var text = cmd.getAttribute("data-cmd") || cmd.textContent;
            var done = function (ok) {
              copyBtn.textContent = ok ? "Copied" : "Copy failed";
              setTimeout(function () {
                copyBtn.textContent = "Copy";
              }, 1600);
            };
            if (navigator.clipboard && navigator.clipboard.writeText) {
              navigator.clipboard.writeText(text).then(
                function () {
                  done(true);
                },
                function () {
                  done(false);
                }
              );
            } else {
              var ta = document.createElement("textarea");
              ta.value = text;
              document.body.appendChild(ta);
              ta.select();
              var ok = false;
              try {
                ok = document.execCommand("copy");
              } catch (e) {
                ok = false;
              }
              document.body.removeChild(ta);
              done(ok);
            }
          });
        }
      })();
    </script>
  </body>
</html>
`
