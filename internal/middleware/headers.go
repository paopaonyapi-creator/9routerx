// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"net/http"
	"strings"
)

// SecurityHeaders sets the baseline browser-facing headers on every response
// the gateway emits. Before this middleware existed the survey of a running
// instance showed neither X-Content-Type-Options, X-Frame-Options, CSP nor a
// Referrer-Policy on / or /api/* (audit B-04): a dashboard holding a session
// cookie rendered clickjackable and its API answers were left to MIME
// sniffing. The set is deliberately the conservative, non-breaking one for a
// localhost gateway:
//
//   - X-Content-Type-Options: nosniff — an API error body is never
//     reinterpreted as HTML/JS by a browser that navigates to it.
//   - X-Frame-Options: SAMEORIGIN (+ CSP frame-ancestors) — the SPA embeds no
//     iframes of its own, but same-origin framing stays possible so nothing
//     that legitimately embeds the dashboard breaks.
//   - Referrer-Policy: strict-origin-when-cross-origin — an operator clicking
//     an external link from the dashboard no longer leaks the full local URL
//     (which can carry provider ids) to that site.
//   - Content-Security-Policy — applied to every response. The directives are
//     sized to what the built SPA actually does: scripts only from itself plus
//     the pinned hash of the theme-bootstrap inline script in
//     web/dist/index.html, styles from itself (Svelte injects per-component
//     styles through CSSOM, not inline tags), images and media from itself
//     plus the data:/blob: URLs the downloads and previews build at runtime,
//     and connections only back to the gateway the page came from.
//   - Strict-Transport-Security — only when the request actually arrived over
//     TLS (directly or through a proxy that set x-forwarded-proto=https).
//     Sending HSTS on plain HTTP is ignored by browsers, so it is simply
//     omitted until it can do anything.
const CSPScriptHash = "'sha256-pBeg18co1lmot6fRw3+Vt7tSkHpVPNdT1rJbVN3ir5c='"

// contentSecurityPolicy is one string so the header and its test cannot drift
// apart: the test asserts the pinned script hash still matches the inline
// script embedded in the SPA build.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' " + CSPScriptHash + "; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"media-src 'self' blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'self'"

// requestIsTLS reports whether the request reached the gateway over HTTPS,
// either directly or through a reverse proxy that set x-forwarded-proto.
func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("x-forwarded-proto"), "https")
}

// SecurityHeaders is the middleware that stamps the headers above. It runs
// before the router so 404s, Recoverer panics and every route's answer carry
// the same baseline — a header set only on happy-path handlers would leave the
// probe surface (the NotFound handler answers most scanners) unprotected.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		if requestIsTLS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
