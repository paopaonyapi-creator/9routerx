// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"path/filepath"
	"testing"
)

// backupRestoreTestRepo builds a schema-complete repo on a file-backed
// database, so VACUUM INTO and the restore have a real file to work with.
func backupRestoreTestRepo(t *testing.T) *Repo {
	t.Helper()
	return openEphemeralSchemaDB(t)
}

// TestRestoreFromSQLiteBackupTwice pins the connection discipline of the
// restore: ATTACH, the copy transaction and DETACH all run on one pooled
// connection. The earlier shape attached through a *sql.Tx (one pooled
// connection) and detached through r.db (another), so every restore left
// backupDb attached to the connection the transaction had pinned, and the
// next restore to land on that connection failed its own ATTACH with
// "database backupDb is already in use" — restore worked exactly once per
// connection. A second restore on the same repo is the regression probe.
func TestRestoreFromSQLiteBackupTwice(t *testing.T) {
	repo := backupRestoreTestRepo(t)
	ctx := context.Background()
	db := repo.RawDB()

	if err := repo.CreateTeam(Team{ID: "acme", Name: "Acme", Status: TeamStatusActive, CreatedAt: nowUTC(), UpdatedAt: nowUTC()}); err != nil {
		t.Fatalf("seed team: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup.sqlite")
	if err := repo.VacuumInto(ctx, backupPath); err != nil {
		t.Fatalf("VacuumInto: %v", err)
	}

	// Damage the live table, restore once, expect the row back.
	if _, err := db.Exec(`DELETE FROM teams`); err != nil {
		t.Fatalf("clear teams: %v", err)
	}
	if err := repo.RestoreFromSQLiteBackup(ctx, backupPath); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	var restored int
	if err := db.QueryRow(`SELECT COUNT(1) FROM teams WHERE id = 'acme'`).Scan(&restored); err != nil {
		t.Fatalf("read restored team: %v", err)
	}
	if restored != 1 {
		t.Fatalf("first restore did not bring the row back")
	}

	// The second restore is the one the leaked attachment used to break: the
	// pooled connection that ran the first restore still carries backupDb.
	if err := repo.RestoreFromSQLiteBackup(ctx, backupPath); err != nil {
		t.Fatalf("second restore: %v (backupDb leaked on a pooled connection)", err)
	}
	if err := db.QueryRow(`SELECT COUNT(1) FROM teams WHERE id = 'acme'`).Scan(&restored); err != nil {
		t.Fatalf("read re-restored team: %v", err)
	}
	if restored != 1 {
		t.Fatalf("second restore did not bring the row back")
	}

	// A restore whose copy fails (a backup table whose shape no longer fits)
	// must leave the live rows intact: the transaction rolls back.
	if err := repo.CreateTeam(Team{ID: "extra", Name: "Extra", Status: TeamStatusActive, CreatedAt: nowUTC(), UpdatedAt: nowUTC()}); err != nil {
		t.Fatalf("seed extra team: %v", err)
	}
	if err := repo.RestoreFromSQLiteBackup(ctx, backupPath); err != nil {
		t.Fatalf("third restore (should succeed): %v", err)
	}
	var teams int
	if err := db.QueryRow(`SELECT COUNT(1) FROM teams`).Scan(&teams); err != nil {
		t.Fatalf("count teams: %v", err)
	}
	if teams != 1 {
		t.Errorf("teams after final restore = %d, want 1 (the restore replaces operator data with the backup's)", teams)
	}
}
