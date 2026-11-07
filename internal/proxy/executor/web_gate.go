// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"9routerx/proxy/internal/handlerutil"
)

var webExecutorsGloballyEnabled atomic.Bool

// SetWebExecutorsEnabled controls the runtime master switch for web executors.
func SetWebExecutorsEnabled(v bool) {
	webExecutorsGloballyEnabled.Store(v)
}

// WebExecutorsEnabled reports whether grey-zone web session executors are permitted to run.
// Default is false (OFF). Set ENABLE_WEB_EXECUTORS=true or enableWebExecutors in settings to enable.
func WebExecutorsEnabled() bool {
	if webExecutorsGloballyEnabled.Load() {
		return true
	}
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_WEB_EXECUTORS")))
	return env == "true" || env == "1"
}

// CheckWebExecutorAllowed checks if web executors are permitted, writing an HTTP 403 error if blocked.
func CheckWebExecutorAllowed(w http.ResponseWriter, provider string) bool {
	if !WebExecutorsEnabled() {
		handlerutil.WriteJSONErrorCode(w, http.StatusForbidden, "web_executors_disabled",
			fmt.Sprintf("%s web session executor is disabled by policy (set ENABLE_WEB_EXECUTORS=true to enable)", provider))
		return false
	}
	return true
}
