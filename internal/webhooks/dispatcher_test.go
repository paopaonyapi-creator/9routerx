// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package webhooks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
)

// dispatcherFixture is the shared harness: a repo with the webhookTargets
// table, the SSRF guard opened for loopback, and three seeded targets.
type dispatcherFixture struct {
	repo *db.Repo
	hits map[string]*atomic.Int32
	now  time.Time
}

// newDispatcherFixture seeds one target per case: a discord target subscribed
// to quota.exceeded, a slack target subscribed to everything, and a generic
// target that is disabled. Each points at its own httptest origin.
func newDispatcherFixture(t *testing.T) *dispatcherFixture {
	t.Helper()
	conn, err := db.OpenDatabase(t.TempDir() + "/webhooks.sqlite")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureKeiRouterTables(conn); err != nil {
		t.Fatalf("EnsureKeiRouterTables: %v", err)
	}
	repo := db.NewRepo(conn)

	pointAtTestServer(t)
	resetRateLimit()

	quotaOnly, quotaHits, _ := captureServer(t)
	all, allHits, _ := captureServer(t)
	off, offHits, _ := captureServer(t)

	fix := &dispatcherFixture{
		repo: repo,
		hits: map[string]*atomic.Int32{
			"quotaOnly": quotaHits,
			"all":       allHits,
			"off":       offHits,
		},
		now: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
	seed := []db.WebhookTarget{
		{ID: "wh-quota", Type: TargetDiscord, URL: quotaOnly.URL, Enabled: true, FilterEvents: `["quota.exceeded"]`},
		{ID: "wh-all", Type: TargetSlack, URL: all.URL, Enabled: true, FilterEvents: "*"},
		{ID: "wh-off", Type: TargetGeneric, URL: off.URL, Enabled: false},
	}
	for i, row := range seed {
		if err := repo.SaveWebhookTarget(context.Background(), row); err != nil {
			t.Fatalf("seed target %d: %v", i, err)
		}
	}
	return fix
}

func (f *dispatcherFixture) dispatcher() *Dispatcher {
	d := NewDispatcher(f.repo)
	// The closure reads f.now through the fixture, so advance() reaches the
	// rate limiter. DispatchAll runs on the test goroutine; only the sends it
	// spawns are asynchronous.
	d.now = func() time.Time { return f.now }
	return d
}

func (f *dispatcherFixture) advance(d time.Duration) { f.now = f.now.Add(d) }

// waitFor polls cond until it holds or the deadline passes; the fan-out is
// asynchronous, so the observation window is the test's polling loop.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never held within 5s")
}

func TestDispatchAllDeliversToMatchingEnabledTargets(t *testing.T) {
	fix := newDispatcherFixture(t)

	fix.dispatcher().DispatchAll(context.Background(), Event{Type: EventQuotaExceeded, Title: "Quota"})
	waitFor(t, func() bool { return fix.hits["quotaOnly"].Load() == 1 && fix.hits["all"].Load() == 1 })
}

func TestDispatchAllHonorsFilter(t *testing.T) {
	fix := newDispatcherFixture(t)

	// The slack row subscribes to everything, the discord row only to
	// quota.exceeded: key.expired must reach exactly one of them.
	fix.dispatcher().DispatchAll(context.Background(), Event{Type: EventKeyExpired, Title: "Expired"})
	waitFor(t, func() bool { return fix.hits["all"].Load() == 1 })
	if fix.hits["quotaOnly"].Load() != 0 {
		t.Errorf("unsubscribed target hit %d times, want 0", fix.hits["quotaOnly"].Load())
	}
}

func TestDispatchAllSkipsDisabledTarget(t *testing.T) {
	fix := newDispatcherFixture(t)

	// The disabled generic row has a wildcard filter and a live server behind
	// it; being disabled is the only reason it must not fire.
	fix.dispatcher().DispatchAll(context.Background(), Event{Type: EventRequestFailed, Title: "Failed"})
	waitFor(t, func() bool { return fix.hits["all"].Load() == 1 })
	time.Sleep(100 * time.Millisecond)
	if fix.hits["off"].Load() != 0 {
		t.Errorf("disabled target hit %d times, want 0", fix.hits["off"].Load())
	}
}

// TestDispatchAllRateLimitDedupe proves the window: at most one notification
// per event type per minute, advanced with the injected clock instead of
// sleeping through a real one.
func TestDispatchAllRateLimitDedupe(t *testing.T) {
	fix := newDispatcherFixture(t)
	d := fix.dispatcher()

	d.DispatchAll(context.Background(), Event{Type: EventQuotaExceeded, Title: "first"})
	waitFor(t, func() bool { return fix.hits["quotaOnly"].Load() == 1 })

	// 30s later: still inside the window, nothing more goes out.
	fix.advance(30 * time.Second)
	d.DispatchAll(context.Background(), Event{Type: EventQuotaExceeded, Title: "second"})
	time.Sleep(150 * time.Millisecond)
	if fix.hits["quotaOnly"].Load() != 1 {
		t.Fatalf("dispatch inside the window hit %d times, want 1 (rate limited)", fix.hits["quotaOnly"].Load())
	}

	// 61s after the first send: the window has passed, the next one goes out.
	fix.advance(31 * time.Second)
	d.DispatchAll(context.Background(), Event{Type: EventQuotaExceeded, Title: "third"})
	waitFor(t, func() bool { return fix.hits["quotaOnly"].Load() == 2 })

	// A different event type has its own window and is not swallowed by the
	// quota.exceeded send that just went out.
	d.DispatchAll(context.Background(), Event{Type: EventKeyExpired, Title: "expired"})
	waitFor(t, func() bool { return fix.hits["all"].Load() >= 2 })
}

// TestDispatchAllWithoutTargets is the no-op path: an install with no
// destinations must not consume a rate slot or error.
func TestDispatchAllWithoutTargets(t *testing.T) {
	conn, err := db.OpenDatabase(t.TempDir() + "/empty.sqlite")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureKeiRouterTables(conn); err != nil {
		t.Fatalf("EnsureKeiRouterTables: %v", err)
	}
	resetRateLimit()

	NewDispatcher(db.NewRepo(conn)).DispatchAll(context.Background(), Event{Type: EventTestPing, Title: "ping"})
	if got := len(lastSentAt); got != 0 {
		t.Errorf("rate map holds %d entries after a target-less dispatch, want 0", got)
	}
}
