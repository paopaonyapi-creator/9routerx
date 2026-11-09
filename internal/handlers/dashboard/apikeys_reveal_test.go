// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9routerx/proxy/internal/keikey"
)

// createTestKey mints a key through the handler and returns its id and the
// plaintext the create call disclosed.
func createTestKey(t *testing.T, router http.Handler, name string) (id, full string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name})
	req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create apiKey expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	id, _ = created["id"].(string)
	full, _ = created["key"].(string)
	if id == "" || full == "" {
		t.Fatalf("create returned id=%q key=%q", id, full)
	}
	return id, full
}

// listKeys reads GET /api/keys and returns the single row, failing when the
// list is not exactly one key — an absent key would make the assertions below
// pass for the wrong reason.
func listKeys(t *testing.T, router http.Handler) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list keys: status %d: %s", rec.Code, rec.Body.String())
	}
	var listed []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected exactly one key, got %v", listed)
	}
	return listed[0]
}

// TestApiKeySecretShownOnceAtCreation is the hash-only contract: the create
// response is the only place the secret appears. The row stores a verifier and
// a mask, so a later listing shows the mask and marks the row unrevealable.
func TestApiKeySecretShownOnceAtCreation(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// requireLogin is on by default, so with no session cookie this caller is
	// anonymous. Disabling it models the default local install — loopback, no
	// login — which is the one setup that must still manage its own keys.
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}

	id, full := createTestKey(t, router, "Readable Key")

	row := listKeys(t, router)
	if got, _ := row["key"].(string); got != "" {
		t.Errorf("key = %q, want empty after creation", got)
	}
	if revealable, _ := row["revealable"].(bool); revealable {
		t.Error("revealable = true for a hash-only key, want false")
	}
	// keyDisplay stays masked: it is what the table renders in place of a
	// secret it can no longer read.
	masked, _ := row["keyDisplay"].(string)
	if masked == "" || masked == full {
		t.Errorf("keyDisplay = %q, want a masked value distinct from the secret", masked)
	}
	if strings.Contains(masked, full) {
		t.Errorf("keyDisplay %q contains the full secret", masked)
	}

	// The stored row must not hold the plaintext in any column.
	stored, err := repo.GetApiKeyByID(id)
	if err != nil || stored == nil {
		t.Fatalf("read stored row: %v", err)
	}
	if stored.Key == full || strings.Contains(stored.Key, full) {
		t.Error("the plaintext secret is stored in the key column")
	}
	if stored.KeyHash == nil || *stored.KeyHash == "" {
		t.Error("no argon2id verifier stored for the issued key")
	}
}

// TestLegacyPlaintextKeyStaysRevealable is the transition half: a row written
// while #199 kept the plaintext still reveals to a dashboard credential, so an
// operator can migrate a client before that row is rotated.
func TestLegacyPlaintextKeyStaysRevealable(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}

	const legacy = "sk-legacy-plaintext-secret-value"
	if err := repo.CreateApiKey("legacy-row", legacy, "Legacy", ""); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	row := listKeys(t, router)
	if got, _ := row["key"].(string); got != legacy {
		t.Errorf("legacy key = %q, want the stored plaintext", got)
	}
	if revealable, _ := row["revealable"].(bool); !revealable {
		t.Error("revealable = false for a legacy row, want true")
	}
}

// TestApiKeySecretHiddenFromApiKeyCallers is the half of #199 that must not
// regress. An engine client key is handed to a client app, so it still gets
// only the masked value — otherwise one leaked key dumps every other key,
// which is exactly what F-6 was merged to prevent.
func TestApiKeySecretHiddenFromApiKeyCallers(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatalf("set requireLogin: %v", err)
	}
	router := setupTestRouter(repo)

	otherID, otherSecret := createTestKey(t, router, "Other Key")

	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	req.Header.Set("Authorization", "Bearer "+otherSecret)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("api-key caller: status %d: %s", rec.Code, rec.Body.String())
	}

	var listed []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}
	var found map[string]any
	for _, row := range listed {
		if id, _ := row["id"].(string); id == otherID {
			found = row
			break
		}
	}
	if found == nil {
		t.Fatalf("key %s absent from the list an api-key caller received", otherID)
	}
	if strings.Contains(rec.Body.String(), otherSecret) {
		t.Errorf("GET /api/keys leaked the full secret to an api-key caller: %s", rec.Body.String())
	}
	if got, _ := found["key"].(string); got != "" {
		t.Errorf("key = %q for an api-key caller, want empty", got)
	}
	if got, _ := found["keyDisplay"].(string); got == "" {
		t.Error("keyDisplay is empty for an api-key caller; the table would render nothing")
	}
}

// TestRotateApiKeyHandsBackSecretOnce covers the second issuance path: rotate
// mints a replacement, hands it back in the response, and the row it replaced
// is listed with the new mask and no reveal control.
func TestRotateApiKeyHandsBackSecretOnce(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatalf("disable requireLogin: %v", err)
	}

	id, first := createTestKey(t, router, "Rotated Key")
	req := httptest.NewRequest(http.MethodPost, "/api/keys/"+id+"/rotate", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: status %d: %s", rec.Code, rec.Body.String())
	}
	var rotated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("unmarshal rotate response: %v", err)
	}
	fresh, _ := rotated["key"].(string)
	if fresh == "" || fresh == first {
		t.Fatalf("rotate returned key %q, want a new secret", fresh)
	}
	if revealable, _ := rotated["revealable"].(bool); revealable {
		t.Error("rotate reported the replacement as revealable")
	}

	row := listKeys(t, router)
	if got, _ := row["key"].(string); got != "" {
		t.Errorf("listed key = %q, want empty", got)
	}
	if revealable, _ := row["revealable"].(bool); revealable {
		t.Error("listed row is revealable after rotation, want false")
	}

	// The replacement is the credential the engine must accept; the old one is
	// gone from the row entirely.
	stored, err := repo.GetApiKeyByID(id)
	if err != nil || stored == nil {
		t.Fatalf("read stored row: %v", err)
	}
	if stored.KeyHash == nil || !keikey.Verify(fresh, *stored.KeyHash) {
		t.Fatal("the rotated secret does not verify against the stored row")
	}
	if keikey.Verify(first, *stored.KeyHash) {
		t.Fatal("the previous secret still verifies against the stored row")
	}
}

// TestApiKeyFromBeforeHashingIsNotFabricated covers the rows #199 cannot help:
// created under F-6, their plaintext is gone. The list must say so with an
// empty key rather than return the sentinel that now occupies the column,
// which would otherwise render as a bogus token the operator could copy.
func TestApiKeyFromBeforeHashingIsNotFabricated(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	createTestKey(t, router, "Modern Key")
	if err := repo.CreateApiKey("legacy-row", "sk-legacy-secret-value", "Legacy", ""); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	// Convert one row into the F-6 shape, exactly as the old create path did.
	if err := repo.HashApiKeyRow("legacy-row", "verifier", "legacy-lookup", "sk-leg…alue"); err != nil {
		t.Fatalf("HashApiKeyRow: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var listed []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}
	for _, row := range listed {
		id, _ := row["id"].(string)
		if id != "legacy-row" {
			continue
		}
		if got, _ := row["key"].(string); got != "" {
			t.Errorf("hashed row key = %q, want empty (the plaintext is unrecoverable)", got)
		}
		if got, _ := row["keyDisplay"].(string); got != "sk-leg…alue" {
			t.Errorf("hashed row keyDisplay = %q, want the stored mask", got)
		}
		return
	}
	t.Fatal("legacy row absent from the list")
}
