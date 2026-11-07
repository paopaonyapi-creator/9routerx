// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"net/http"
	"os"
	"strings"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/updater"
)

// Indirection seams the tests swap. Production points at the real engine; no
// HTTP surface can write either, so the update channel stays unredirectable
// from outside the process (docs/updater-contract.md, anti-SSRF).
var (
	updateCheckFn = updater.EnsureFreshCheck
	updateDebugFn = updater.BuildDebugDump
)

// UpdateCheckResponse is the wire shape of GET /api/update/check: the merged
// UpdateInfo contract plus how the answer was produced. The dashboard banner
// reads channel/hasUpdate/releaseUrl/sha256; everything else is there so the
// UI can say "checked 2 minutes ago from cache" instead of guessing.
type UpdateCheckResponse struct {
	*updater.UpdateInfo
	Channel string `json:"channel"`
	// CacheHit: served from the in-memory cache, no fetch happened for THIS
	// request. The endpoint never fetches per request; it only triggers a
	// refresh when the cache is empty or older than the freshness window.
	CacheHit bool `json:"cacheHit"`
	// InFlight: another dashboard tab (or the ticker) already had a fetch
	// running; this answer is the cache as it stood.
	InFlight bool `json:"inFlight"`
	// CheckEnabled mirrors the updateCheckEnabled setting. When false the
	// endpoint serves the last cache and refuses to dial out.
	CheckEnabled bool `json:"checkEnabled"`
	// Stale: the answer is older than the freshness window (a refresh was
	// triggered, or the last refresh failed and the old answer is all there is).
	Stale bool `json:"stale"`
}

// HandleUpdateCheck handles GET /api/update/check (dashboard group: session,
// API key or CLI token). Serves the engine cache; triggers a fetch only when
// the cache is empty/stale, bounded by updater.FetchTimeout (5s). A failing
// fetch is NOT a failing endpoint: the response still carries the last known
// state, and the cause went to the structured log and /api/update/debug.
func (h *DashboardHandler) HandleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	outcome := updateCheckFn(r.Context(), "dashboard")
	info := outcome.Info
	if info == nil {
		// Never checked and the check is off (or the very first request raced
		// the ticker): answer with the local version and no update claim rather
		// than an error — absence of a check is not an update failure.
		info = &updater.UpdateInfo{CurrentVersion: updater.CurrentVersion}
	}
	handlerutil.WriteJSON(w, http.StatusOK, UpdateCheckResponse{
		UpdateInfo:   info,
		Channel:      string(outcome.Channel),
		CacheHit:     outcome.CacheHit,
		InFlight:     outcome.InFlight,
		CheckEnabled: outcome.CheckEnabled,
		Stale:        outcome.Stale,
	})
}

// updateDebugEndpointEnv gates GET /api/update/debug. Off by default: the dump
// reveals the updater's internal state (last raw response, decision trail),
// which is a debugging surface, not a dashboard feature.
const updateDebugEndpointEnv = "UPDATE_DEBUG_ENDPOINT"

// HandleUpdateDebug handles GET /api/update/debug. Registered behind
// RequireAdminAuth (admin session or CLI token) AND the UPDATE_DEBUG_ENDPOINT=1
// env flag — both must hold, so the default install answers 404 and the
// endpoint cannot be discovered by probing.
func (h *DashboardHandler) HandleUpdateDebug(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv(updateDebugEndpointEnv)), "1") {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "not found")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, updateDebugFn())
}
