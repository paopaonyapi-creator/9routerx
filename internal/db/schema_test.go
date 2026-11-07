// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openEphemeralSchemaDB(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	conn, err := OpenDatabase(filepath.Join(dir, "data.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema failed: %v", err)
	}
	return NewRepo(conn)
}

func schemaTableNames(t *testing.T, r *Repo) map[string]bool {
	t.Helper()
	rows, err := r.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatalf("list tables failed: %v", err)
	}
	defer rows.Close()
	names := map[string]bool{}
	var name string
	for rows.Next() {
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name failed: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read table names failed: %v", err)
	}
	return names
}

func TestEnsureCoreSchema(t *testing.T) {
	t.Run("creates all upstream core tables with indexes", func(t *testing.T) {
		r := openEphemeralSchemaDB(t)
		names := schemaTableNames(t, r)
		for _, want := range []string{
			"settings", "_meta", "providerConnections", "providerNodes",
			"proxyPools", "apiKeys", "combos", "kv",
			"usageHistory", "usageDaily", "requestDetails",
		} {
			if !names[want] {
				t.Errorf("expected table %s to exist", want)
			}
		}
		var idxCount int
		if err := r.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx\_%' ESCAPE '\'`,
		).Scan(&idxCount); err != nil {
			t.Fatalf("count indexes failed: %v", err)
		}
		if idxCount < 15 {
			t.Errorf("expected at least 15 upstream indexes, got %d", idxCount)
		}
	})

	t.Run("seeds meta version and empty settings row", func(t *testing.T) {
		r := openEphemeralSchemaDB(t)
		var ver string
		if err := r.db.QueryRow(
			`SELECT value FROM _meta WHERE key = 'schemaVersion'`,
		).Scan(&ver); err != nil {
			t.Fatalf("seed _meta query failed: %v", err)
		}
		if ver != "1" {
			t.Errorf("expected schemaVersion 1, got %q", ver)
		}
		raw, err := r.GetSettingsRaw()
		if err != nil {
			t.Fatalf("GetSettingsRaw failed: %v", err)
		}
		if len(raw) != 0 {
			t.Errorf("expected empty settings {}, got %v", raw)
		}
	})

	t.Run("end-to-end fresh install flow works", func(t *testing.T) {
		r := openEphemeralSchemaDB(t)
		if err := r.UpdateSettingsRaw(map[string]any{"rtkEnabled": true}); err != nil {
			t.Fatalf("UpdateSettingsRaw failed: %v", err)
		}
		raw, err := r.GetSettingsRaw()
		if err != nil {
			t.Fatalf("GetSettingsRaw failed: %v", err)
		}
		if raw["rtkEnabled"] != true {
			t.Errorf("expected rtkEnabled true, got %v", raw["rtkEnabled"])
		}
		if err := r.UpdateConnectionLastUsed("missing-conn"); err != nil {
			t.Fatalf("UpdateConnectionLastUsed on missing row failed: %v", err)
		}
	})

	t.Run("idempotent and preserves existing data", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "data.sqlite")
		conn, err := OpenDatabase(path)
		if err != nil {
			t.Fatalf("OpenDatabase failed: %v", err)
		}
		defer conn.Close()
		if err := EnsureCoreSchema(conn); err != nil {
			t.Fatalf("first EnsureCoreSchema failed: %v", err)
		}
		r := NewRepo(conn)
		if err := r.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
			t.Fatalf("seed settings failed: %v", err)
		}
		if err := EnsureCoreSchema(conn); err != nil {
			t.Fatalf("second EnsureCoreSchema failed: %v", err)
		}
		raw, err := r.GetSettingsRaw()
		if err != nil {
			t.Fatalf("GetSettingsRaw failed: %v", err)
		}
		if raw["requireLogin"] != false {
			t.Errorf("expected requireLogin preserved, got %v", raw["requireLogin"])
		}
		var ver string
		if err := conn.QueryRow(
			`SELECT value FROM _meta WHERE key = 'schemaVersion'`,
		).Scan(&ver); err != nil {
			t.Fatalf("seed _meta query failed: %v", err)
		}
		if ver != "1" {
			t.Errorf("expected schemaVersion 1, got %q", ver)
		}
	})

	t.Run("backfills missing column on legacy database", func(t *testing.T) {
		f, err := os.CreateTemp("", "legacy_db_*.sqlite")
		if err != nil {
			t.Fatalf("temp file failed: %v", err)
		}
		path := f.Name()
		f.Close()
		defer os.Remove(path)
		conn, err := OpenDatabase(path)
		if err != nil {
			t.Fatalf("OpenDatabase failed: %v", err)
		}
		defer conn.Close()
		if _, err := conn.Exec(
			`CREATE TABLE providerConnections (id TEXT PRIMARY KEY, provider TEXT NOT NULL)`,
		); err != nil {
			t.Fatalf("legacy table setup failed: %v", err)
		}
		if err := EnsureCoreSchema(conn); err != nil {
			t.Fatalf("EnsureCoreSchema failed: %v", err)
		}
		rows, err := conn.Query(`PRAGMA table_info(providerConnections)`)
		if err != nil {
			t.Fatalf("inspect columns failed: %v", err)
		}
		defer rows.Close()
		var (
			cid, notNull, pk int
			name, colType    string
			dflt             sql.NullString
		)
		found := map[string]bool{}
		for rows.Next() {
			if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
				t.Fatalf("scan column failed: %v", err)
			}
			found[name] = true
		}
		for _, want := range []string{"data", "createdAt", "isActive", "lastUsedAt", "consecutiveUseCount"} {
			if !found[want] {
				t.Errorf("expected backfilled column %s", want)
			}
		}
	})
}

