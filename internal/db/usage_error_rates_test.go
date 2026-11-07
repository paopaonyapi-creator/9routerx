// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"path/filepath"
	"testing"
)

func setupUsageErrorRatesTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "usage_rates.sqlite")
	conn, err := OpenDatabase(dbPath)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return NewRepo(conn)
}

func TestGetProviderHourlyErrorRatesAggregation(t *testing.T) {
	repo := setupUsageErrorRatesTestRepo(t)
	ctx := context.Background()

	// Seed hour 1: 2026-11-05 10:00:00 -> provider "openai": 3 successes in usageHistory, 1 error in requestDetails
	hour1 := "2026-11-05 10:15:00"
	for i := 0; i < 3; i++ {
		if err := repo.InsertUsageHistory("openai", "gpt-4o", "c1", "sk-1", "/v1/chat/completions",
			10, 10, 0, "success", 20, "{}", "{}", ""); err != nil {
			t.Fatalf("InsertUsageHistory: %v", err)
		}
	}
	// Update timestamps for hour 1
	_, _ = repo.RawDB().ExecContext(ctx, `UPDATE usageHistory SET timestamp = ? WHERE provider = 'openai'`, hour1)

	// Seed 1 error in requestDetails for hour 1
	if err := repo.InsertRequestDetail("rd-err-1", "openai", "gpt-4o", "c1", "error", "{}"); err != nil {
		t.Fatalf("InsertRequestDetail: %v", err)
	}
	_, _ = repo.RawDB().ExecContext(ctx, `UPDATE requestDetails SET timestamp = ? WHERE id = 'rd-err-1'`, hour1)

	// Seed hour 2: 2026-11-05 11:00:00 -> provider "anthropic": 1 success, 1 failure in usageHistory
	hour2 := "2026-11-05 11:20:00"
	if err := repo.InsertUsageHistory("anthropic", "claude-3-5", "c2", "sk-2", "/v1/messages",
		15, 15, 0, "success", 30, "{}", "{}", ""); err != nil {
		t.Fatalf("InsertUsageHistory: %v", err)
	}
	if err := repo.InsertUsageHistory("anthropic", "claude-3-5", "c2", "sk-2", "/v1/messages",
		15, 15, 0, "error", 30, "{}", "{}", ""); err != nil {
		t.Fatalf("InsertUsageHistory: %v", err)
	}
	_, _ = repo.RawDB().ExecContext(ctx, `UPDATE usageHistory SET timestamp = ? WHERE provider = 'anthropic'`, hour2)

	// Query aggregation since 2026-11-05 00:00:00
	rates, err := repo.GetProviderHourlyErrorRates(ctx, "2026-11-05 00:00:00")
	if err != nil {
		t.Fatalf("GetProviderHourlyErrorRates: %v", err)
	}

	if len(rates) != 2 {
		t.Fatalf("rates count = %d, want 2 (2 hours/providers)", len(rates))
	}

	// Verify hour 2 (anthropic): 2 total, 1 error -> 50%
	var anthropicRate *ProviderHourlyErrorRate
	var openaiRate *ProviderHourlyErrorRate
	for i := range rates {
		if rates[i].Provider == "anthropic" {
			anthropicRate = &rates[i]
		}
		if rates[i].Provider == "openai" {
			openaiRate = &rates[i]
		}
	}

	if anthropicRate == nil || anthropicRate.Total != 2 || anthropicRate.Errors != 1 || anthropicRate.ErrorRate != 0.5 {
		t.Errorf("anthropic rate mismatch: %+v", anthropicRate)
	}

	// Verify hour 1 (openai): 4 total (3 successes + 1 error), 1 error -> 25%
	if openaiRate == nil || openaiRate.Total != 4 || openaiRate.Errors != 1 || openaiRate.ErrorRate != 0.25 {
		t.Errorf("openai rate mismatch: %+v", openaiRate)
	}
}
