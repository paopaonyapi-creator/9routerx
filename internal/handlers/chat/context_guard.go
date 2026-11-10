// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"

	"9routerx/proxy/internal/middleware"
	"9routerx/proxy/internal/providers"
)

type promptTokensContextKey struct{}

// WithEstimatedPromptTokens stores the pre-calculated prompt token count on ctx.
func WithEstimatedPromptTokens(ctx context.Context, tokens int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, promptTokensContextKey{}, tokens)
}

// promptTokensFrom returns the cached prompt tokens on ctx, or estimates them from body.
func promptTokensFrom(ctx context.Context, body []byte) int {
	if ctx != nil {
		if v, ok := ctx.Value(promptTokensContextKey{}).(int); ok && v > 0 {
			return v
		}
	}
	return middleware.EstimatePromptTokens(body)
}

// modelContextWindow resolves the authoritative context window for a provider/model.
func (h *ChatHandler) modelContextWindow(provider, model string) int {
	if h != nil {
		if cw, _ := h.declaredTokenLimitsFor(provider, model); cw > 0 {
			return cw
		}
	}
	caps := providers.GetCapabilitiesDetailForModel(provider, model)
	if caps.ContextWindow > 0 {
		return caps.ContextWindow
	}
	if cw, _ := providers.GetModelTokenLimits(model); cw > 0 {
		return cw
	}
	return 128000
}

// isContextExceeded checks if prompt tokens with 10% tolerance exceed target window.
func isContextExceeded(tokens, window int) bool {
	if window <= 0 || tokens <= 0 {
		return false
	}
	// 10% tolerance margin per A3 spec
	effective := int(float64(tokens) * 1.10)
	return effective > window
}

func (h *ChatHandler) contextGuardPolicy() (enabled bool, mode string) {
	if h == nil || h.Repo == nil {
		return true, "skip"
	}
	settings, err := h.Repo.GetSettings()
	if err != nil || settings == nil {
		return true, "skip"
	}
	mode = settings.ContextGuardMode
	if mode == "" {
		mode = "skip"
	}
	return settings.ContextGuardEnabled, mode
}

// contextGuardSkip reports whether the candidate model should be skipped because
// the prompt exceeds the model's context window.
func (h *ChatHandler) contextGuardSkip(ctx context.Context, body []byte, provider, model string) bool {
	enabled, mode := h.contextGuardPolicy()
	if !enabled || mode != "skip" {
		return false
	}
	window := h.modelContextWindow(provider, model)
	tokens := promptTokensFrom(ctx, body)
	return isContextExceeded(tokens, window)
}

// contextGuardTruncate reports whether the body should be truncated to fit the
// candidate model's context window.
func (h *ChatHandler) contextGuardTruncate(ctx context.Context, body []byte, provider, model string) bool {
	enabled, mode := h.contextGuardPolicy()
	if !enabled || mode != "truncate" {
		return false
	}
	window := h.modelContextWindow(provider, model)
	tokens := promptTokensFrom(ctx, body)
	return isContextExceeded(tokens, window)
}