// TestBillingTeamIndexFollowsTheAdditivePass pins the ordering constraint the
// tenant cost-center index lives under: it names usageHistory.billingTeamId,
// a go-only column that exists only after EnsureAdditiveColumns has run, so it
// is created in the deferred pass. A legacy ledger — the shape every existing
// install boots from — must gain both the column and the index in one boot
// without "no such column" failing the schema pass.
func TestBillingTeamIndexFollowsTheAdditivePass(t *testing.T) {
	dir := t.TempDir()
	conn, err := OpenDatabase(filepath.Join(dir, "legacy.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer conn.Close()

	// A legacy ledger: the upstream core shape, no clientKey, no billingTeamId.
	if _, err := conn.Exec(`CREATE TABLE usageHistory (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp TEXT NOT NULL,
		provider TEXT,
		model TEXT,
		promptTokens INTEGER DEFAULT 0,
		completionTokens INTEGER DEFAULT 0,
		cost REAL DEFAULT 0,
		status TEXT
	)`); err != nil {
		t.Fatalf("legacy usageHistory setup: %v", err)
	}

	if err := EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema on legacy ledger: %v", err)
	}

	var indexCount int
	if err := conn.QueryRow(
		`SELECT COUNT(1) FROM sqlite_master WHERE type = 'index' AND name = 'idx_uh_billing_team'`,
	).Scan(&indexCount); err != nil {
		t.Fatalf("look up deferred index: %v", err)
	}
	if indexCount != 1 {
		t.Errorf("idx_uh_billing_team not created by the deferred pass")
	}

	var colCount int
	if err := conn.QueryRow(
		`SELECT COUNT(1) FROM pragma_table_info('usageHistory') WHERE name = 'billingTeamId'`,
	).Scan(&colCount); err != nil {
		t.Fatalf("look up billingTeamId column: %v", err)
	}
	if colCount != 1 {
		t.Errorf("billingTeamId column not backfilled onto the legacy ledger")
	}

	// The index must serve the shape TeamUsageReport runs, not just exist.
	plan := queryPlan(t, conn, `SELECT COALESCE(SUM(cost), 0) FROM usageHistory WHERE billingTeamId = 'x' AND timestamp >= '2026-01-01'`)
	if !strings.Contains(plan, "idx_uh_billing_team") {
		t.Errorf("team spend query does not use idx_uh_billing_team; plan:\n%s", plan)
	}
}

// queryPlan runs EXPLAIN QUERY PLAN and returns the plan text.
func queryPlan(t *testing.T, conn *sql.DB, query string) string {
	t.Helper()
	rows, err := conn.Query(`EXPLAIN QUERY PLAN ` + query)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var id, parent int
		var notUsed, detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		sb.WriteString(detail)
		sb.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan rows: %v", err)
	}
	return sb.String()
}
