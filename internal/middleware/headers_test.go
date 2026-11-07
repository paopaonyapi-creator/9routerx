// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"9routerx/proxy/web"
)

func TestSecurityHeadersPresent(t *testing.T) {
	handler := SecurityHeaders(http.NotFoundHandler())
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("Content-Security-Policy = %q, want the pinned policy", got)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q on a plain HTTP request, want absent", got)
	}
}

func TestSecurityHeadersHSTSOnTLS(t *testing.T) {
	handler := SecurityHeaders(http.NotFoundHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("Strict-Transport-Security missing on x-forwarded-proto=https request")
	}

	// Plain HTTP through the same middleware still must not send HSTS.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q on plain HTTP, want absent", got)
	}
}

// TestCSPScriptHashMatchesEmbeddedIndex pins the CSP script hash to the bytes
// of the theme-bootstrap inline script that the SPA build embeds. The hash is
// what lets the CSP keep script-src free of 'unsafe-inline' while the index
// page still runs its theme loader. A rebuilt index.html whose inline script
// changed would silently lose that script (dark mode would break) without
// this test failing — so the hash must be updated together with the page.
func TestCSPScriptHashMatchesEmbeddedIndex(t *testing.T) {
	raw, err := fs.ReadFile(web.DistFS, "dist/index.html")
	if err != nil {
		t.Fatalf("read embedded dist/index.html: %v", err)
	}
	re := regexp.MustCompile(`(?s)<script>(.*?)</script>`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatal("embedded dist/index.html has no plain <script> block; update CSPScriptHash if the inline script moved")
	}
	sum := sha256.Sum256(m[1])
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if hash != CSPScriptHash {
		t.Errorf("CSPScriptHash %s does not match the embedded inline script hash %s — update CSPScriptHash in headers.go", CSPScriptHash, hash)
	}
}
