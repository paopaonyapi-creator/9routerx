// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package sessionvault

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/vault"
)

// newTestStore opens a temp SQLite database with the webexecSessions table
// and attaches a vault when masterKey is non-nil, mirroring how production
// attaches the vault built from ROUTER_MASTER_KEY.
func newTestStore(t *testing.T, masterKey []byte) (*Store, func()) {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test_sessionvault_*.sqlite")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpFile.Close()
	database, err := db.OpenDatabase(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("OpenDatabase: %v", err)
	}
	if err := db.EnsureKeiRouterTables(database); err != nil {
		database.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("EnsureKeiRouterTables: %v", err)
	}
	repo := db.NewRepo(database)
	if masterKey != nil {
		v, err := vault.New(masterKey)
		if err != nil {
			database.Close()
			os.Remove(tmpFile.Name())
			t.Fatalf("vault.New: %v", err)
		}
		repo.SetVault(v)
	}
	cleanup := func() {
		database.Close()
		os.Remove(tmpFile.Name())
	}
	return New(repo), cleanup
}

// testMasterKey returns a fresh random 32-byte AES key.
func testMasterKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, vault.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("read random key: %v", err)
	}
	return key
}

// storedRaw reads the sessionData column directly, so assertions can pin what
// is actually on disk rather than what Load returns.
func storedRaw(t *testing.T, s *Store, provider, accountID string) string {
	t.Helper()
	var raw string
	if err := s.repo.RawDB().QueryRow(
		`SELECT sessionData FROM webexecSessions WHERE provider = ? AND accountID = ?`,
		provider, accountID,
	).Scan(&raw); err != nil {
		t.Fatalf("read stored sessionData: %v", err)
	}
	return raw
}

func TestStoreLoadRoundTripSealed(t *testing.T) {
	s, cleanup := newTestStore(t, testMasterKey(t))
	defer cleanup()
	ctx := context.Background()

	const secret = "sessionCookie=abc123; path=/; HttpOnly"
	if err := s.Store(ctx, "deepseek-web", "a@x.com", secret); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := s.Load(ctx, "deepseek-web", "a@x.com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != secret {
		t.Fatalf("Load = %q, want the stored plaintext", got)
	}
	// What is on disk is the packed vault envelope, not the cookie.
	raw := storedRaw(t, s, "deepseek-web", "a@x.com")
	if !strings.HasPrefix(raw, sealedPrefix) {
		t.Fatalf("stored value %q must carry the sealed prefix", raw)
	}
	if strings.Contains(raw, secret) {
		t.Fatalf("stored value must not contain the plaintext session")
	}
}

func TestStorePlaintextWhenVaultDisabled(t *testing.T) {
	s, cleanup := newTestStore(t, nil)
	defer cleanup()
	ctx := context.Background()

	const secret = "no-master-key-install"
	if err := s.Store(ctx, "gemini-web", "a@x.com", secret); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, err := s.Load(ctx, "gemini-web", "a@x.com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != secret {
		t.Fatalf("Load = %q, want %q", got, secret)
	}
	// Without a master key the degraded mode stores plaintext, matching the
	// connection vault's behaviour.
	if raw := storedRaw(t, s, "gemini-web", "a@x.com"); raw != secret {
		t.Fatalf("stored = %q, want plaintext %q", raw, secret)
	}
}

