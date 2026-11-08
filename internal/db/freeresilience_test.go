// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"testing"
	"time"

	"9routerx/proxy/internal/freeresilience"
)

func TestFreeTierRecovery_RecordBumpsActiveRow(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Minute)

	if err := r.RecordFreeTierLimitEvent("opencode", "big-pickle", "rate_limit", "keyless", &reset, "too many requests", now); err != nil {
		t.Fatalf("record: %v", err)
	}
	// A second event bumps the same row — attempts grow, the wait does not
	// shorten, and no second row appears.
	if err := r.RecordFreeTierLimitEvent("opencode", "big-pickle", "rate_limit", "keyless", &reset, "too many requests (again)", now.Add(time.Minute)); err != nil {
		t.Fatalf("record 2: %v", err)
	}

	active, err := r.ActiveFreeTierCooldowns(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active cooldowns = %d, want 1", len(active))
	}
	cd := active[0]
	if cd.Provider != "opencode" || cd.Kind != "rate_limit" || cd.Regime != "keyless" {
		t.Errorf("cooldown = %+v", cd)
	}
	if !cd.ResetAt.Equal(reset) {
		t.Errorf("reset = %v, want %v", cd.ResetAt, reset)
	}

	var attempts int
	if err := r.db.QueryRow(`SELECT attempts FROM freeTierRecovery WHERE provider = 'opencode'`).Scan(&attempts); err != nil {
		t.Fatalf("query attempts: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
	var count int
	_ = r.db.QueryRow(`SELECT COUNT(1) FROM freeTierRecovery`).Scan(&count)
	if count != 1 {
		t.Errorf("rows = %d, want 1 (bumped, not duplicated)", count)
	}
}

func TestFreeTierRecovery_ResetOnlyMovesLater(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	long := now.Add(24 * time.Hour)
	short := now.Add(5 * time.Minute)
	_ = r.RecordFreeTierLimitEvent("monthly-tier", "m", "rate_limit", "recurring-monthly", &long, "monthly cap", now)
	// A later event whose header asks for less must NOT shorten the wait.
	_ = r.RecordFreeTierLimitEvent("monthly-tier", "m", "rate_limit", "recurring-monthly", &short, "429", now.Add(time.Minute))

	active, _ := r.ActiveFreeTierCooldowns(now.Add(time.Minute))
	if len(active) != 1 || !active[0].ResetAt.Equal(long) {
		t.Fatalf("active = %+v, want reset kept at %v", active, long)
	}
}

func TestFreeTierRecovery_OpenEndedAndExpiry(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	// Credit tier: open-ended (no auto reset).
	_ = r.RecordFreeTierLimitEvent("credittier", "m", "quota", "recurring-credit", nil, "credits exhausted", now)
	// Daily tier: reset in one hour.
	reset := now.Add(time.Hour)
	_ = r.RecordFreeTierLimitEvent("dailytier", "m", "rate_limit", "recurring-daily", &reset, "429", now)

	// Nothing expired yet.
	if rejoined, _ := r.ExpireFreeTierCooldowns(now.Add(30 * time.Minute)); len(rejoined) != 0 {
		t.Fatalf("nothing should expire yet, got %v", rejoined)
	}
	// The open-ended row must NEVER expire.
	if rejoined, _ := r.ExpireFreeTierCooldowns(now.Add(48 * time.Hour)); len(rejoined) != 1 || rejoined[0] != "dailytier" {
		t.Fatalf("rejoined = %v, want [dailytier] only", rejoined)
	}
	active, _ := r.ActiveFreeTierCooldowns(now.Add(48 * time.Hour))
	if len(active) != 1 || active[0].Provider != "credittier" || !active[0].OpenEnded {
		t.Fatalf("active = %+v, want only the open-ended credit row", active)
	}

	// Early recovery clears the credit row.
	if err := r.ClearFreeTierCooldowns("credittier", now.Add(48*time.Hour), "success"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if active, _ = r.ActiveFreeTierCooldowns(now.Add(48 * time.Hour)); len(active) != 0 {
		t.Fatalf("active after clear = %+v, want empty", active)
	}
	var recovered int
	_ = r.db.QueryRow(`SELECT COUNT(1) FROM freeTierRecovery WHERE status = 'recovered'`).Scan(&recovered)
	if recovered != 2 {
		t.Errorf("recovered rows = %d, want 2 (expired + cleared)", recovered)
	}
}

func TestFreeProviderHealth_RoundTrip(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	outcomes := []freeresilience.Outcome{
		{At: now.Add(-time.Hour), Success: true, Status: 200, LatencyMs: 120},
		{At: now.Add(-30 * time.Minute), Success: false, Status: 429, LatencyMs: 0},
		{At: now.Add(-time.Minute), Success: true, Status: 200, LatencyMs: 90},
	}
	if err := r.SaveFreeProviderHealth("groq", outcomes, now); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A second save rewrites the same row.
	trimmed := outcomes[1:]
	if err := r.SaveFreeProviderHealth("groq", trimmed, now); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	loaded, err := r.LoadFreeProviderHealth()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded providers = %d, want 1", len(loaded))
	}
	got := loaded["groq"]
	if len(got) != 2 {
		t.Fatalf("loaded outcomes = %d, want 2", len(got))
	}
	if got[0].Status != 429 || got[1].LatencyMs != 90 {
		t.Errorf("loaded = %+v", got)
	}
}

func TestFreeTierRecovery_LatestEvents(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Minute)
	_ = r.RecordFreeTierLimitEvent("opencode", "big-pickle", "rate_limit", "keyless", &reset, "429", now)
	_ = r.RecordFreeTierLimitEvent("groq", "openai/gpt-oss-120b", "rate_limit", "recurring-daily", &reset, "429", now.Add(time.Second))

	events, err := r.LatestFreeTierRecoveryEvents(10)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	// Newest first.
	if events[0].Provider != "groq" {
		t.Errorf("newest = %s, want groq", events[0].Provider)
	}
	if events[0].Status != "cooling" {
		t.Errorf("status = %s, want cooling", events[0].Status)
	}
}
