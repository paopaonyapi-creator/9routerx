// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package tlsstealth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
)

// openTestRepo builds a repository over a throwaway sqlite file with the
// core schema, so the alert loop's Notify path runs against the same shape
// production writes to.
func openTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	conn, err := db.OpenDatabase(filepath.Join(t.TempDir(), "tlsstealth.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

// fixedClock returns a clock pinned to one instant, so the day-scoped
// dedupe key is identical on every pass and the loop's ticks collapse onto
// one notification row.
func fixedClock() time.Time {
	return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
}

func TestFlagDefaultsOff(t *testing.T) {
	s := New()
	if s.Enabled() {
		t.Fatal("a fresh controller must be disabled")
	}
	st := s.Status()
	if st.Enabled || st.ActiveProfile != "" || st.AlertLoopRunning {
		t.Fatalf("status of a disabled controller: %+v", st)
	}
	if st.LegalNotice == "" {
		t.Fatal("LegalNotice must never be empty: the banner carries it while on")
	}
	if !strings.Contains(st.LegalNotice, "compatibility layer") ||
		!strings.Contains(st.LegalNotice, "Terms of Service") {
		t.Errorf("LegalNotice must name the compatibility-layer framing: %q", st.LegalNotice)
	}
}

func TestSetProfileValidation(t *testing.T) {
	s := New()
	if err := s.SetProfile("not-a-profile"); err == nil {
		t.Fatal("unknown profile must be refused")
	}
	if err := s.SetProfile("chrome_124/macos"); err != nil {
		t.Fatalf("SetProfile catalog name: %v", err)
	}
	// The profile is selected but not presented until the flag is on: the
	// banner must not advertise a fingerprint that is not being sent.
	if got := s.ActiveProfile(); got != "" {
		t.Fatalf("ActiveProfile while disabled = %q, want empty", got)
	}
	s.SetEnabled(true)
	st := s.Status()
	if !st.Enabled || st.ActiveProfile != "chrome_124/macos" {
		t.Fatalf("status after enable: %+v", st)
	}
}

func TestProfilesCatalog(t *testing.T) {
	want := map[string]bool{
		"chrome_124/macos":   false,
		"chrome_146/linux":   false,
		"chrome_146/windows": false,
		"firefox_151/macos":  false,
	}
	catalog := Profiles()
	if len(catalog) != len(want) {
		t.Fatalf("catalog size = %d, want %d", len(catalog), len(want))
	}
	for _, p := range catalog {
		if _, ok := want[p.Name]; !ok {
			t.Fatalf("unexpected profile %q", p.Name)
		}
		want[p.Name] = true
		if p.Browser == "" || p.OS == "" {
			t.Errorf("profile %q missing browser/os", p.Name)
		}
		if p.ClientHello == "" {
			t.Errorf("profile %q missing ClientHello", p.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("catalog missing %q", name)
		}
	}
}

func TestDedupeKeyStability(t *testing.T) {
	at := fixedClock()
	// Two builds the same instant: the same key, so the feed's unique index
	// collapses them onto one row.
	if a, b := alertDedupeKey(at), alertDedupeKey(at); a != b {
		t.Fatalf("same-instant keys differ: %q vs %q", a, b)
	}
	// Two builds the same day: still the same key.
	if a, b := alertDedupeKey(at), alertDedupeKey(at.Add(11*time.Hour)); a != b {
		t.Fatalf("same-day keys differ: %q vs %q", a, b)
	}
	// The next day: a new key, so the alert recurs instead of refreshing a
	// stale row forever.
	if a, b := alertDedupeKey(at), alertDedupeKey(at.Add(24*time.Hour)); a == b {
		t.Fatalf("next-day key must change: %q", a)
	}
	want := "tls-stealth-enabled-24h:2026-11-06"
	if got := alertDedupeKey(at); got != want {
		t.Fatalf("dedupeKey = %q, want %q", got, want)
	}
	// A local clock is normalized to UTC, so a timezone move cannot shift
	// the cadence.
	local := time.Date(2026, 11, 7, 0, 30, 0, 0, time.FixedZone("plus-two", 2*60*60))
	if got := alertDedupeKey(local); got != "tls-stealth-enabled-24h:2026-11-06" {
		t.Fatalf("non-UTC clock key = %q, want UTC date", got)
	}
}

func TestAlertLoopWritesOnePerWindow(t *testing.T) {
	repo := openTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := fixedClock()
	s := New()
	s.SetEnabled(true)
	s.StartAlertLoop(ctx, repo, nil,
		WithInterval(5*time.Millisecond),
		WithClock(func() time.Time { return clock }),
	)
	if !s.Status().AlertLoopRunning {
		t.Fatal("alert loop must report running right after StartAlertLoop")
	}

	// The first alert fires immediately; wait for it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		list, _, err := repo.ListNotifications(context.Background(), 50)
		if err != nil {
			t.Fatalf("ListNotifications: %v", err)
		}
		if len(list) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no notification written")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Many more ticks land inside the same 24h window; the day-scoped
	// dedupe key collapses all of them onto the one row.
	time.Sleep(100 * time.Millisecond)
	list, _, err := repo.ListNotifications(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("notifications = %d, want exactly 1 per 24h window", len(list))
	}
	n := list[0]
	if n.DedupeKey != "tls-stealth-enabled-24h:2026-11-06" {
		t.Errorf("dedupeKey = %q", n.DedupeKey)
	}
	if n.Severity != "warn" {
		t.Errorf("severity = %q, want warn", n.Severity)
	}
	if n.Type != "security" {
		t.Errorf("type = %q, want security", n.Type)
	}
	if !strings.Contains(n.Body, "compatibility layer") || !strings.Contains(n.Body, "Terms of Service") {
		t.Errorf("body must carry the legal notice: %q", n.Body)
	}
}

func TestAlertLoopDisabledNoop(t *testing.T) {
	repo := openTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A disabled flag must not start the loop: no alerts when OFF.
	s := New()
	s.StartAlertLoop(ctx, repo, nil,
		WithInterval(5*time.Millisecond),
		WithClock(fixedClock),
	)
	time.Sleep(100 * time.Millisecond)
	if s.Status().AlertLoopRunning {
		t.Fatal("StartAlertLoop on a disabled controller must not start the loop")
	}
	list, _, err := repo.ListNotifications(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("disabled controller wrote %d notifications, want 0", len(list))
	}
}

func TestAlertLoopDisabledMidRunStaysSilent(t *testing.T) {
	repo := openTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clock := fixedClock()
	s := New()
	s.SetEnabled(true)
	s.StartAlertLoop(ctx, repo, nil,
		WithInterval(5*time.Millisecond),
		WithClock(func() time.Time { return clock }),
	)
	deadline := time.Now().Add(3 * time.Second)
	for {
		list, _, _ := repo.ListNotifications(context.Background(), 50)
		if len(list) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no notification written")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Off after the loop started: the loop keeps running but every pass
	// rechecks the flag, so nothing further is written.
	s.SetEnabled(false)
	time.Sleep(100 * time.Millisecond)
	list, _, err := repo.ListNotifications(context.Background(), 50)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("notifications after disable = %d, want the one written while on", len(list))
	}
}

func TestAlertLoopDispatchesWebhook(t *testing.T) {
	repo := openTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dispatched := make(chan string, 16)
	clock := fixedClock()
	s := New()
	s.SetEnabled(true)
	s.StartAlertLoop(ctx, repo, func(eventType, title, body, severity string) {
		select {
		case dispatched <- eventType + "|" + severity:
		default:
		}
	},
		WithInterval(5*time.Millisecond),
		WithClock(func() time.Time { return clock }),
	)

	select {
	case got := <-dispatched:
		if got != AlertEventType+"|warn" {
			t.Fatalf("dispatched %q, want %q", got, AlertEventType+"|warn")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no webhook event dispatched")
	}
}

func TestTransportErrors(t *testing.T) {
	if _, err := Transport(""); err == nil {
		t.Fatal("empty profile must be an error")
	}
	if _, err := Transport("not-a-profile"); err == nil {
		t.Fatal("unknown profile must be an error")
	}
}

func TestTransportChrome124(t *testing.T) {
	// The flag is off (fresh controller): Transport still works, it is
	// opt-in per call.
	client, err := Transport("chrome_124/macos")
	if err != nil {
		t.Fatalf("Transport(chrome_124/macos): %v", err)
	}
	if client == nil || client.Transport == nil {
		t.Fatal("client and transport must be non-nil")
	}
	// A second call reuses the cached client: the handshake state and the
	// connection pool are per-client.
	again, err := Transport("chrome_124/macos")
	if err != nil {
		t.Fatalf("Transport again: %v", err)
	}
	if again != client {
		t.Fatal("Transport must cache one client per profile")
	}
}

func TestWebExecutorClientSeam(t *testing.T) {
	s := New()
	client, err := s.WebExecutorClient()
	if err != nil || client != nil {
		t.Fatalf("while off, seam must return (nil, nil); got (%v, %v)", client, err)
	}
	s.SetEnabled(true)
	client, err = s.WebExecutorClient()
	if err != nil {
		t.Fatalf("WebExecutorClient while on: %v", err)
	}
	if client == nil {
		t.Fatal("while on, seam must return the stealth transport")
	}
}
