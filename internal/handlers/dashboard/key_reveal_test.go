// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/log"
)

func assertRevealHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for name, want := range map[string]string{
		"Cache-Control":          "no-store, no-cache, must-revalidate",
		"Pragma":                 "no-cache",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Result().Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func revealLogs() []string {
	var lines []string
	for _, entry := range log.ConsoleEntries() {
		if strings.Contains(entry.Line, "apikeys reveal") {
			lines = append(lines, entry.Line)
		}
	}
	return lines
}

func TestKeyRevealHeadersAndLogs(t *testing.T) {
	level := log.GetLevel()
	log.SetLevel(log.LevelInfo)
	t.Cleanup(func() { log.SetLevel(level); log.ClearConsoleLogs() })
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatal(err)
	}
	id, secret := createTestKey(t, router, "audit-test-key")
	log.ClearConsoleLogs()
	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.RemoteAddr = "127.0.0.1:23456"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assertRevealHeaders(t, rec)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), secret) {
		t.Fatal("privileged list no longer reveals its stored key")
	}
	lines := revealLogs()
	if len(lines) != 1 {
		t.Fatalf("reveal log count = %d, want 1", len(lines))
	}
	if strings.Contains(lines[0], secret) || strings.Contains(lines[0], id) {
		t.Fatal("reveal log contains plaintext secret or unmasked ID")
	}
	if !strings.Contains(lines[0], "127.0.0.1:23456") || !strings.Contains(lines[0], handlerutil.RunePrefix(id, 4)) {
		t.Fatal("reveal log omits masked ID or remote address")
	}

	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatal(err)
	}
	log.ClearConsoleLogs()
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assertRevealHeaders(t, rec)
	if strings.Contains(rec.Body.String(), secret) || len(revealLogs()) != 0 {
		t.Fatal("redacted list disclosed or logged a secret reveal")
	}
}

func TestProviderExportRevealHeadersAndLogs(t *testing.T) {
	level := log.GetLevel()
	log.SetLevel(log.LevelInfo)
	t.Cleanup(func() { log.SetLevel(level); log.ClearConsoleLogs() })
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatal(err)
	}
	priority := 1
	if err := repo.CreateProviderConnectionFull("audit-test-connection", "audit-test-provider", "apikey", "audit-test", &priority, `{"apiKey":"audit-test-credential"}`); err != nil {
		t.Fatal(err)
	}
	router := setupTestRouter(repo)
	log.ClearConsoleLogs()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/audit-test-provider/export-keys", nil))
	assertRevealHeaders(t, rec)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "audit-test-credential") {
		t.Fatal("provider export changed its credential response")
	}
	lines := revealLogs()
	if len(lines) != 1 || strings.Contains(lines[0], "audit-test-credential") || strings.Contains(lines[0], "audit-test-connection") {
		t.Fatal("provider reveal logging contains secrets, full ID, or wrong count")
	}
	log.ClearConsoleLogs()
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/audit-empty-provider/export-keys", nil))
	assertRevealHeaders(t, rec)
	if len(revealLogs()) != 0 {
		t.Fatal("empty export produced a reveal log")
	}
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/audit-test-provider/export-keys", nil))
	assertRevealHeaders(t, rec)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unprivileged export = %d, want 401", rec.Code)
	}
}
