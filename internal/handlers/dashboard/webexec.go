// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"io"
	"net/http"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/webexec"
)

// FASE 10.0 web-executor session pool: the dashboard reads one status line
// per web-session executor (deepseek-web, gemini-web, chatgpt-web, muse-web),
// flips the per-provider settings switch, and triggers a manual validation
// pass. The pool itself and the 15-minute job live in internal/webexec; this
// file is the wire surface.

// webExecRunner returns the health runner the endpoints report on. The field
// wins so a test can inject its own runner; production falls back to the
// process-wide instance the app's start hook published.
func (h *DashboardHandler) webExecRunner() *webexec.HealthRunner {
	if h.WebExec != nil {
		return h.WebExec
	}
	return webexec.ActiveRunner()
}

// webExecPool returns the runner's pool, tolerating a runner that was never
// started: the status endpoint still reports the settings switches.
func (h *DashboardHandler) webExecPool() *webexec.Pool {
	if runner := h.webExecRunner(); runner != nil {
		return runner.Pool
	}
	return nil
}

// HandleGetWebExec handles GET /api/webexec.
//
// It returns one status line per known web executor: the settings switch, the
// pool's session verdict, the most recent use, and the worst-case remaining
// rate budget across the provider's accounts.
func (h *DashboardHandler) HandleGetWebExec(w http.ResponseWriter, r *http.Request) {
	settings, err := h.Repo.GetSettings()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pool := h.webExecPool()
	statuses := make([]webexec.ProviderStatus, 0, len(webexec.Providers))
	for _, provider := range webexec.Providers {
		statuses = append(statuses, webexec.BuildStatus(provider, settings, pool))
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"providers": statuses})
}

// HandleToggleWebExec handles PUT /api/webexec/{provider}/toggle.
//
// It writes the provider's webExec*Enabled settings field. The switch is the
// per-provider layer under the process-wide ENABLE_WEB_EXECUTORS gate: both
// must be on for an executor to serve.
func (h *DashboardHandler) HandleToggleWebExec(w http.ResponseWriter, r *http.Request) {
	provider := getURLParam(r, "provider")
	if webexec.EnabledSettingsKey(provider) == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "unknown web executor provider: "+provider)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	if err := h.Repo.UpdateSettingsRaw(map[string]any{webexec.EnabledSettingsKey(provider): req.Enabled}); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	settings, err := h.Repo.GetSettings()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, webexec.BuildStatus(provider, settings, h.webExecPool()))
}

// HandleTestWebExec handles POST /api/webexec/{provider}/test.
//
// It runs the health pass for one provider right now — the same validation
// the 15-minute job performs — and reports the per-account outcomes. A failed
// session is a 200 with ok=false: the endpoint worked, the session did not.
func (h *DashboardHandler) HandleTestWebExec(w http.ResponseWriter, r *http.Request) {
	provider := getURLParam(r, "provider")
	if webexec.EnabledSettingsKey(provider) == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "unknown web executor provider: "+provider)
		return
	}
	runner := h.webExecRunner()
	if runner == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "web executor health job is not running")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, runner.CheckProvider(r.Context(), provider))
}
