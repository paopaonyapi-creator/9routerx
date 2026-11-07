// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package notifications

import (
	"context"
	"sync"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/webhooks"
)

// TestInsertedNotificationFansOutToWebhooks proves the single choke point
// (ConditionScanner.notify) dispatches a webhook event whenever a condition
// write lands. The fan-out seam is stubbed — the SSRF guard refuses the
// loopback capture servers this suite would otherwise need — so delivery to
// matching targets stays covered by internal/webhooks' dispatcher tests.
func TestInsertedNotificationFansOutToWebhooks(t *testing.T) {
	repo := openScannerTestRepo(t)
	scanner := NewScanner(repo)

	baseTime := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	scanner.SetClock(func() time.Time { return baseTime })

	var mu sync.Mutex
	var events []webhooks.Event
	orig := fanout
	fanout = func(_ context.Context, _ *db.Repo, event webhooks.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	t.Cleanup(func() { fanout = orig })

	past := baseTime.Add(-1 * time.Hour).Format(time.RFC3339)
	if err := repo.CreateApiKey("k-fanout", "sk-fanout", "Fanout Key", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := repo.SetApiKeyExpiry("k-fanout", past); err != nil {
		t.Fatalf("SetApiKeyExpiry: %v", err)
	}

	if _, err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("got %d fan-out events, want 1: %+v", len(events), events)
	}
	got := events[0]
	want := webhooks.Event{
		Type:     "notification.key",
		Title:    "API key expired: Fanout Key",
		Body:     "Key expired at " + past + " and will refuse incoming requests with 401.",
		Severity: "warn",
	}
	if got != want {
		t.Fatalf("fan-out event = %+v, want %+v", got, want)
	}
}
