// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
)

func setupBackupTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "primary.sqlite")
	conn, err := db.OpenDatabase(dbPath)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

func TestBackupCreateAndRotate(t *testing.T) {
	repo := setupBackupTestRepo(t)
	backupDir := t.TempDir()
	ctx := context.Background()

	// Seed some data
	if err := repo.CreateApiKey("k-bk1", "sk-bk1", "Backup Key 1", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	cfg := db.AutoBackupConfig{
		Enabled:       true,
		IntervalHours: 24,
		Retention:     3,
		Destination:   backupDir,
	}
	if err := repo.SetAutoBackupConfig(ctx, cfg); err != nil {
		t.Fatalf("SetAutoBackupConfig: %v", err)
	}

	bm := NewManager(repo)

	// Mock JSON exporter
	exporter := func(ctx context.Context) ([]byte, error) {
		return json.Marshal(map[string]any{
			"version": 1,
			"apiKeys": []map[string]any{{"id": "k-bk1", "name": "Backup Key 1"}},
		})
	}

	baseTime := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

	// Create 5 backups sequentially with simulated time advance
	for i := 0; i < 5; i++ {
		now := baseTime.Add(time.Duration(i) * time.Hour)
		bm.SetClock(func() time.Time { return now })
		res, err := bm.CreateBackup(ctx, exporter)
		if err != nil {
			t.Fatalf("CreateBackup %d: %v", i, err)
		}
		if res.SQLitePath == "" || res.JSONPath == "" {
			t.Fatalf("empty backup paths: %+v", res)
		}
	}

	// Verify retention (retention=3): only 3 snapshots (3 sqlite + 3 json = 6 files) should remain
	items, err := bm.ListBackups(backupDir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(items) != 6 {
		t.Fatalf("found %d backup files, want 6 (3 sqlite + 3 json)", len(items))
	}

	// Check status in repo
	status, err := repo.GetAutoBackupStatus(ctx)
	if err != nil {
		t.Fatalf("GetAutoBackupStatus: %v", err)
	}
	if status.LastStatus != "success" || status.LastBackupBytes <= 0 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestRestoreDatabaseSQLiteAndJSON(t *testing.T) {
	repo := setupBackupTestRepo(t)
	backupDir := t.TempDir()
	ctx := context.Background()

	// Seed key
	if err := repo.CreateApiKey("k-init", "sk-init", "Initial Key", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	bm := NewManager(repo)
	cfg := db.AutoBackupConfig{Enabled: true, Retention: 5, Destination: backupDir}
	_ = repo.SetAutoBackupConfig(ctx, cfg)

	// Export JSON
	exportedData := []byte(`{"apiKeys":[{"id":"k-restored","name":"Restored JSON Key"}]}`)
	jsonExporter := func(ctx context.Context) ([]byte, error) {
		return exportedData, nil
	}

	res, err := bm.CreateBackup(ctx, jsonExporter)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	t.Run("restore_from_sqlite", func(t *testing.T) {
		// Mutate active DB (delete key)
		_ = repo.DeleteApiKey("k-init")
		k, _ := repo.GetApiKeyByID("k-init")
		if k != nil {
			t.Fatal("key should be deleted")
		}

		// Restore from sqlite snapshot
		if err := RestoreDatabase(ctx, repo, res.SQLitePath, nil); err != nil {
			t.Fatalf("RestoreDatabase sqlite: %v", err)
		}

		// Key should be back!
		k, err = repo.GetApiKeyByID("k-init")
		if err != nil || k == nil {
			t.Fatalf("key k-init not restored from sqlite: %v", err)
		}
	})

	t.Run("restore_from_json", func(t *testing.T) {
		var imported []byte
		mockImporter := func(ctx context.Context, data []byte) error {
			imported = data
			return nil
		}

		if err := RestoreDatabase(ctx, repo, res.JSONPath, mockImporter); err != nil {
			t.Fatalf("RestoreDatabase json: %v", err)
		}
		if string(imported) != string(exportedData) {
			t.Errorf("imported = %s, want %s", string(imported), string(exportedData))
		}
	})

	t.Run("restore_non_existent_file_errors", func(t *testing.T) {
		err := RestoreDatabase(ctx, repo, filepath.Join(backupDir, "missing.sqlite"), nil)
		if err == nil {
			t.Fatal("expected error for missing backup file")
		}
	})
}

func TestBackupManagerPeriodicTick(t *testing.T) {
	repo := setupBackupTestRepo(t)
	backupDir := t.TempDir()
	ctx := context.Background()

	cfg := db.AutoBackupConfig{
		Enabled:       true,
		IntervalHours: 24,
		Retention:     5,
		Destination:   backupDir,
	}
	_ = repo.SetAutoBackupConfig(ctx, cfg)

	bm := NewManager(repo)
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	bm.SetClock(func() time.Time { return base })

	// First tick should trigger backup
	bm.tick(nil)
	items, _ := bm.ListBackups(backupDir)
	if len(items) != 1 {
		t.Fatalf("items count = %d, want 1", len(items))
	}

	// Immediate second tick (no time elapsed) should NOT trigger backup
	bm.tick(nil)
	items2, _ := bm.ListBackups(backupDir)
	if len(items2) != 1 {
		t.Fatalf("items count = %d, want still 1", len(items2))
	}

	// Tick after interval elapsed should trigger second backup
	bm.SetClock(func() time.Time { return base.Add(25 * time.Hour) })
	bm.tick(nil)
	items3, _ := bm.ListBackups(backupDir)
	if len(items3) != 2 {
		t.Fatalf("items count = %d, want 2 after 25h", len(items3))
	}
}
