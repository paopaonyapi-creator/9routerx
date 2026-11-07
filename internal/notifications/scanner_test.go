// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package notifications

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
)

func openScannerTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.OpenDatabase(filepath.Join(dir, "scanner.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return db.NewRepo(conn)
}

func TestScannerConditionTriggers(t *testing.T) {
	repo := openScannerTestRepo(t)
	scanner := NewScanner(repo)

	baseTime := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	scanner.SetClock(func() time.Time { return baseTime })
	ctx := context.Background()

	t.Run("expired_key_triggers_notification", func(t *testing.T) {
		past := baseTime.Add(-1 * time.Hour).Format(time.RFC3339)
		if err := repo.CreateApiKey("k-exp", "sk-exp", "Expired Key", ""); err != nil {
			t.Fatalf("CreateApiKey: %v", err)
		}
		if err := repo.SetApiKeyExpiry("k-exp", past); err != nil {
			t.Fatalf("SetApiKeyExpiry: %v", err)
		}

		res, err := scanner.Scan(ctx)
		if err != nil {
			t.Fatalf("Scan error: %v", err)
		}

		found := false
		for _, k := range res.Created {
			if k == "key_expired:k-exp" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("key_expired:k-exp not triggered in scan: %+v", res.Created)
		}
	})

	t.Run("quota_pressure_triggers_notification", func(t *testing.T) {
		// Daily cost limit near cap
		if err := repo.CreateApiKey("k-cost", "sk-cost", "Cost Key", ""); err != nil {
			t.Fatalf("CreateApiKey: %v", err)
		}
		if err := repo.SetApiKeyDailyCostLimit("k-cost", 10.0); err != nil {
			t.Fatalf("SetApiKeyDailyCostLimit: %v", err)
		}
		// Spent 9.5 out of 10 -> 95% ratio (warning)
		if err := repo.BumpApiKeyCostSpent("k-cost", 9.5, "2026-11-05"); err != nil {
			t.Fatalf("BumpApiKeyCostSpent: %v", err)
		}

		res, err := scanner.Scan(ctx)
		if err != nil {
			t.Fatalf("Scan error: %v", err)
		}

		found := false
		for _, k := range res.Created {
			if k == "quota:k-cost:cost:warning" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("quota:k-cost:cost:warning not triggered in scan: %+v", res.Created)
		}

		// Spent 10.5 -> critical
		if err := repo.BumpApiKeyCostSpent("k-cost", 1.0, "2026-11-05"); err != nil {
			t.Fatalf("BumpApiKeyCostSpent: %v", err)
		}
		res, _ = scanner.Scan(ctx)
		foundCritical := false
		for _, k := range res.Created {
			if k == "quota:k-cost:cost:critical" {
				foundCritical = true
				break
			}
		}
		if !foundCritical {
			t.Fatalf("quota:k-cost:cost:critical not triggered in scan: %+v", res.Created)
		}
	})

	t.Run("critical_security_log_triggers_notification", func(t *testing.T) {
		secAt := baseTime.Add(-2 * time.Minute).Format(time.RFC3339)
		id, err := repo.InsertSecurityLog(ctx, secAt, "probe_detected", "critical", "192.168.1.50", "", "GET", "/.env", 404, "Secret file probe", "curl")
		if err != nil {
			t.Fatalf("InsertSecurityLog: %v", err)
		}

		res, err := scanner.Scan(ctx)
		if err != nil {
			t.Fatalf("Scan error: %v", err)
		}

		wantKey := fmt.Sprintf("security:critical:%d", id)
		found := false
		for _, k := range res.Created {
			if k == wantKey {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not triggered in scan: %+v", wantKey, res.Created)
		}
	})

	t.Run("provider_error_spike_triggers_notification", func(t *testing.T) {
		// Insert 12 failed usage rows in the last 5 minutes
		stamp := baseTime.Add(-3 * time.Minute).Format("2006-01-02 15:04:05")
		for i := 0; i < 12; i++ {
			if err := repo.InsertUsageHistory("openai", "gpt-4o", "conn-1", "sk-1", "/v1/chat/completions",
				10, 10, 0, "error", 20, "{}", "{}", ""); err != nil {
				t.Fatalf("InsertUsageHistory: %v", err)
			}
		}
		// Manually update timestamps to fall inside the 15m window
		_, err := repo.RawDB().ExecContext(ctx, `UPDATE usageHistory SET timestamp = ? WHERE status = 'error'`, stamp)
		if err != nil {
			t.Fatalf("update timestamp: %v", err)
		}

		res, err := scanner.Scan(ctx)
		if err != nil {
			t.Fatalf("Scan error: %v", err)
		}

		found := false
		for _, k := range res.Created {
			if len(k) > 13 && k[:13] == "spike:errors:" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("provider error spike not triggered in scan: %+v", res.Created)
		}
	})
}
