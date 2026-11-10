// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"testing"

	"9routerx/proxy/internal/db"
)

func TestResolveEconomyModel(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	handler := NewChatHandler(repo)

	// 1. By default, falls back to opencode/space-bunny-free
	if got := handler.resolveEconomyModel("claude-3-5-sonnet"); got != "opencode/space-bunny-free" {
		t.Errorf("resolveEconomyModel default = %q, want opencode/space-bunny-free", got)
	}

	// 2. If original is already free, keeps it
	if got := handler.resolveEconomyModel("opencode/my-model-free"); got != "opencode/my-model-free" {
		t.Errorf("resolveEconomyModel for free model = %q, want opencode/my-model-free", got)
	}

	// 3. If settings specifies custom BudgetFallbackModel, honors it
	err := repo.UpdateSettingsRaw(map[string]any{
		"budgetFallbackModel": "gemini-2.5-flash",
	})
	if err != nil {
		t.Fatalf("UpdateSettingsRaw: %v", err)
	}

	if got := handler.resolveEconomyModel("gpt-4o"); got != "gemini-2.5-flash" {
		t.Errorf("resolveEconomyModel with setting = %q, want gemini-2.5-flash", got)
	}
}
