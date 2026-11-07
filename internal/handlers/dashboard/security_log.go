// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"net/http"
	"strconv"
	"strings"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/securitylog"
)

// maxSecurityLogLimit caps the ?limit= parameter. The default already covers
// what the view renders; an unbounded value would let a caller walk the whole
// table (and its retained attacker-controlled paths) in one response.
const maxSecurityLogLimit = 5000

// HandleGetSecurityLog handles GET /api/security-log.
func (h *DashboardHandler) HandleGetSecurityLog(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if n, err := strconv.Atoi(rawLimit); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxSecurityLogLimit {
		limit = maxSecurityLogLimit
	}
	severity := strings.TrimSpace(r.URL.Query().Get("severity"))

	rows, err := h.Repo.GetSecurityLogs(r.Context(), limit, severity)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	enriched := securitylog.Enrich(rows)

	counts := map[string]int{"critical": 0, "warn": 0, "info": 0}
	classifications := map[string]int{"breach": 0, "suspicious": 0, "normal": 0}
	logins := map[string]int{"success": 0, "failed": 0, "viaApiKey": 0}

	for _, e := range enriched {
		counts[e.Level]++
		classifications[e.Classification]++
		if e.Type == "login_success" {
			logins["success"]++
		} else if e.Type == "login_failed" {
			logins["failed"]++
		} else if e.Type == "apikey_login_success" {
			logins["viaApiKey"]++
		}
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"events": enriched,
		"total":  len(enriched),
		"summary": map[string]any{
			"counts":          counts,
			"classifications": classifications,
			"logins":          logins,
			"total":           len(enriched),
		},
	})
}

// HandleClearSecurityLog handles DELETE /api/security-log.
func (h *DashboardHandler) HandleClearSecurityLog(w http.ResponseWriter, r *http.Request) {
	if err := h.Repo.ClearSecurityLogs(r.Context()); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"cleared": true})
}
