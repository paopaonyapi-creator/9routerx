// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const autoBackupScope = "autoBackup"

// AutoBackupConfig configures automatic database backups.
type AutoBackupConfig struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"intervalHours"` // e.g. 24 (daily)
	Retention     int    `json:"retention"`     // number of backup snapshots to keep, default 7
	Destination   string `json:"destination"`   // directory to save backups in, empty = default dataDir/backups
}

// AutoBackupStatus holds the status and timestamp of the last backup attempt.
type AutoBackupStatus struct {
	LastBackupAt    string `json:"lastBackupAt,omitempty"`
	LastBackupFile  string `json:"lastBackupFile,omitempty"`
	LastBackupBytes int64  `json:"lastBackupBytes,omitempty"`
	LastStatus      string `json:"lastStatus,omitempty"` // "success" or "error"
	LastError       string `json:"lastError,omitempty"`
}

// DefaultAutoBackupConfig returns sensible defaults (daily backup, keep 7 snapshots).
func DefaultAutoBackupConfig() AutoBackupConfig {
	return AutoBackupConfig{
		Enabled:       false,
		IntervalHours: 24,
		Retention:     7,
		Destination:   "",
	}
}

// VacuumInto creates a consistent point-in-time copy of the SQLite database using VACUUM INTO.
// Safe under WAL mode and concurrent read/write traffic.
func (r *Repo) VacuumInto(ctx context.Context, destPath string) error {
	_ = os.Remove(destPath)
	escaped := strings.ReplaceAll(destPath, "'", "''")
	_, err := r.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", escaped))
	if err != nil {
		return fmt.Errorf("vacuum into %s: %w", destPath, err)
	}
	return nil
}

// GetAutoBackupConfig reads configuration from the kv table.
func (r *Repo) GetAutoBackupConfig(ctx context.Context) (AutoBackupConfig, error) {
	cfg := DefaultAutoBackupConfig()
	var raw string
	err := r.db.QueryRowContext(ctx,
		`SELECT value FROM kv WHERE scope = ? AND key = 'config' LIMIT 1`, autoBackupScope,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read auto backup config: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return DefaultAutoBackupConfig(), nil
	}
	if cfg.IntervalHours <= 0 {
		cfg.IntervalHours = 24
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 7
	}
	return cfg, nil
}

// SetAutoBackupConfig stores configuration into the kv table.
func (r *Repo) SetAutoBackupConfig(ctx context.Context, cfg AutoBackupConfig) error {
	if cfg.IntervalHours <= 0 {
		cfg.IntervalHours = 24
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 7
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal auto backup config: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO kv (scope, key, value) VALUES (?, 'config', ?)
		 ON CONFLICT(scope, key) DO UPDATE SET value = excluded.value`,
		autoBackupScope, string(data),
	)
	if err != nil {
		return fmt.Errorf("write auto backup config: %w", err)
	}
	return nil
}

// GetAutoBackupStatus reads the last backup status from the kv table.
func (r *Repo) GetAutoBackupStatus(ctx context.Context) (AutoBackupStatus, error) {
	var status AutoBackupStatus
	var raw string
	err := r.db.QueryRowContext(ctx,
		`SELECT value FROM kv WHERE scope = ? AND key = 'status' LIMIT 1`, autoBackupScope,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return status, nil
		}
		return status, fmt.Errorf("read auto backup status: %w", err)
	}
	_ = json.Unmarshal([]byte(raw), &status)
	return status, nil
}

// SetAutoBackupStatus records the last backup status into the kv table.
func (r *Repo) SetAutoBackupStatus(ctx context.Context, status AutoBackupStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal auto backup status: %w", err)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO kv (scope, key, value) VALUES (?, 'status', ?)
		 ON CONFLICT(scope, key) DO UPDATE SET value = excluded.value`,
		autoBackupScope, string(data),
	)
	if err != nil {
		return fmt.Errorf("write auto backup status: %w", err)
	}
	return nil
}

// restoreTableSQLQuotes marks a table name safe to interpolate into the copy
// statements. The names come from the backup file's own sqlite_master, so they
// are data, not code: a backup carrying a table named `main; DROP TABLE
// apiKeys--` must be skipped, not executed. SQLite identifiers cannot contain
// a double quote unescaped and every table this schema creates is plain
// alphanumeric, so the conservative check costs nothing real.
var restoreTableNameOK = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RestoreFromSQLiteBackup restores tables from an external SQLite backup file
// by attaching the backup file and synchronizing tables inside a single transaction.
//
// The whole restore runs on one dedicated pooled connection. ATTACH and DETACH
// are connection-scoped, not session-scoped: attaching through a *sql.Tx and
// detaching through r.db sent the two statements to different pooled
// connections, so every restore left `backupDb` attached to whichever
// connection the transaction had pinned and errored (swallowed) on the
// connection that happened to run the DETACH. The second restore to land on
// the now-attached connection then failed its own ATTACH with "database
// backupDb is already in use" — restore worked exactly once per connection.
// One *sql.Conn for ATTACH, the copy transaction, and DETACH makes the three
// statements share a fate.
func (r *Repo) RestoreFromSQLiteBackup(ctx context.Context, backupPath string) error {
	if _, err := os.Stat(backupPath); err != nil {
		return fmt.Errorf("backup file not found: %w", err)
	}

	escaped := strings.ReplaceAll(backupPath, "'", "''")
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve connection for restore: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("ATTACH '%s' AS backupDb", escaped)); err != nil {
		return fmt.Errorf("attach backup database: %w", err)
	}
	// DETACH must not run inside the copy transaction (SQLite forbids ATTACH
	// and DETACH within one), and must run even when the copy fails, or the
	// attachment outlives the restore on this connection.
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "DETACH backupDb")
	}()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin restore transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	// Query tables that exist in the backup database
	rows, err := conn.QueryContext(ctx, `SELECT name FROM backupDb.sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("inspect backup tables: %w", err)
	}

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list backup tables: %w", err)
	}
	_ = rows.Close()

	for _, tbl := range tables {
		// Only restore tables that exist in the active schema, and only ones
		// whose names are safe to interpolate.
		if !restoreTableNameOK.MatchString(tbl) {
			continue
		}
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(1) FROM main.sqlite_master WHERE type = 'table' AND name = ?`, tbl).Scan(&count); err != nil || count == 0 {
			continue
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("DELETE FROM main.%s", tbl)); err != nil {
			return fmt.Errorf("clear table %s: %w", tbl, err)
		}
		// INSERT ... SELECT * pairs the columns by position: a backup taken by
		// an older build (fewer columns) fails the copy, which fails the
		// transaction — and the rollback keeps the live data intact. Restoring
		// across schema versions is a re-bootstrap, not a copy.
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("INSERT INTO main.%s SELECT * FROM backupDb.%s", tbl, tbl)); err != nil {
			return fmt.Errorf("copy table %s: %w", tbl, err)
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit restore transaction: %w", err)
	}
	committed = true
	return nil
}
