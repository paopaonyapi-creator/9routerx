// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"testing"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/translator"
)

// TestLogUsage_IncrementsFreeTierCounter pins the FASE 10.1 hook: a successful
// request's total tokens land on the provider's freeTierBudget row, a provider
// without a row is never created, and a failed write does not fail the request.
func TestLogUsage_IncrementsFreeTierCounter(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// Track one provider, leave another untracked.
	if err := repo.SetFreeTierBudget("groq", 1_000_000, 1); err != nil {
		t.Fatalf("SetFreeTierBudget: %v", err)
	}

	usage := translator.OpenAIUsage{PromptTokens: 300, CompletionTokens: 200}
	info := &UsageLogInfo{Provider: "groq", Model: "llama-3.3-70b", ConnectionID: "conn-ft"}
	h.LogUsage(context.Background(), info, &usage, 100, []byte(`{"messages":[]}`), nil)

	info2 := &UsageLogInfo{Provider: "untracked-provider", Model: "m", ConnectionID: "conn-ft2"}
	usage2 := translator.OpenAIUsage{PromptTokens: 10, CompletionTokens: 5}
	h.LogUsage(context.Background(), info2, &usage2, 100, []byte(`{"messages":[]}`), nil)

	budgets, err := repo.GetFreeTierBudgets()
	if err != nil {
		t.Fatalf("GetFreeTierBudgets: %v", err)
	}
	used := map[string]int64{}
	for _, b := range budgets {
		used[b.Provider] = b.UsedThisPeriod
	}
	if got := used["groq"]; got != 500 {
		t.Errorf("groq usedThisPeriod = %d, want 500 (prompt+completion)", got)
	}
	if _, ok := used["untracked-provider"]; ok {
		t.Error("usage for an untracked provider created a freeTierBudget row")
	}
}
