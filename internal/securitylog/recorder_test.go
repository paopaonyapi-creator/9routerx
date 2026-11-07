// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package securitylog

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
)

func openTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.OpenDatabase(filepath.Join(dir, "sec.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

func TestRecorderAsyncAndFlush(t *testing.T) {
	repo := openTestRepo(t)
	rec := NewRecorder(repo, 500)
	defer rec.Close()

	for i := 0; i < 50; i++ {
		rec.Record(Entry{
			Type:     "login_failed",
			Severity: "warn",
			IP:       "127.0.0.1",
			Detail:   fmt.Sprintf("attempt %d", i),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rec.Flush(ctx); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	entries, err := repo.GetSecurityLogs(ctx, 100, "")
	if err != nil {
		t.Fatalf("GetSecurityLogs failed: %v", err)
	}
	if len(entries) != 50 {
		t.Fatalf("entries count = %d, want 50", len(entries))
	}
}

func TestRecorderRetentionTrimsOldest(t *testing.T) {
	repo := openTestRepo(t)
	const keep = 10
	rec := NewRecorder(repo, keep)
	defer rec.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Insert 30 entries sequentially with flush
	for i := 1; i <= 30; i++ {
		rec.Record(Entry{
			Type:     "probe",
			Severity: "critical",
			IP:       "10.0.0.1",
			Detail:   fmt.Sprintf("probe-%d", i),
		})
	}

	if err := rec.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Trigger retention trim
	if _, err := repo.TrimSecurityLog(ctx, keep); err != nil {
		t.Fatalf("TrimSecurityLog: %v", err)
	}

	remaining, err := repo.GetSecurityLogs(ctx, 100, "")
	if err != nil {
		t.Fatalf("GetSecurityLogs: %v", err)
	}
	if len(remaining) != keep {
		t.Fatalf("remaining = %d, want %d", len(remaining), keep)
	}
	// Oldest should have been pruned; newest (probe-30) must be present at head
	if remaining[0].Detail != "probe-30" {
		t.Errorf("newest entry = %q, want probe-30", remaining[0].Detail)
	}
	// The oldest preserved in remaining should be probe-21
	last := remaining[len(remaining)-1]
	if last.Detail != "probe-21" {
		t.Errorf("oldest preserved entry = %q, want probe-21", last.Detail)
	}
}

func TestRecorderNonBlockingUnderFlood(t *testing.T) {
	repo := openTestRepo(t)
	rec := NewRecorder(repo, 500)
	defer rec.Close()

	// Concurrently flood the channel with more than BufferCapacity events
	const flood = 5000
	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < flood; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			rec.Record(Entry{
				Type:     "flood_event",
				Severity: "info",
				IP:       "192.168.1.1",
				Detail:   fmt.Sprintf("flood-%d", n),
			})
		}(i)
	}
	wg.Wait()
	duration := time.Since(start)

	// Flooding 5000 events must complete rapidly without blocking callers
	if duration > 2*time.Second {
		t.Errorf("flood took %v, want < 2s (non-blocking drop-oldest)", duration)
	}

	// Dropped count must be non-negative (can be > 0 if buffer saturated)
	t.Logf("dropped events during flood: %d", rec.DroppedCount())
}
