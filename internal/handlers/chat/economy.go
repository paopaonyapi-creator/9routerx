// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"strings"

	"9routerx/proxy/internal/log"
)

// resolveEconomyModel determines the fallback model when a request is downgraded
// under Mode Hemat (C1 budget fallback).
func (h *ChatHandler) resolveEconomyModel(original string) string {
	if h != nil && h.Repo != nil {
		if s, err := h.Repo.GetSettings(); err == nil && s != nil {
			if s.BudgetFallbackModel != "" {
				return s.BudgetFallbackModel
			}
		}
	}
	// If the original model is already a free tier model, keep it.
	if strings.Contains(original, "-free") || strings.Contains(original, "free/") {
		return original
	}
	log.Debug("economy", "resolving economy model for budget fallback", "original", original)
	// Default economy model is opencode/space-bunny-free
	return "opencode/space-bunny-free"
}
