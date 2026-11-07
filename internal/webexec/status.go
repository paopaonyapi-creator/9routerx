// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package webexec

import (
	"time"

	"9routerx/proxy/internal/db"
)

// The four web-session executors the pool tracks. Their ids match the
// registrations in internal/proxy/executor.
const (
	ProviderDeepSeek = "deepseek-web"
	ProviderGemini   = "gemini-web"
	ProviderChatGPT  = "chatgpt-web"
	ProviderMuse     = "muse-web"
)

// Providers lists the web-executor provider ids in dashboard order.
var Providers = []string{ProviderDeepSeek, ProviderGemini, ProviderChatGPT, ProviderMuse}

// EnabledSettingsKey maps a provider to the settings-blob key the dashboard's
// toggle endpoint writes. Empty for an unknown provider.
func EnabledSettingsKey(provider string) string {
	switch provider {
	case ProviderDeepSeek:
		return "webExecDeepSeekEnabled"
	case ProviderGemini:
		return "webExecGeminiEnabled"
	case ProviderChatGPT:
		return "webExecChatGPTEnabled"
	case ProviderMuse:
		return "webExecMuseEnabled"
	default:
		return ""
	}
}

// ProviderEnabled reads the provider's settings switch. A nil settings row
// (a fresh install before the row exists) means off.
func ProviderEnabled(settings *db.SettingsData, provider string) bool {
	if settings == nil {
		return false
	}
	switch provider {
	case ProviderDeepSeek:
		return settings.WebExecDeepSeekEnabled
	case ProviderGemini:
		return settings.WebExecGeminiEnabled
	case ProviderChatGPT:
		return settings.WebExecChatGPTEnabled
	case ProviderMuse:
		return settings.WebExecMuseEnabled
	default:
		return false
	}
}

// SessionExpiredDedupeKey is the notifications.dedupeKey one provider
// account's expired session alerts under.
func SessionExpiredDedupeKey(provider, accountID string) string {
	return "webexec_session_expired:" + provider + ":" + accountID
}

// ProviderStatus is the dashboard wire shape for one web-executor provider.
type ProviderStatus struct {
	Provider           string `json:"provider"`
	Enabled            bool   `json:"enabled"`
	SessionValid       bool   `json:"sessionValid"`
	LastUsed           string `json:"lastUsed,omitempty"`
	RateLimitRemaining int    `json:"rateLimitRemaining"`
}

// BuildStatus composes one provider's dashboard status from the settings
// switch and the pool snapshot. A nil pool (health job never started) reports
// the settings switch with defaults, rather than inventing health state.
func BuildStatus(provider string, settings *db.SettingsData, pool *Pool) ProviderStatus {
	status := ProviderStatus{
		Provider: provider,
		Enabled:  ProviderEnabled(settings, provider),
	}
	if pool == nil {
		status.RateLimitRemaining = DefaultMaxReqPerMin
		return status
	}
	snap := pool.Snapshot(provider)
	status.SessionValid = snap.SessionValid
	status.RateLimitRemaining = snap.RateLimitRemaining
	if !snap.LastUsed.IsZero() {
		status.LastUsed = snap.LastUsed.UTC().Format(time.RFC3339)
	}
	return status
}
