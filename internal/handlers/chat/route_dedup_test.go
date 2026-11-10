// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"testing"

	"9routerx/proxy/internal/db"
)

func TestRouteDedupAndAttemptCap(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	handler := NewChatHandler(repo)

	// 1. Initially disabled: dedup returns false, cap returns false.
	ctx := handler.attachRouteDedup(context.Background())
	body := []byte(`{"model":"combo-1","messages":[{"role":"user","content":"hello"}]}`)
	if seatRetryDedupSkip(ctx, body, "openai", "gpt-4o") {
		t.Error("dedup should be inactive when setting is disabled")
	}

	// 2. Enable retryDedupEnabled in settings.
	err := repo.UpdateSettingsRaw(map[string]any{
		"retryDedupEnabled": true,
		"attemptCap": map[string]any{
			"global": 3,
			"providers": map[string]any{
				"anthropic": 1,
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateSettingsRaw: %v", err)
	}

	ctx = handler.attachRouteDedup(context.Background())
	if seatRetryDedupSkip(ctx, body, "openai", "gpt-4o") {
		t.Error("fresh dedup context should not skip on first sight")
	}
	markRetryDedupSeat(ctx, body, "openai", "gpt-4o")
	if !seatRetryDedupSkip(ctx, body, "openai", "gpt-4o") {
		t.Error("dedup must skip marked (body, provider, model)")
	}
	// A different model or provider with same body should not skip
	if seatRetryDedupSkip(ctx, body, "openai", "gpt-4o-mini") {
		t.Error("dedup must not skip different model under same body")
	}

	// 3. Test attempt cap with FallbackAttemptRecorder
	rec := NewFallbackAttemptRecorder()
	ctx = WithFallbackAttemptRecorder(ctx, rec)

	if handler.attemptCapReached(ctx, "anthropic", "claude-3-5-sonnet") {
		t.Error("should not be reached at 0 attempts")
	}

	rec.Record(FallbackAttempt{
		Provider: "anthropic",
		Model:    "claude-3-5-sonnet",
	})

	// anthropic has provider cap of 1, so 1 attempt means reached
	if !handler.attemptCapReached(ctx, "anthropic", "claude-3-5-sonnet") {
		t.Error("anthropic should reach provider cap of 1 after 1 attempt")
	}

	// openai has no provider cap, global is 3, total attempts is 1, so openai is not reached
	if handler.attemptCapReached(ctx, "openai", "gpt-4o") {
		t.Error("openai should not reach cap with 1 global attempt")
	}

	rec.Record(FallbackAttempt{Provider: "openai", Model: "gpt-4o"})
	rec.Record(FallbackAttempt{Provider: "openai", Model: "gpt-4o-mini"})
	// total attempts now 3 -> global cap of 3 reached
	if !handler.attemptCapReached(ctx, "openai", "gpt-4o") {
		t.Error("openai should reach global cap of 3 after 3 total attempts")
	}
}
