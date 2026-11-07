// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"testing"
	"time"
)

// apikeys_perkey_test.go covers the serenhope per-key governance columns
// (port(serenhope) TASK-1.1): the additive migration, the read-back through
// the shared scanner, the daily cost accumulator's lazy reset and the
// per-interval request counter's windowed reset.

// TestApiKeyPerKeyColumnsBackfilled proves a database created without the
// serenhope columns gains them on the next startup, the same in-place upgrade
// the budget columns get.
func TestApiKeyPerKeyColumnsBackfilled(t *testing.T) {
	dir := t.TempDir()
	conn, err := OpenDatabase(dir + "/legacy.sqlite")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer conn.Close()

	// A legacy apiKeys table: the pre-port shape, no per-key columns.
	if _, err := conn.Exec(`CREATE TABLE apiKeys (
		id TEXT PRIMARY KEY,
		key TEXT UNIQUE NOT NULL,
		name TEXT,
		machineId TEXT,
		isActive INTEGER DEFAULT 1,
		createdAt TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("legacy apiKeys setup: %v", err)
	}

	if err := EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	found := apiKeyColumnNames(t, conn)
	for _, want := range []string{
		"dailyCostLimit", "costSpent", "costDay",
		"ipWhitelist", "systemPrompt", "permissions",
		"requestLimit", "usedRequests", "requestResetInterval", "requestResetAt",
	} {
		if !found[want] {
			t.Errorf("column %s not backfilled onto legacy apiKeys", want)
		}
	}
}

// TestApiKeyPerKeyRoundTrip proves every serenhope governance column survives
// a write and a read through the shared column list, on both the single-row
// and the listing read, and that a fresh key reads them all as zero values.
func TestApiKeyPerKeyRoundTrip(t *testing.T) {
	r := openEphemeralSchemaDB(t)

	if err := r.CreateApiKey("k1", "sk-test-key-perkey", "per-key", "machine-1"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	fresh, err := r.GetApiKeyByID("k1")
	if err != nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	if fresh.DailyCostLimit != 0 || fresh.CostSpent != 0 || fresh.CostDay != "" ||
		fresh.IPWhitelist != "" || fresh.SystemPrompt != "" || fresh.Permissions != "" ||
		fresh.RequestLimit != 0 || fresh.UsedRequests != 0 ||
		fresh.RequestResetInterval != 0 || fresh.RequestResetAt != "" {
		t.Fatalf("fresh key carries per-key values: %+v", fresh)
	}

	sets := []struct {
		name string
		fn   func() error
	}{
		{"dailyCostLimit", func() error { return r.SetApiKeyDailyCostLimit("k1", 5.50) }},
		{"ipWhitelist", func() error { return r.SetApiKeyIPWhitelist("k1", `["10.0.0.0/8","1.2.3.4"]`) }},
		{"systemPrompt", func() error { return r.SetApiKeySystemPrompt("k1", "You are terse.") }},
		{"permissions", func() error { return r.SetApiKeyPermissions("k1", `{"endpoints":["chat"],"admin":false}`) }},
		{"requestLimit", func() error { return r.SetApiKeyRequestLimit("k1", 500, 1440) }},
	}
	for _, s := range sets {
		if err := s.fn(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
	}
	if err := r.BumpApiKeyCostSpent("k1", 1.25, "2026-11-05"); err != nil {
		t.Fatalf("BumpApiKeyCostSpent: %v", err)
	}
	if err := r.BumpApiKeyRequests("k1", time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("BumpApiKeyRequests: %v", err)
	}

	got, err := r.GetApiKeyByID("k1")
	if err != nil {
		t.Fatalf("GetApiKeyByID after set: %v", err)
	}
	if got.DailyCostLimit != 5.50 {
		t.Errorf("DailyCostLimit = %v, want 5.5", got.DailyCostLimit)
	}
	if got.CostSpent != 1.25 || got.CostDay != "2026-11-05" {
		t.Errorf("CostSpent/CostDay = %v/%q, want 1.25/2026-11-05", got.CostSpent, got.CostDay)
	}
	if got.IPWhitelist != `["10.0.0.0/8","1.2.3.4"]` {
		t.Errorf("IPWhitelist = %q", got.IPWhitelist)
	}
	if got.SystemPrompt != "You are terse." {
		t.Errorf("SystemPrompt = %q", got.SystemPrompt)
	}
	if got.Permissions != `{"endpoints":["chat"],"admin":false}` {
		t.Errorf("Permissions = %q", got.Permissions)
	}
	if got.RequestLimit != 500 || got.RequestResetInterval != 1440 {
		t.Errorf("RequestLimit/RequestResetInterval = %d/%d, want 500/1440", got.RequestLimit, got.RequestResetInterval)
	}
	if got.UsedRequests != 1 || got.RequestResetAt == "" {
		t.Errorf("UsedRequests/RequestResetAt = %d/%q, want 1/non-empty", got.UsedRequests, got.RequestResetAt)
	}

	keys, err := r.GetApiKeys()
	if err != nil {
		t.Fatalf("GetApiKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].SystemPrompt != "You are terse." {
		t.Errorf("listing lost the per-key columns: %+v", keys)
	}
}

// TestApiKeyPerKeyLegacySchemaReadsZero proves a Repo over a table that still
// misses the serenhope columns degrades to zero values instead of failing the
// read — the contract apiKeyColumnsFor exists for.
func TestApiKeyPerKeyLegacySchemaReadsZero(t *testing.T) {
	dir := t.TempDir()
	conn, err := OpenDatabase(dir + "/legacy.sqlite")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer conn.Close()
	// The apiKeys shape before the serenhope columns: base table plus the
	// budget set, nothing newer. EnsureCoreSchema is deliberately NOT run.
	if _, err := conn.Exec(`CREATE TABLE apiKeys (
		id TEXT PRIMARY KEY, key TEXT UNIQUE NOT NULL, name TEXT, machineId TEXT,
		isActive INTEGER DEFAULT 1, createdAt TEXT NOT NULL,
		rateLimitRPM INTEGER DEFAULT 0, rateLimitTPM INTEGER DEFAULT 0,
		rateLimitConcurrency INTEGER DEFAULT 0, tokenQuota INTEGER DEFAULT 0,
		queueTimeoutMs INTEGER DEFAULT 0, keyHash TEXT DEFAULT '',
		lookupHash TEXT DEFAULT '', keyDisplay TEXT DEFAULT '',
		expiresAt TEXT DEFAULT '', lastUsedAt TEXT DEFAULT '',
		usedCount INTEGER DEFAULT 0, metadata TEXT DEFAULT '', planId TEXT DEFAULT '',
		limitMicros INTEGER DEFAULT 0, limitTokens INTEGER DEFAULT 0,
		budgetPeriod TEXT DEFAULT '', alertPct INTEGER DEFAULT 0,
		hardCutoff INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("legacy apiKeys setup: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO apiKeys (id, key, createdAt) VALUES ('k1', 'sk-legacy', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	r := NewRepo(conn)
	if r.perKeyColumnsPresent() {
		t.Fatal("perKeyColumnsPresent = true on a legacy table")
	}
	got, err := r.GetApiKeyByID("k1")
	if err != nil {
		t.Fatalf("GetApiKeyByID on legacy table: %v", err)
	}
	if got == nil || got.ID != "k1" {
		t.Fatalf("legacy row missing: %+v", got)
	}
	if got.DailyCostLimit != 0 || got.SystemPrompt != "" || got.RequestLimit != 0 {
		t.Errorf("legacy read must zero the per-key axes: %+v", got)
	}
}

// TestBumpApiKeyCostSpentLazyReset pins the accumulator semantics upstream's
// usageRepo.js writes: same-day cost adds, a stale costDay stamp is replaced
// (not added to), and a zero-cost request is a no-op.
func TestBumpApiKeyCostSpentLazyReset(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	if err := r.CreateApiKey("k1", "sk-cost-key", "cost", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	if err := r.BumpApiKeyCostSpent("k1", 0.15, "2026-11-05"); err != nil {
		t.Fatalf("first bump: %v", err)
	}
	if err := r.BumpApiKeyCostSpent("k1", 0.10, "2026-11-05"); err != nil {
		t.Fatalf("second bump: %v", err)
	}
	spent, err := r.ApiKeyCostToday("k1", "2026-11-05")
	if err != nil {
		t.Fatalf("ApiKeyCostToday: %v", err)
	}
	if spent != 0.25 {
		t.Errorf("same-day spend = %v, want 0.25", spent)
	}

	// A costDay stamp from an earlier day reads as zero — the lazy daily
	// reset: stale spend is neither charged again nor carried over.
	stale, err := r.ApiKeyCostToday("k1", "2026-11-06")
	if err != nil {
		t.Fatalf("ApiKeyCostToday next day: %v", err)
	}
	if stale != 0 {
		t.Errorf("next-day spend = %v, want 0 (lazy reset)", stale)
	}

	// The next write on the new day REPLACES the accumulator.
	if err := r.BumpApiKeyCostSpent("k1", 0.15, "2026-11-06"); err != nil {
		t.Fatalf("next-day bump: %v", err)
	}
	got, err := r.GetApiKeyByID("k1")
	if err != nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	if got.CostSpent != 0.15 || got.CostDay != "2026-11-06" {
		t.Errorf("after rollover CostSpent/CostDay = %v/%q, want 0.15/2026-11-06 (replace, not 0.40)", got.CostSpent, got.CostDay)
	}

	// Zero-cost bumps change nothing, like upstream's `if (cost > 0)` guard.
	if err := r.BumpApiKeyCostSpent("k1", 0, "2026-11-06"); err != nil {
		t.Fatalf("zero bump: %v", err)
	}
	got, _ = r.GetApiKeyByID("k1")
	if got.CostSpent != 0.15 {
		t.Errorf("zero-cost bump changed the accumulator: %v", got.CostSpent)
	}
}

// TestBumpApiKeyRequestsIntervalReset pins the windowed counter semantics:
// inside the window the count grows, past the window it restarts at 1 with
// requestResetAt advanced, and with a zero interval the cap is lifetime and
// never rolls.
func TestBumpApiKeyRequestsIntervalReset(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	if err := r.CreateApiKey("k1", "sk-req-key", "req", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := r.SetApiKeyRequestLimit("k1", 3, 60); err != nil {
		t.Fatalf("SetApiKeyRequestLimit: %v", err)
	}

	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := r.BumpApiKeyRequests("k1", base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("bump %d: %v", i, err)
		}
	}
	got, _ := r.GetApiKeyByID("k1")
	if got.UsedRequests != 3 {
		t.Fatalf("UsedRequests = %d, want 3 inside the window", got.UsedRequests)
	}

	// One minute past the interval: the counter restarts.
	if err := r.BumpApiKeyRequests("k1", base.Add(61*time.Minute)); err != nil {
		t.Fatalf("bump past window: %v", err)
	}
	got, _ = r.GetApiKeyByID("k1")
	if got.UsedRequests != 1 {
		t.Errorf("UsedRequests = %d, want 1 after the window rolled", got.UsedRequests)
	}
	wantResetAt := base.Add(61 * time.Minute).Format(time.RFC3339)
	if got.RequestResetAt != wantResetAt {
		t.Errorf("RequestResetAt = %q, want %q", got.RequestResetAt, wantResetAt)
	}

	// Lifetime cap (interval 0): the counter never rolls.
	if err := r.SetApiKeyRequestLimit("k1", 10, 0); err != nil {
		t.Fatalf("SetApiKeyRequestLimit lifetime: %v", err)
	}
	if err := r.BumpApiKeyRequests("k1", base.Add(48*time.Hour)); err != nil {
		t.Fatalf("bump lifetime: %v", err)
	}
	got, _ = r.GetApiKeyByID("k1")
	if got.UsedRequests != 2 {
		t.Errorf("UsedRequests = %d, want 2 (lifetime cap never resets)", got.UsedRequests)
	}
}

// TestBumpApiKeyRequestsConcurrent proves the counter update is atomic under
// parallel writers: N bumps land as exactly N counts inside one window.
func TestBumpApiKeyRequestsConcurrent(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	if err := r.CreateApiKey("k1", "sk-race-key", "race", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := r.SetApiKeyRequestLimit("k1", 1000, 60); err != nil {
		t.Fatalf("SetApiKeyRequestLimit: %v", err)
	}

	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	const writers = 32
	done := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func() {
			done <- r.BumpApiKeyRequests("k1", now)
		}()
	}
	for i := 0; i < writers; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent bump: %v", err)
		}
	}
	got, _ := r.GetApiKeyByID("k1")
	if got.UsedRequests != writers {
		t.Errorf("UsedRequests = %d, want %d (lost updates under concurrency)", got.UsedRequests, writers)
	}
}

// TestBumpApiKeyCostSpentDayBoundaryOutOfOrder pins the day-boundary race the
// lazy reset has across midnight: a request that completed at 23:59:59 can
// reach the database after one that completed at 00:00:01, and the late
// old-day bump must not stamp costDay backwards — that would make today's
// accumulator read as zero through ApiKeyCostToday and under-count the daily
// cap for exactly the burst that crossed the boundary. The old-day cost
// belongs to a bucket the lazy reset has already closed, so it is dropped;
// what must never happen is the newer day's total being replaced by it.
func TestBumpApiKeyCostSpentDayBoundaryOutOfOrder(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	if err := r.CreateApiKey("k1", "sk-boundary-key", "boundary", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	// The new day's bump lands first (the 00:00:01 request).
	if err := r.BumpApiKeyCostSpent("k1", 0.20, "2026-11-06"); err != nil {
		t.Fatalf("new-day bump: %v", err)
	}
	// The late 23:59:59 arrival follows it.
	if err := r.BumpApiKeyCostSpent("k1", 0.30, "2026-11-05"); err != nil {
		t.Fatalf("late old-day bump: %v", err)
	}

	got, err := r.GetApiKeyByID("k1")
	if err != nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	if got.CostDay != "2026-11-06" {
		t.Errorf("costDay = %q, want 2026-11-06 (a late old-day bump must not move the stamp backwards)", got.CostDay)
	}
	if got.CostSpent != 0.20 {
		t.Errorf("costSpent = %v, want 0.20 (the new day's accumulator survives the late old-day bump)", got.CostSpent)
	}
	today, err := r.ApiKeyCostToday("k1", "2026-11-06")
	if err != nil {
		t.Fatalf("ApiKeyCostToday: %v", err)
	}
	if today != 0.20 {
		t.Errorf("ApiKeyCostToday = %v, want 0.20 (the cap must keep counting the new day)", today)
	}

	// Same-instant ordering the other way round still resets: an old-day
	// accumulator followed by the new day's first bump replaces it.
	if err := r.BumpApiKeyCostSpent("k1", 0.05, "2026-11-06"); err != nil {
		t.Fatalf("new-day accumulate: %v", err)
	}
	got, _ = r.GetApiKeyByID("k1")
	if got.CostSpent != 0.25 {
		t.Errorf("costSpent = %v, want 0.25 (same-day bumps accumulate)", got.CostSpent)
	}
}
