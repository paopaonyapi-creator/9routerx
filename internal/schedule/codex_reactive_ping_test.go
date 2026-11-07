// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package schedule

import (
	"context"
	"testing"
	"time"
)

func ptrFloat(v float64) *float64 {
	return &v
}

func TestCodexReactiveAutoPingTableDriven(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	account := CodexAccount{
		ID:          "codex-1",
		Provider:    "codex",
		AuthType:    "oauth",
		AccessToken: "test-token",
		IsActive:    true,
	}

	t.Run("does_not_ping_when_setting_is_absent", func(t *testing.T) {
		s := NewCodexReactiveScheduler()
		s.SetClock(func() time.Time { return baseTime })

		pingCalled := false
		usageCalled := false

		fetchUsage := func(ctx context.Context, acc CodexAccount) (*CodexUsageReport, error) {
			usageCalled = true
			return nil, nil
		}
		sendPing := func(ctx context.Context, acc CodexAccount) error {
			pingCalled = true
			return nil
		}

		s.RunTick(context.Background(), nil, []CodexAccount{account}, fetchUsage, sendPing, nil)
		if usageCalled || pingCalled {
			t.Errorf("expected no usage fetch or ping when setting is absent (usage=%v, ping=%v)", usageCalled, pingCalled)
		}
	})

	t.Run("opt_in_and_opt_out_activation", func(t *testing.T) {
		// Opted-out
		cfgOptOut := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": false},
			},
		}
		if HasAnyCodexAutoPingEnabled(cfgOptOut) {
			t.Error("expected false when connection is false")
		}

		// Opted-in
		cfgOptIn := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": true},
			},
		}
		if !HasAnyCodexAutoPingEnabled(cfgOptIn) {
			t.Error("expected true when connection is true")
		}
	})

	t.Run("does_not_ping_on_first_resetAt_observation", func(t *testing.T) {
		s := NewCodexReactiveScheduler()
		s.SetClock(func() time.Time { return baseTime })

		settings := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": true},
			},
		}
		pingCalled := false
		resetAt := "2026-01-01T13:00:00Z"

		fetchUsage := func(ctx context.Context, acc CodexAccount) (*CodexUsageReport, error) {
			return &CodexUsageReport{
				Quotas: map[string]QuotaEntry{
					"session": {Used: ptrFloat(1), ResetAt: resetAt},
				},
			}, nil
		}
		sendPing := func(ctx context.Context, acc CodexAccount) error {
			pingCalled = true
			return nil
		}

		s.RunTick(context.Background(), settings, []CodexAccount{account}, fetchUsage, sendPing, nil)
		if pingCalled {
			t.Error("ping called on first resetAt observation")
		}
		if s.ResetCache()["codex-1"] != resetAt {
			t.Errorf("resetCache = %q, want %q", s.ResetCache()["codex-1"], resetAt)
		}
	})

	t.Run("sends_ping_when_session_resetAt_slides", func(t *testing.T) {
		s := NewCodexReactiveScheduler()
		s.SetClock(func() time.Time { return baseTime })
		s.SetResetCache(map[string]string{
			"codex-1": "2026-01-01T17:00:00Z",
		})

		settings := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": true},
			},
		}
		pingCalled := false
		updated := false
		newResetAt := "2026-01-01T17:01:00Z" // drifted 60s > 30s threshold

		fetchUsage := func(ctx context.Context, acc CodexAccount) (*CodexUsageReport, error) {
			return &CodexUsageReport{
				Quotas: map[string]QuotaEntry{
					"session": {Used: ptrFloat(1), Total: ptrFloat(100), Remaining: ptrFloat(99), ResetAt: newResetAt},
				},
			}, nil
		}
		sendPing := func(ctx context.Context, acc CodexAccount) error {
			pingCalled = true
			return nil
		}
		updateAcc := func(ctx context.Context, id string, lastPingAt time.Time, rAt, rKey string) error {
			if id == "codex-1" && rAt == newResetAt {
				updated = true
			}
			return nil
		}

		s.RunTick(context.Background(), settings, []CodexAccount{account}, fetchUsage, sendPing, updateAcc)
		if !pingCalled {
			t.Error("expected ping when resetAt slides")
		}
		if !updated {
			t.Error("expected account update after ping")
		}
	})

	t.Run("does_not_ping_when_resetAt_is_stable", func(t *testing.T) {
		s := NewCodexReactiveScheduler()
		s.SetClock(func() time.Time { return baseTime })
		s.SetResetCache(map[string]string{
			"codex-1": "2026-01-01T17:00:00Z",
		})

		settings := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": true},
			},
		}
		pingCalled := false
		stableResetAt := "2026-01-01T17:00:00Z"

		fetchUsage := func(ctx context.Context, acc CodexAccount) (*CodexUsageReport, error) {
			return &CodexUsageReport{
				Quotas: map[string]QuotaEntry{
					"session": {Used: ptrFloat(1), Total: ptrFloat(100), Remaining: ptrFloat(99), ResetAt: stableResetAt},
				},
			}, nil
		}
		sendPing := func(ctx context.Context, acc CodexAccount) error {
			pingCalled = true
			return nil
		}

		s.RunTick(context.Background(), settings, []CodexAccount{account}, fetchUsage, sendPing, nil)
		if pingCalled {
			t.Error("ping called when resetAt was stable")
		}
	})

	t.Run("does_not_repeat_inside_minimum_ping_interval", func(t *testing.T) {
		s := NewCodexReactiveScheduler()
		s.SetClock(func() time.Time { return baseTime })
		s.SetResetCache(map[string]string{
			"codex-1": "2026-01-01T17:00:00Z",
		})

		accRecent := account
		accRecent.LastPingAt = baseTime.Add(-5 * time.Minute) // 5m ago < 10m min interval

		settings := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": true},
			},
		}
		pingCalled := false

		fetchUsage := func(ctx context.Context, acc CodexAccount) (*CodexUsageReport, error) {
			return &CodexUsageReport{
				Quotas: map[string]QuotaEntry{
					"session": {Used: ptrFloat(1), Total: ptrFloat(100), Remaining: ptrFloat(99), ResetAt: "2026-01-01T17:01:00Z"},
				},
			}, nil
		}
		sendPing := func(ctx context.Context, acc CodexAccount) error {
			pingCalled = true
			return nil
		}

		s.RunTick(context.Background(), settings, []CodexAccount{accRecent}, fetchUsage, sendPing, nil)
		if pingCalled {
			t.Error("ping called inside minimum ping interval")
		}
	})

	t.Run("does_not_ping_when_usage_is_zero", func(t *testing.T) {
		s := NewCodexReactiveScheduler()
		s.SetClock(func() time.Time { return baseTime })
		s.SetResetCache(map[string]string{
			"codex-1": "2026-01-01T17:00:00Z",
		})

		settings := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": true},
			},
		}
		pingCalled := false

		fetchUsage := func(ctx context.Context, acc CodexAccount) (*CodexUsageReport, error) {
			return &CodexUsageReport{
				Quotas: map[string]QuotaEntry{
					"session": {Used: ptrFloat(0), ResetAt: "2026-01-01T17:01:00Z"},
				},
			}, nil
		}
		sendPing := func(ctx context.Context, acc CodexAccount) error {
			pingCalled = true
			return nil
		}

		s.RunTick(context.Background(), settings, []CodexAccount{account}, fetchUsage, sendPing, nil)
		if pingCalled {
			t.Error("ping called when usage was zero")
		}
	})

	t.Run("does_not_ping_when_weekly_blocking_quota_is_exhausted", func(t *testing.T) {
		s := NewCodexReactiveScheduler()
		s.SetClock(func() time.Time { return baseTime })
		s.SetResetCache(map[string]string{
			"codex-1": "2026-01-01T17:00:00Z",
		})

		settings := map[string]any{
			"codexAutoPing": map[string]any{
				"connections": map[string]any{"codex-1": true},
			},
		}
		pingCalled := false

		fetchUsage := func(ctx context.Context, acc CodexAccount) (*CodexUsageReport, error) {
			return &CodexUsageReport{
				Quotas: map[string]QuotaEntry{
					"session": {Used: ptrFloat(1), Total: ptrFloat(100), Remaining: ptrFloat(99), ResetAt: "2026-01-01T17:01:00Z"},
					"weekly":  {Used: ptrFloat(100), Total: ptrFloat(100), Remaining: ptrFloat(0), ResetAt: "2026-01-03T12:00:00Z"},
				},
			}, nil
		}
		sendPing := func(ctx context.Context, acc CodexAccount) error {
			pingCalled = true
			return nil
		}

		s.RunTick(context.Background(), settings, []CodexAccount{account}, fetchUsage, sendPing, nil)
		if pingCalled {
			t.Error("ping called when weekly quota was exhausted")
		}
	})
}
