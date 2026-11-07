// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package dashboard

import (
	"net/http"
	"time"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/log"
)

// Free-tier live tracking (FASE 10.1) — dashboard surface.
//
// The curated catalog (providers, model lists, TOS verdicts, the evidence
// trail behind every figure) lives in the SPA: web/src/lib/freeTierCatalog.ts.
// This endpoint owns the half the SPA cannot know: what each tracked provider
// has actually served this period. The SPA joins the two by provider id, so
// the catalog keeps its single curated home and the counters keep one writer
// (the usage path).

// freeTierLiveRow is one provider's live counter as the SPA reads it.
type freeTierLiveRow struct {
	Provider     string `json:"provider"`
	MonthlyQuota int64  `json:"monthlyQuota"`
	ResetDay     int    `json:"resetDay"`
	Used         int64  `json:"used"`
	Remaining    int64  `json:"remaining"`
	// RemainingPct is 0-100; a tier without a published quota reports 100 so
	// the SPA's low-usage badge cannot fire on a number that means nothing.
	RemainingPct float64 `json:"remainingPct"`
	// Low is the pre-computed <10% remaining verdict, so the SPA does not
	// re-derive the threshold (and drift from it) on every poll.
	Low         bool   `json:"low"`
	LastUpdated string `json:"lastUpdated"`
}

// lowRemainingThreshold is the share of quota below which the SPA badges a
// provider as running out.
const lowRemainingThreshold = 10.0

// HandleGetFreeTierLive handles GET /api/free-tier/live: every tracked
// provider's budget and this period's spend, the numbers the free-tier page
// polls every 60 seconds.
func (h *DashboardHandler) HandleGetFreeTierLive(w http.ResponseWriter, r *http.Request) {
	budgets, err := h.Repo.GetFreeTierBudgets()
	if err != nil {
		log.Error("freetier", "read free tier budgets failed", "error", err)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to read free tier budgets")
		return
	}
	rows := make([]freeTierLiveRow, 0, len(budgets))
	for _, b := range budgets {
		remaining := b.MonthlyQuota - b.UsedThisPeriod
		if remaining < 0 {
			// A tier can overdraw its estimate (the quota is a published cap,
			// not an enforced ceiling); the page shows an exhausted tier, not
			// a negative promise.
			remaining = 0
		}
		pct := 100.0
		if b.MonthlyQuota > 0 {
			pct = float64(remaining) / float64(b.MonthlyQuota) * 100
		}
		rows = append(rows, freeTierLiveRow{
			Provider:     b.Provider,
			MonthlyQuota: b.MonthlyQuota,
			ResetDay:     b.ResetDay,
			Used:         b.UsedThisPeriod,
			Remaining:    remaining,
			RemainingPct: pct,
			Low:          b.MonthlyQuota > 0 && pct < lowRemainingThreshold,
			LastUpdated:  b.LastUpdated,
		})
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"providers":   rows,
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
	})
}