func TestStoreUpsertResetsValidation(t *testing.T) {
	s, cleanup := newTestStore(t, testMasterKey(t))
	defer cleanup()
	ctx := context.Background()

	if err := s.Store(ctx, "chatgpt-web", "a@x.com", "first"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := s.MarkValidated(ctx, "chatgpt-web", "a@x.com"); err != nil {
		t.Fatalf("MarkValidated: %v", err)
	}
	entries, err := s.ListByProvider(ctx, "chatgpt-web")
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListByProvider = %v entries (err %v), want 1", len(entries), err)
	}
	if entries[0].LastValidated == "" {
		t.Fatalf("LastValidated must be stamped after MarkValidated")
	}
	// Re-storing is a fresh paste: the validation stamp resets.
	if err := s.Store(ctx, "chatgpt-web", "a@x.com", "second"); err != nil {
		t.Fatalf("re-Store: %v", err)
	}
	got, err := s.Load(ctx, "chatgpt-web", "a@x.com")
	if err != nil || got != "second" {
		t.Fatalf("Load after upsert = %q (err %v), want \"second\"", got, err)
	}
	entries, _ = s.ListByProvider(ctx, "chatgpt-web")
	if entries[0].LastValidated != "" {
		t.Fatalf("LastValidated after re-store = %q, want empty", entries[0].LastValidated)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, s *Store, ctx context.Context)
		wantErr error
	}{
		{
			name:    "missing row is ErrSessionNotFound",
			setup:   func(t *testing.T, s *Store, ctx context.Context) {},
			wantErr: ErrSessionNotFound,
		},
		{
			name: "tampered sealed value fails authentication",
			setup: func(t *testing.T, s *Store, ctx context.Context) {
				if err := s.Store(ctx, "deepseek-web", "a@x.com", "real"); err != nil {
					t.Fatalf("Store: %v", err)
				}
				if _, err := s.repo.RawDB().Exec(
					`UPDATE webexecSessions SET sessionData = ? WHERE provider = ? AND accountID = ?`,
					sealedPrefix+"AAAA:AAAA", "deepseek-web", "a@x.com",
				); err != nil {
					t.Fatalf("corrupt stored value: %v", err)
				}
			},
			wantErr: vault.ErrTampered,
		},
		{
			name: "malformed sealed prefix fails closed",
			setup: func(t *testing.T, s *Store, ctx context.Context) {
				if err := s.Store(ctx, "deepseek-web", "a@x.com", "real"); err != nil {
					t.Fatalf("Store: %v", err)
				}
				if _, err := s.repo.RawDB().Exec(
					`UPDATE webexecSessions SET sessionData = ? WHERE provider = ? AND accountID = ?`,
					sealedPrefix+"no-separator", "deepseek-web", "a@x.com",
				); err != nil {
					t.Fatalf("corrupt stored value: %v", err)
				}
			},
			wantErr: vault.ErrTampered,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := newTestStore(t, testMasterKey(t))
			defer cleanup()
			ctx := context.Background()
			tt.setup(t, s, ctx)
			_, err := s.Load(ctx, "deepseek-web", "a@x.com")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Load error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeleteRemovesOnlyOneAccount(t *testing.T) {
	s, cleanup := newTestStore(t, testMasterKey(t))
	defer cleanup()
	ctx := context.Background()

	seed := []struct{ provider, account string }{
		{"deepseek-web", "a@x.com"},
		{"deepseek-web", "b@x.com"},
		{"gemini-web", "a@x.com"},
	}
	for _, row := range seed {
		if err := s.Store(ctx, row.provider, row.account, "session-of-"+row.provider+"-"+row.account); err != nil {
			t.Fatalf("Store %s/%s: %v", row.provider, row.account, err)
		}
	}
	if err := s.Delete(ctx, "deepseek-web", "a@x.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Deleting an absent row is not an error: the end state already holds.
	if err := s.Delete(ctx, "deepseek-web", "a@x.com"); err != nil {
		t.Fatalf("repeat Delete: %v", err)
	}
	entries, err := s.ListByProvider(ctx, "deepseek-web")
	if err != nil {
		t.Fatalf("ListByProvider: %v", err)
	}
	if len(entries) != 1 || entries[0].AccountID != "b@x.com" {
		t.Fatalf("deepseek entries = %+v, want only b@x.com", entries)
	}
	// The other provider's row is untouched.
	other, err := s.ListByProvider(ctx, "gemini-web")
	if err != nil || len(other) != 1 {
		t.Fatalf("gemini entries = %d (err %v), want 1", len(other), err)
	}
}

func TestListByProviderIsolatesProviders(t *testing.T) {
	s, cleanup := newTestStore(t, testMasterKey(t))
	defer cleanup()
	ctx := context.Background()

	for _, provider := range []string{"deepseek-web", "gemini-web", "chatgpt-web", "muse-web"} {
		if err := s.Store(ctx, provider, "a@x.com", "s-"+provider); err != nil {
			t.Fatalf("Store %s: %v", provider, err)
		}
	}
	// An unknown provider lists empty rather than everything.
	entries, err := s.ListByProvider(ctx, "claude")
	if err != nil {
		t.Fatalf("ListByProvider unknown: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unknown provider returned %d entries, want 0", len(entries))
	}
	entries, err = s.ListByProvider(ctx, "muse-web")
	if err != nil {
		t.Fatalf("ListByProvider muse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("muse entries = %d, want 1", len(entries))
	}
	// The listing carries the stored (sealed) form; decryption stays on Load.
	if !strings.HasPrefix(entries[0].SessionData, sealedPrefix) {
		t.Fatalf("listing SessionData = %q, want the sealed stored form", entries[0].SessionData)
	}
}

func TestStoreRequiresProviderAndAccount(t *testing.T) {
	s, cleanup := newTestStore(t, testMasterKey(t))
	defer cleanup()
	if err := s.Store(context.Background(), "", "a@x.com", "x"); err == nil {
		t.Fatalf("Store without provider must error")
	}
	if err := s.Store(context.Background(), "deepseek-web", "", "x"); err == nil {
		t.Fatalf("Store without accountID must error")
	}
}
