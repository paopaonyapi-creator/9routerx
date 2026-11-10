// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"testing"

	"9routerx/proxy/internal/db"
)

func TestIsContextExceeded(t *testing.T) {
	tests := []struct {
		name    string
		tokens  int
		window  int
		wantExc bool
	}{
		{"fits easily", 1000, 128000, false},
		{"exact match hits 10pct tolerance", 1000, 1050, true}, // 1000 * 1.10 = 1100 > 1050
		{"fits with tolerance", 1000, 1150, false},            // 1100 <= 1150
		{"zero window passes through", 50000, 0, false},
		{"zero tokens passes through", 0, 4000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isContextExceeded(tt.tokens, tt.window); got != tt.wantExc {
				t.Errorf("isContextExceeded(%d, %d) = %v, want %v", tt.tokens, tt.window, got, tt.wantExc)
			}
		})
	}
}

func TestContextGuardSkipAndTruncate(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	handler := NewChatHandler(repo)

	// Default: enabled, mode = skip
	// Create a context with 50,000 estimated tokens
	ctx := WithEstimatedPromptTokens(context.Background(), 50000)
	body := []byte(`{}`)

	// Register custom model with small 4000 window
	customJSON := `{"providerAlias":"testprov","id":"small-model","type":"llm","name":"small-model","contextWindow":4000,"maxOutput":1000}`
	if _, err := database.Exec(`INSERT INTO kv (scope, key, value) VALUES ('customModels', 'testprov|small-model|llm', ?)`, customJSON); err != nil {
		t.Fatalf("seed customModels: %v", err)
	}

	if !handler.contextGuardSkip(ctx, body, "testprov", "small-model") {
		t.Error("contextGuardSkip should skip candidate when prompt (50k) > window (4k)")
	}

	// Large model with 128k window should not be skipped
	if handler.contextGuardSkip(ctx, body, "openai", "gpt-4o") {
		t.Error("contextGuardSkip should not skip candidate when prompt (50k) < window (128k)")
	}

	// Switch mode to truncate
	err := repo.UpdateSettingsRaw(map[string]any{
		"contextGuardEnabled": true,
		"contextGuardMode":    "truncate",
	})
	if err != nil {
		t.Fatalf("UpdateSettingsRaw: %v", err)
	}

	// In truncate mode, skip is false and truncate is true
	if handler.contextGuardSkip(ctx, body, "testprov", "small-model") {
		t.Error("contextGuardSkip should be false when mode is truncate")
	}
	if !handler.contextGuardTruncate(ctx, body, "testprov", "small-model") {
		t.Error("contextGuardTruncate should be true when mode is truncate and prompt exceeds window")
	}
}
