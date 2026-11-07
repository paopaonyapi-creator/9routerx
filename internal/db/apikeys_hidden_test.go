// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package db

import (
	"path/filepath"
	"testing"
)

// TestApiKeyHiddenModels_RoundTrip pins the per-key hide-list column: unset
// reads empty, a write is read back verbatim, and a second write replaces.
func TestApiKeyHiddenModels_RoundTrip(t *testing.T) {
	repo := openEphemeralSchemaDB(t)
	if _, err := repo.db.Exec(
		`INSERT INTO apiKeys (id, key, isActive, createdAt) VALUES ('k1', 'sk-h1', 1, '2026-11-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	got, err := repo.GetApiKeyHiddenModels("k1")
	if err != nil {
		t.Fatalf("GetApiKeyHiddenModels: %v", err)
	}
	if got != "" {
		t.Errorf("unset key: got %q, want empty", got)
	}

	stored := `["oc/*-free","*haiku*"]`
	if err := repo.SetApiKeyHiddenModels("k1", stored); err != nil {
		t.Fatalf("SetApiKeyHiddenModels: %v", err)
	}
	got, err = repo.GetApiKeyHiddenModels("k1")
	if err != nil {
		t.Fatalf("GetApiKeyHiddenModels after set: %v", err)
	}
	if got != stored {
		t.Errorf("round trip: got %q, want %q", got, stored)
	}

	if err := repo.SetApiKeyHiddenModels("k1", ""); err != nil {
		t.Fatalf("SetApiKeyHiddenModels (clear): %v", err)
	}
	if got, _ = repo.GetApiKeyHiddenModels("k1"); got != "" {
		t.Errorf("cleared key: got %q, want empty", got)
	}

	// An unknown key id reads as empty rather than erroring: the filter wants
	// "hide nothing" from a key that vanished, not a failed listing.
	if got, err := repo.GetApiKeyHiddenModels("no-such-key"); err != nil || got != "" {
		t.Errorf("unknown key: got (%q, %v), want (\"\", nil)", got, err)
	}
}

// TestHiddenModels_GlobalSettingsRoundTrip pins the global hide list: the
// settings blob carries it, SetHiddenModels merges under the write lock
// without dropping other settings, and GetHiddenModels reads it back.
func TestHiddenModels_GlobalSettingsRoundTrip(t *testing.T) {
	repo := openEphemeralSchemaDB(t)

	if got, err := repo.GetHiddenModels(); err != nil || got != "" {
		t.Fatalf("unset global: got (%q, %v), want (\"\", nil)", got, err)
	}

	stored := `["openrouter/*:free"]`
	if err := repo.SetHiddenModels(stored); err != nil {
		t.Fatalf("SetHiddenModels: %v", err)
	}
	got, err := repo.GetHiddenModels()
	if err != nil {
		t.Fatalf("GetHiddenModels: %v", err)
	}
	if got != stored {
		t.Errorf("round trip: got %q, want %q", got, stored)
	}

	// The merge must not have clobbered a setting written beside it.
	if err := repo.SetAutoUpdate(true); err != nil {
		t.Fatalf("SetAutoUpdate: %v", err)
	}
	settings, err := repo.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if !settings.AutoUpdate {
		t.Error("SetAutoUpdate lost to the hidden-models write")
	}
	if settings.HiddenModels != stored {
		t.Errorf("settings.HiddenModels = %q, want %q", settings.HiddenModels, stored)
	}
}

// TestApiKeyHiddenModels_ColumnIsAdditive pins that a database created from
// the upstream core schema alone gains the column on EnsureAdditiveColumns —
// the upgrade path every existing install takes.
func TestApiKeyHiddenModels_ColumnIsAdditive(t *testing.T) {
	dir := t.TempDir()
	conn, err := OpenDatabase(filepath.Join(dir, "legacy.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer conn.Close()
	// The bare upstream shape: no hiddenModels column yet.
	if _, err := conn.Exec(`CREATE TABLE apiKeys (
		id TEXT PRIMARY KEY, key TEXT UNIQUE NOT NULL, name TEXT, machineId TEXT,
		isActive INTEGER DEFAULT 1, createdAt TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy apiKeys: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK (id = 1), data TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy settings: %v", err)
	}
	if err := EnsureAdditiveColumns(conn); err != nil {
		t.Fatalf("EnsureAdditiveColumns: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO apiKeys (id, key, isActive, createdAt, hiddenModels) VALUES ('k', 'sk', 1, '2026-11-01T00:00:00Z', '["a"]')`,
	); err != nil {
		t.Fatalf("write hiddenModels on upgraded table: %v", err)
	}
}
