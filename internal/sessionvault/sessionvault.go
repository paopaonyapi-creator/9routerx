// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package sessionvault persists the browser-session credentials the
// web-session executors (deepseek-web, gemini-web, chatgpt-web, muse-web)
// replay against their providers, sealed with the same credential vault the
// connection secrets use.
//
// The storage is the Go-only webexecSessions table, one row per provider
// account. The sessionData column holds either a vault-sealed envelope —
// packed as "sv1:<wrappedDEK>:<ciphertext>" because the schema gives the pair
// one column — or, on an install without ROUTER_MASTER_KEY, the plaintext
// session. The plaintext fallback mirrors internal/db's connection vault: a
// missing master key degrades to plaintext rather than refusing to boot, and
// the read path cannot tell the two apart without trying.
package sessionvault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/vault"
)

// sealedPrefix marks a sessionData value that carries a vault-sealed pair.
// A ":" separator is safe because both halves are std base64, whose alphabet
// has no colon.
const sealedPrefix = "sv1:"

// ErrSessionNotFound is returned by Load when no row is stored for the pair.
var ErrSessionNotFound = errors.New("sessionvault: no session stored")

// SessionEntry is one stored session. SessionData holds the stored (sealed)
// form; Load decrypts it, so a listing never pays for a decrypt it did not
// ask for.
type SessionEntry struct {
	Provider      string `json:"provider"`
	AccountID     string `json:"accountId"`
	SessionData   string `json:"-"`
	CreatedAt     string `json:"createdAt"`
	LastValidated string `json:"lastValidated,omitempty"`
}

// Store reads and writes the webexecSessions table through the repo, sealing
// through the repo's live vault instance so a key rotation applies to the
// next Store without any re-wiring.
type Store struct {
	repo *db.Repo
}

// New builds a session store over the repo's database and vault.
func New(repo *db.Repo) *Store {
	return &Store{repo: repo}
}

// Store upserts one account's session, sealing it when a master key is
// configured. Re-storing means the operator pasted a fresh session, so the
// validation stamp resets: the new cookie has not been proven to work yet.
func (s *Store) Store(ctx context.Context, provider, accountID, sessionData string) error {
	if provider == "" || accountID == "" {
		return errors.New("sessionvault.Store: provider and accountID are required")
	}
	stored, err := s.seal(sessionData)
	if err != nil {
		return fmt.Errorf("sessionvault.Store %s/%s: seal: %w", provider, accountID, err)
	}
	now := nowUTC()
	_, err = s.repo.RawDB().ExecContext(ctx,
		`INSERT INTO webexecSessions (provider, accountID, sessionData, createdAt, lastValidated)
		 VALUES (?, ?, ?, ?, '')
		 ON CONFLICT(provider, accountID) DO UPDATE SET
		   sessionData = excluded.sessionData,
		   createdAt = excluded.createdAt,
		   lastValidated = ''`,
		provider, accountID, stored, now,
	)
	if err != nil {
		return fmt.Errorf("sessionvault.Store %s/%s: upsert: %w", provider, accountID, err)
	}
	return nil
}

// Load reads one account's session and unseals it. A row that fails to
// decrypt is an error, never a silent plaintext passthrough: the caller would
// otherwise replay garbage at the provider.
func (s *Store) Load(ctx context.Context, provider, accountID string) (string, error) {
	var stored string
	err := s.repo.RawDB().QueryRowContext(ctx,
		`SELECT sessionData FROM webexecSessions WHERE provider = ? AND accountID = ?`,
		provider, accountID,
	).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sessionvault.Load %s/%s: %w", provider, accountID, ErrSessionNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("sessionvault.Load %s/%s: %w", provider, accountID, err)
	}
	plain, err := s.open(stored)
	if err != nil {
		return "", fmt.Errorf("sessionvault.Load %s/%s: %w", provider, accountID, err)
	}
	return plain, nil
}

// Delete removes one account's stored session. Deleting an absent row is not
// an error: the end state the caller asked for already holds.
func (s *Store) Delete(ctx context.Context, provider, accountID string) error {
	_, err := s.repo.RawDB().ExecContext(ctx,
		`DELETE FROM webexecSessions WHERE provider = ? AND accountID = ?`,
		provider, accountID,
	)
	if err != nil {
		return fmt.Errorf("sessionvault.Delete %s/%s: %w", provider, accountID, err)
	}
	return nil
}

// ListByProvider returns every stored session of one provider, oldest first.
// The rows are answered from the provider index and carry the sealed form;
// decryption is per-account and only on Load.
func (s *Store) ListByProvider(ctx context.Context, provider string) ([]SessionEntry, error) {
	rows, err := s.repo.RawDB().QueryContext(ctx,
		`SELECT provider, accountID, sessionData, createdAt, lastValidated
		 FROM webexecSessions WHERE provider = ? ORDER BY createdAt, accountID`,
		provider,
	)
	if err != nil {
		return nil, fmt.Errorf("sessionvault.ListByProvider %s: %w", provider, err)
	}
	defer rows.Close()

	out := []SessionEntry{}
	for rows.Next() {
		var e SessionEntry
		var lastValidated sql.NullString
		if err := rows.Scan(&e.Provider, &e.AccountID, &e.SessionData, &e.CreatedAt, &lastValidated); err != nil {
			return nil, fmt.Errorf("sessionvault.ListByProvider %s: scan: %w", provider, err)
		}
		e.LastValidated = lastValidated.String
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionvault.ListByProvider %s: %w", provider, err)
	}
	return out, nil
}

// MarkValidated stamps the last time the account's session passed a health
// check, so the dashboard can show how fresh each validation is.
func (s *Store) MarkValidated(ctx context.Context, provider, accountID string) error {
	_, err := s.repo.RawDB().ExecContext(ctx,
		`UPDATE webexecSessions SET lastValidated = ? WHERE provider = ? AND accountID = ?`,
		nowUTC(), provider, accountID,
	)
	if err != nil {
		return fmt.Errorf("sessionvault.MarkValidated %s/%s: %w", provider, accountID, err)
	}
	return nil
}

// seal returns the form the sessionData column stores: the packed sealed pair
// under a configured master key, or the plaintext unchanged when the vault is
// disabled — the same degraded mode the connection vault keeps an install in.
func (s *Store) seal(plaintext string) (string, error) {
	v := s.repo.Vault()
	if v == nil || !v.Enabled() {
		return plaintext, nil
	}
	wrapped, ciphertext, err := v.Seal(plaintext)
	if err != nil {
		return "", err
	}
	return sealedPrefix + wrapped + ":" + ciphertext, nil
}

// open reverses seal. A value without the prefix predates no seal, so it
// passes through untouched — the read path stays single-tracked.
func (s *Store) open(stored string) (string, error) {
	rest, ok := strings.CutPrefix(stored, sealedPrefix)
	if !ok {
		return stored, nil
	}
	wrapped, ciphertext, ok := strings.Cut(rest, ":")
	if !ok || wrapped == "" || ciphertext == "" {
		return "", fmt.Errorf("malformed sealed session: %w", vault.ErrTampered)
	}
	plain, err := s.repo.Vault().Open(wrapped, ciphertext)
	if err != nil {
		return "", err
	}
	return plain, nil
}

// nowUTC is the timestamp format every session row is stamped with.
func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }
