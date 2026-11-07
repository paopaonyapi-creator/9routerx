// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openAuditTestRepo(t *testing.T) *Repo {
	t.Helper()
	conn, err := OpenDatabase(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return NewRepo(conn)
}

// withAuditClock pins auditClock to a fixed instant for the duration of one
// test, so prune windows and default stamps are deterministic.
func withAuditClock(t *testing.T, fixed time.Time) {
	t.Helper()
	orig := auditClock
	auditClock = func() time.Time { return fixed }
	t.Cleanup(func() { auditClock = orig })
}

func TestInsertAndListAuditLogs(t *testing.T) {
	r := openAuditTestRepo(t)
	ctx := context.Background()

	entries := []AuditLogEntry{
		{Timestamp: "2026-01-03T10:00:00Z", Actor: "dashboard", Action: "api_key.create", Target: "key-1", AfterJSON: `{"name":"ci"}`, IP: "10.0.0.1"},
		{Timestamp: "2026-01-02T10:00:00Z", Actor: "dashboard", Action: "settings.update", BeforeJSON: `{"keys":[]}`, AfterJSON: `{"keys":["retentionDays"]}`},
		{Timestamp: "2026-01-01T10:00:00Z", Actor: "operator", Action: "connection.delete", Target: "conn-1", BeforeJSON: `{"provider":"claude"}`},
	}
	for i, e := range entries {
		if err := r.InsertAuditLog(ctx, e); err != nil {
			t.Fatalf("InsertAuditLog(%d): %v", i, err)
		}
	}

	all, err := r.ListAuditLogs(ctx, 0, "", "")
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if len(all) != len(entries) {
		t.Fatalf("listed %d entries, want %d", len(all), len(entries))
	}
	// Newest first: the read order an auditor expects.
	if all[0].Action != "api_key.create" || all[2].Action != "connection.delete" {
		t.Errorf("order = [%s, %s, %s], want newest first",
			all[0].Action, all[1].Action, all[2].Action)
	}
	if all[0].Target != "key-1" || all[0].IP != "10.0.0.1" || all[0].Actor != "dashboard" {
		t.Errorf("entry 0 = %+v, want the key-1 create with its ip and actor", all[0])
	}
	if all[1].BeforeJSON != `{"keys":[]}` || all[1].AfterJSON != `{"keys":["retentionDays"]}` {
		t.Errorf("entry 1 before/after = %q / %q, want the stored JSON", all[1].BeforeJSON, all[1].AfterJSON)
	}

	// limit caps the page.
	page, err := r.ListAuditLogs(ctx, 1, "", "")
	if err != nil || len(page) != 1 {
		t.Fatalf("limited page = %d entries (err %v), want 1", len(page), err)
	}
}

func TestListAuditLogsFilters(t *testing.T) {
	r := openAuditTestRepo(t)
	ctx := context.Background()

	seed := []AuditLogEntry{
		{Timestamp: "2026-01-01T10:00:00Z", Actor: "dashboard", Action: "api_key.create"},
		{Timestamp: "2026-01-02T10:00:00Z", Actor: "dashboard", Action: "api_key.delete"},
		{Timestamp: "2026-01-03T10:00:00Z", Actor: "operator", Action: "api_key.create"},
	}
	for i, e := range seed {
		if err := r.InsertAuditLog(ctx, e); err != nil {
			t.Fatalf("InsertAuditLog(%d): %v", i, err)
		}
	}

	cases := []struct {
		name      string
		actor     string
		action    string
		wantCount int
		wantFirst string
	}{
		{"filters by actor", "dashboard", "", 2, "api_key.delete"},
		{"filters by action", "", "api_key.create", 2, "api_key.create"},
		{"filters both", "operator", "api_key.create", 1, "api_key.create"},
		{"no match", "nobody", "", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.ListAuditLogs(ctx, 0, tc.actor, tc.action)
			if err != nil {
				t.Fatalf("ListAuditLogs: %v", err)
			}
			if len(got) != tc.wantCount {
				t.Fatalf("got %d entries, want %d", len(got), tc.wantCount)
			}
			if tc.wantCount > 0 && got[0].Action != tc.wantFirst {
				t.Errorf("first action = %q, want %q", got[0].Action, tc.wantFirst)
			}
		})
	}
}

// TestAuditLogWriteAhead proves the write-ahead contract: the entry exists
// (and lists) even though the action it describes was never performed.
func TestAuditLogWriteAhead(t *testing.T) {
	r := openAuditTestRepo(t)
	ctx := context.Background()

	entry := AuditLogEntry{
		Actor: "dashboard", Action: "api_key.create", Target: "key-never-created",
		AfterJSON: `{"name":"ghost"}`,
	}
	if err := r.InsertAuditLog(ctx, entry); err != nil {
		t.Fatalf("InsertAuditLog: %v", err)
	}
	// The "action" is deliberately a no-op: nothing else is written.

	got, err := r.ListAuditLogs(ctx, 0, "", "api_key.create")
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if len(got) != 1 || got[0].Target != "key-never-created" {
		t.Fatalf("write-ahead entry = %+v, want one row for key-never-created", got)
	}
}

func TestInsertAuditLogDefaultsTimestamp(t *testing.T) {
	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	withAuditClock(t, fixed)
	r := openAuditTestRepo(t)
	ctx := context.Background()

	if err := r.InsertAuditLog(ctx, AuditLogEntry{Actor: "dashboard", Action: "settings.update"}); err != nil {
		t.Fatalf("InsertAuditLog: %v", err)
	}
	got, err := r.ListAuditLogs(ctx, 0, "", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("listed %d entries (err %v), want 1", len(got), err)
	}
	if got[0].Timestamp != fixed.Format(time.RFC3339Nano) {
		t.Errorf("timestamp = %q, want the injected clock's %q", got[0].Timestamp, fixed.Format(time.RFC3339Nano))
	}
}

func TestPruneAuditLogsWithClock(t *testing.T) {
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	withAuditClock(t, fixed)
	r := openAuditTestRepo(t)
	ctx := context.Background()

	stamp := func(daysAgo int) string {
		return fixed.Add(-time.Duration(daysAgo) * 24 * time.Hour).Format(time.RFC3339Nano)
	}
	seed := []AuditLogEntry{
		{Timestamp: stamp(1), Actor: "dashboard", Action: "a.fresh"},
		{Timestamp: stamp(100), Actor: "dashboard", Action: "a.mid"},
		{Timestamp: stamp(365), Actor: "dashboard", Action: "a.ancient"},
	}
	for i, e := range seed {
		if err := r.InsertAuditLog(ctx, e); err != nil {
			t.Fatalf("InsertAuditLog(%d): %v", i, err)
		}
	}

	// Nothing is older than the window at first, then the 365-day row crosses
	// the default 180-day line.
	pruned, err := r.PruneAuditLogs(ctx, DefaultAuditLogRetentionDays)
	if err != nil {
		t.Fatalf("PruneAuditLogs: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned %d rows, want 1 (the 365-day-old entry)", pruned)
	}
	left, err := r.ListAuditLogs(ctx, 0, "", "")
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if len(left) != 2 {
		t.Fatalf("%d rows left, want 2", len(left))
	}
	for _, e := range left {
		if e.Action == "a.ancient" {
			t.Errorf("ancient row survived the prune: %+v", e)
		}
	}

	// days <= 0 keeps everything, and a tighter window drops the next-oldest
	// row: 10 days keeps only the fresh entry.
	if pruned, err := r.PruneAuditLogs(ctx, 0); err != nil || pruned != 0 {
		t.Fatalf("PruneAuditLogs(0) = %d, %v; want 0, nil", pruned, err)
	}
	if pruned, err := r.PruneAuditLogs(ctx, 10); err != nil || pruned != 1 {
		t.Fatalf("PruneAuditLogs(10) = %d, %v; want 1 (the 100-day-old entry)", pruned, err)
	}
	if left, err := r.ListAuditLogs(ctx, 0, "", ""); err != nil || len(left) != 1 || left[0].Action != "a.fresh" {
		t.Fatalf("remaining rows = %+v (err %v), want only a.fresh", left, err)
	}
}

func TestListAuditLogsFailsOnMissingTable(t *testing.T) {
	r := openAuditTestRepo(t)
	if _, err := r.RawDB().Exec(`DROP TABLE auditLog`); err != nil {
		t.Fatalf("drop auditLog: %v", err)
	}
	if _, err := r.ListAuditLogs(context.Background(), 0, "", ""); err == nil {
		t.Fatal("ListAuditLogs without the table = nil error, want a failure")
	}
	err := r.InsertAuditLog(context.Background(), AuditLogEntry{Action: "x"})
	if err == nil || !strings.Contains(err.Error(), "insert audit log") {
		t.Errorf("InsertAuditLog error = %v, want a wrapped insert failure", err)
	}
}
