// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

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

// pageTemplate is the whole chrome around the rendered README: a masthead with
// the links a visitor to a project page actually reaches for, the article
// itself, and a footer pointing back at the README on GitHub.
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
    <title>9routerx</title>
    <meta
      name="description"
      content="9routerx is a single-binary AI gateway and token saver in Go. Serve OpenAI, Claude and Gemini compatible APIs to your CLI tools with automatic fallback across 40+ providers."
    />
    <link rel="stylesheet" href="style.css" />` + `
    <!-- The real icon files copied from web/public (see copyIcons), in the order
         Google's favicon guidelines ask for. The previous inline SVG data URI
         was never picked up by crawlers — an .ico at the site root is. -->
    <link rel="icon" href="favicon.ico" sizes="32x32" />
    <link rel="icon" type="image/png" href="favicon-32x32.png" />
    <link rel="apple-touch-icon" href="apple-touch-icon.png" />
    <meta property="og:image" content="` + ogImageURL + `" />
  </head>
  <body>
    <a class="skip-link" href="#readme">Skip to content</a>

    <header class="masthead">
      <div class="masthead-inner">
        <a class="wordmark" href="index.html">
          <!-- Brand blue instead of the stylesheet's terracotta: the plate is
               the one place the site's own palette disagrees with the product
               identity (web/public/favicon.ico and the dashboard chrome are
               blue), and style.css is shared with pages this template does not
               render, so the override is inline rather than a stylesheet edit. -->
          <span class="wordmark-mark" aria-hidden="true" style="background:#2079F5">9</span>
          <span>9routerx</span>
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

    <!-- Rendered from README.md by tools/sitegen, so the two can never disagree.
         The HTML passes through unescaped because it is this repository's own
         README: the same file GitHub renders for everyone reading the repo. -->
    <main id="readme" class="prose">{{ .Body }}</main>

    <footer class="colophon">
      <p>
        Wiki:
        <a href="installation.html">Installation</a> ·
        <a href="providers.html">Providers</a> ·
        <a href="token-saver.html">Token Saver</a> ·
        <a href="dashboard.html">Dashboard</a> ·
        <a href="cli.html">CLI</a> ·
        <a href="security.html">Security</a>
      </p>
      <p>
        This page is the
        <a href="{{ blob "README.md" }}">README</a>, generated on every push to
        <code>main</code>. Content licensed
        <a href="{{ blob "LICENSE" }}">MIT</a>.
      </p>
    </footer>
  </body>
</html>
`
