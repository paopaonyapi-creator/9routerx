// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"net/http"
	"time"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/providers"
)

// ProviderHealthResponse is one free provider's health row for the dashboard's
// "Free Provider Health" panel.
type ProviderHealthResponse struct {
	Provider       string  `json:"provider"`
	Status         string  `json:"status"` // healthy | flaky | cooling
	CoolingUntil   string  `json:"coolingUntil,omitempty"`
	CoolingOpen    bool    `json:"coolingOpenEnded,omitempty"`
	SuccessRatePct float64 `json:"successRatePct"` // -1 = no data in window
	Samples        int     `json:"samples"`
	Recent429      int     `json:"recent429"`
	Recent403      int     `json:"recent403"`
	P95LatencyMs   float64 `json:"p95LatencyMs"`
	Regime         string  `json:"regime,omitempty"`
	FreeModels     int     `json:"freeModels"`
}

// ProvidersHealthResponse is GET /api/providers/health's payload.
type ProvidersHealthResponse struct {
	Providers  []ProviderHealthResponse `json:"providers"`
	CoolingNow int                      `json:"coolingNow"`
	GeneratedAt string                  `json:"generatedAt"`
}

// HandleGetProvidersHealth handles GET /api/providers/health.
//
// The numbers come from the same tracker the request path and the fallback
// chain read, so what the panel shows is what the chain would act on — not a
// second estimate that can drift from the gate. The roster is the shipped
// free catalogue plus every provider the tracker has outcomes for, so a
// provider that has never been tried still appears (with no data) instead of
// the panel silently shrinking.
func (h *DashboardHandler) HandleGetProvidersHealth(w http.ResponseWriter, r *http.Request) {
	if h.FreeResilience == nil {
		handlerutil.WriteJSON(w, http.StatusOK, ProvidersHealthResponse{
			Providers:   []ProviderHealthResponse{},
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		})
		return
	}

	catalog := providers.FreeCatalogProviders()
	counts := providers.FreeCatalogModelCounts()
	snapshot := h.FreeResilience.HealthSnapshot(catalog, func(provider, model string) string {
		regime, _ := providers.FreeRegimeFor(provider, model)
		return regime
	})

	out := make([]ProviderHealthResponse, 0, len(snapshot))
	cooling := 0
	for _, row := range snapshot {
		entry := ProviderHealthResponse{
			Provider:       row.Provider,
			Status:         row.Status,
			CoolingUntil:   row.CoolingUntil,
			CoolingOpen:    row.CoolingOpen,
			SuccessRatePct: row.SuccessRatePct,
			Samples:        row.Samples,
			Recent429:      row.RecentRate429,
			Recent403:      row.Recent403,
			P95LatencyMs:   row.P95LatencyMs,
			Regime:         row.Regime,
			FreeModels:     counts[row.Provider],
		}
		if entry.Regime == "" {
			if regime, ok := providers.FreeRegimeFor(row.Provider, ""); ok {
				entry.Regime = regime
			}
		}
		if entry.Status == "cooling" {
			cooling++
		}
		out = append(out, entry)
	}

	handlerutil.WriteJSON(w, http.StatusOK, ProvidersHealthResponse{
		Providers:   out,
		CoolingNow:  cooling,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleResetProvidersHealth clears every cooldown the free-tier suite holds,
// on explicit operator action. The next request to a cleared provider dials it
// immediately instead of waiting out the window.
func (h *DashboardHandler) HandleResetProvidersHealth(w http.ResponseWriter, r *http.Request) {
	if h.FreeResilience == nil {
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"cleared": []string{}})
		return
	}
	cleared := h.FreeResilience.ClearAllCooldowns("operator reset")
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"cleared":    cleared,
		"clearedNow": len(cleared),
	})
}
