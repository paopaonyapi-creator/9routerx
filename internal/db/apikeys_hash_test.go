// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package db

import (
	"strings"
	"testing"
)

func TestFindApiKeyByLookup(t *testing.T) {
	repo, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	if err := repo.CreateApiKey("k1", "sk-legacy-plaintext", "one", "m1"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := repo.HashApiKeyRow("k1", "$argon2id$verifier", "lookup-hash-1", "sk-l…text"); err != nil {
		t.Fatalf("HashApiKeyRow: %v", err)
	}

	got, err := repo.FindApiKeyByLookup("lookup-hash-1")
	if err != nil {
		t.Fatalf("FindApiKeyByLookup: %v", err)
	}
	if got == nil {
		t.Fatal("FindApiKeyByLookup returned nil for a stored lookup hash")
	}
	if got.ID != "k1" {
		t.Fatalf("FindApiKeyByLookup returned row %q, want k1", got.ID)
	}
	if got.KeyHash == nil || *got.KeyHash != "$argon2id$verifier" {
		t.Fatalf("KeyHash = %v, want the stored verifier", got.KeyHash)
	}
	if got.KeyDisplay == nil || *got.KeyDisplay != "sk-l…text" {
		t.Fatalf("KeyDisplay = %v", got.KeyDisplay)
	}

	missing, err := repo.FindApiKeyByLookup("no-such-lookup")
	if err != nil {
		t.Fatalf("FindApiKeyByLookup (miss): %v", err)
	}
	if missing != nil {
		t.Fatalf("FindApiKeyByLookup (miss) returned %+v, want nil", missing)
	}

	empty, err := repo.FindApiKeyByLookup("")
	if err != nil || empty != nil {
		t.Fatalf("FindApiKeyByLookup(\"\") = %+v, %v; want nil, nil", empty, err)
	}
}

// TestHashApiKeyRowBlanksPlaintext is the core storage guarantee: after
// hashing, the plaintext must not be readable from the row.
func TestHashApiKeyRowBlanksPlaintext(t *testing.T) {
	repo, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	const secret = "sk-super-secret-value-1234"
	if err := repo.CreateApiKey("k1", secret, "one", "m1"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	// Before hashing, the legacy plaintext path still resolves the key.
	legacy, err := repo.GetApiKeyByKey(secret)
	if err != nil || legacy == nil {
		t.Fatalf("legacy GetApiKeyByKey = %+v, %v; want the row", legacy, err)
	}

	if err := repo.HashApiKeyRow("k1", "verifier", "lookup1", "sk-s…1234"); err != nil {
		t.Fatalf("HashApiKeyRow: %v", err)
	}

	after, err := repo.GetApiKeyByKey(secret)
	if err != nil {
		t.Fatalf("GetApiKeyByKey after hashing: %v", err)
	}
	if after != nil {
		t.Fatal("plaintext key still resolves after the row was hashed")
	}
	byLookup, err := repo.FindApiKeyByLookup("lookup1")
	if err != nil || byLookup == nil {
		t.Fatalf("FindApiKeyByLookup after hashing = %+v, %v", byLookup, err)
	}
	if strings.Contains(byLookup.Key, secret) {
		t.Fatalf("row still carries the plaintext secret: %q", byLookup.Key)
	}
}

// TestHashApiKeyRowSupportsMultipleKeys guards the sentinel: apiKeys.key is
// UNIQUE NOT NULL, so blanking every row to the same value would make the
// second hashed key collide with the first.
func TestHashApiKeyRowSupportsMultipleKeys(t *testing.T) {
	repo, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	for _, id := range []string{"k1", "k2", "k3"} {
		secret := "sk-key-number-" + id
		if err := repo.CreateApiKey(id, secret, "n", "m"); err != nil {
			t.Fatalf("CreateApiKey %s: %v", id, err)
		}
		if err := repo.HashApiKeyRow(id, "verifier-"+id, "lookup-"+id, "sk-k…-"+id); err != nil {
			t.Fatalf("HashApiKeyRow %s: %v", id, err)
		}
		got, err := repo.FindApiKeyByLookup("lookup-" + id)
		if err != nil || got == nil || got.ID != id {
			t.Fatalf("key %s not resolvable by lookup: %+v, %v", id, got, err)
		}
	}
}

// TestCreateHashedApiKeyStoresNoPlaintext is the issuance guarantee: a key this
// build mints exists only as a verifier, a lookup index and a mask. Nothing in
// the row can be turned back into the secret.
func TestCreateHashedApiKeyStoresNoPlaintext(t *testing.T) {
	repo, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	const secret = "sk-issued-by-this-build-0000001"
	const display = "sk-issue***0001"
	if err := repo.CreateHashedApiKey("h1", "verifier-1", "lookup-1", display, "one", "m1"); err != nil {
		t.Fatalf("CreateHashedApiKey: %v", err)
	}

	row, err := repo.GetApiKeyByID("h1")
	if err != nil || row == nil {
		t.Fatalf("GetApiKeyByID = %+v, %v", row, err)
	}
	if strings.Contains(row.Key, secret) || row.Key == secret {
		t.Fatalf("row carries the plaintext secret: %q", row.Key)
	}
	if !IsHashedKeyRow(row) {
		t.Fatalf("row is not recognised as hash-only: %q", row.Key)
	}
	if row.KeyHash == nil || *row.KeyHash != "verifier-1" {
		t.Fatalf("KeyHash = %v, want the stored verifier", row.KeyHash)
	}
	if row.LookupHash == nil || *row.LookupHash != "lookup-1" {
		t.Fatalf("LookupHash = %v, want the stored index", row.LookupHash)
	}
	if LedgerIdentity(row) != display || LedgerMask(row) != display {
		t.Fatalf("ledger spellings = %q/%q, want %q", LedgerIdentity(row), LedgerMask(row), display)
	}
	bySecret, err := repo.GetApiKeyByKey(secret)
	if err != nil {
		t.Fatalf("GetApiKeyByKey: %v", err)
	}
	if bySecret != nil {
		t.Fatal("the plaintext resolves a row it was never written to")
	}
	byLookup, err := repo.FindApiKeyByLookup("lookup-1")
	if err != nil || byLookup == nil || byLookup.ID != "h1" {
		t.Fatalf("FindApiKeyByLookup = %+v, %v", byLookup, err)
	}
}

// TestRotateApiKeySecretReplacesVerifier proves rotation swaps the whole
// credential in one write: the old lookup index stops resolving, the new one
// does, and the plaintext never becomes a column value.
func TestRotateApiKeySecretReplacesVerifier(t *testing.T) {
	repo, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	if err := repo.CreateHashedApiKey("r1", "verifier-old", "lookup-old", "sk-old***0001", "n", "m"); err != nil {
		t.Fatalf("CreateHashedApiKey: %v", err)
	}
	if err := repo.RotateApiKeySecret("r1", "verifier-new", "lookup-new", "sk-new***0002"); err != nil {
		t.Fatalf("RotateApiKeySecret: %v", err)
	}

	row, err := repo.GetApiKeyByID("r1")
	if err != nil || row == nil {
		t.Fatalf("GetApiKeyByID = %+v, %v", row, err)
	}
	if row.KeyHash == nil || *row.KeyHash != "verifier-new" {
		t.Fatalf("KeyHash = %v, want the rotated verifier", row.KeyHash)
	}
	if row.KeyDisplay == nil || *row.KeyDisplay != "sk-new***0002" {
		t.Fatalf("KeyDisplay = %v, want the rotated mask", row.KeyDisplay)
	}
	if !IsHashedKeyRow(row) {
		t.Fatalf("rotated row is not hash-only: %q", row.Key)
	}
	old, err := repo.FindApiKeyByLookup("lookup-old")
	if err != nil {
		t.Fatalf("FindApiKeyByLookup(old): %v", err)
	}
	if old != nil {
		t.Fatalf("the previous lookup index still resolves %q", old.ID)
	}
	fresh, err := repo.FindApiKeyByLookup("lookup-new")
	if err != nil || fresh == nil || fresh.ID != "r1" {
		t.Fatalf("FindApiKeyByLookup(new) = %+v, %v", fresh, err)
	}
}

func TestHashApiKeyRowMissingID(t *testing.T) {
	repo, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	err := repo.HashApiKeyRow("does-not-exist", "v", "l", "d")
	if err == nil {
		t.Fatal("HashApiKeyRow on a missing id returned no error")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error %q does not name the row", err)
	}
}

// TestValidateApiKeyLegacyPath proves the legacy read path still works for a
// plaintext row, which is what an un-upgraded key looks like.
func TestValidateApiKeyLegacyPath(t *testing.T) {
	repo, cleanup := setupDashboardTestDB(t)
	defer cleanup()

	if err := repo.CreateApiKey("k1", "sk-legacy-token", "n", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	ok, err := repo.ValidateApiKey("sk-legacy-token")
	if err != nil || !ok {
		t.Fatalf("ValidateApiKey = %v, %v; want true, nil", ok, err)
	}
	ok, err = repo.ValidateApiKey("sk-nope")
	if err != nil || ok {
		t.Fatalf("ValidateApiKey(unknown) = %v, %v; want false, nil", ok, err)
	}
}
