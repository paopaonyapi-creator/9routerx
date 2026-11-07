// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"path/filepath"
	"testing"
)

func openNotificationTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	conn, err := OpenDatabase(filepath.Join(dir, "notif.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return NewRepo(conn)
}

func TestNotificationsDedupeAndLifecycle(t *testing.T) {
	r := openNotificationTestRepo(t)
	ctx := context.Background()

	// 1. First notify creates entry
	id1, err := r.Notify(ctx, "", "", "quota:k1:tokens:warning", "quota", "warn", "Warning 90%", "Key used 90% quota")
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if id1 == "" {
		t.Fatal("empty notification ID returned")
	}

	unread, err := r.GetUnreadNotificationCount(ctx)
	if err != nil || unread != 1 {
		t.Fatalf("unread count = %d, want 1", unread)
	}

	// 2. Second notify with same dedupeKey updates content without creating duplicate
	id2, err := r.Notify(ctx, "", "", "quota:k1:tokens:warning", "quota", "warn", "Warning 95%", "Key used 95% quota")
	if err != nil {
		t.Fatalf("Notify duplicate: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("id2 = %s, want same id1 = %s", id2, id1)
	}

	list, count, err := r.ListNotifications(ctx, 10)
	if err != nil || len(list) != 1 || count != 1 {
		t.Fatalf("list len = %d, count = %d, want 1", len(list), count)
	}
	if list[0].Title != "Warning 95%" || list[0].Body != "Key used 95% quota" {
		t.Errorf("title/body not updated on dedupe: %+v", list[0])
	}

	// 3. Mark read
	if err := r.MarkNotificationRead(ctx, id1); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
	unread, _ = r.GetUnreadNotificationCount(ctx)
	if unread != 0 {
		t.Fatalf("unread after markRead = %d, want 0", unread)
	}

	// 4. Acknowledging retires the notification
	if err := r.AckNotification(ctx, id1, ""); err != nil {
		t.Fatalf("AckNotification: %v", err)
	}
	list, _, _ = r.ListNotifications(ctx, 10)
	if !list[0].Acked {
		t.Errorf("notification should be acked: %+v", list[0])
	}

	// 5. Condition recurs after ack -> re-arms as unread
	id3, err := r.Notify(ctx, "", "", "quota:k1:tokens:warning", "quota", "warn", "Warning again", "Recurred")
	if err != nil {
		t.Fatalf("Notify after ack: %v", err)
	}
	if id3 != id1 {
		t.Fatalf("id3 = %s, want id1 = %s", id3, id1)
	}
	unread, _ = r.GetUnreadNotificationCount(ctx)
	if unread != 1 {
		t.Fatalf("unread after re-arm = %d, want 1", unread)
	}
}

// TestNotifyConcurrentSameDedupeKey pins the insert race on the dedupe path:
// two writers that alert on the same condition in the same instant both pass
// the dedupe probe and both attempt the insert, and the partial unique index
// lets exactly one win. The loser must fold into the winner's row instead of
// surfacing a UNIQUE-constraint failure for a notification that does exist —
// the budget checker treats a Notify error as a lost alert.
func TestNotifyConcurrentSameDedupeKey(t *testing.T) {
	r := openNotificationTestRepo(t)
	ctx := context.Background()

	const writers = 16
	done := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(n int) {
			_, err := r.Notify(ctx, "", "", "team_budget:acme:2026-11-06T00:00:00Z",
				"budget", "warn", "Team budget exceeded", "body")
			done <- err
		}(i)
	}
	for i := 0; i < writers; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent Notify: %v", err)
		}
	}

	var n int
	if err := r.RawDB().QueryRow(
		`SELECT COUNT(*) FROM notifications WHERE dedupeKey = ?`,
		"team_budget:acme:2026-11-06T00:00:00Z",
	).Scan(&n); err != nil {
		t.Fatalf("count deduped rows: %v", err)
	}
	if n != 1 {
		t.Errorf("notifications with the dedupe key = %d, want 1", n)
	}
}
