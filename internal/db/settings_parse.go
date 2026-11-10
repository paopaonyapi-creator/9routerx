// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package db

import (
	"9routerx/proxy/internal/handlerutil"
	json "encoding/json/v2"
)

// parseSettings parses the settings row. The bool is false when the blob is
// not readable.
//
// The row is one JSON blob with one section per feature, so the work is split
// into one function per section: a single function with sixty conditionals
// hides the field a section forgot to read.
func parseSettings(rawData string) (*SettingsData, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(rawData), &raw); err != nil {
		return nil, false
	}

	s := DefaultSettings()
	applyRTKSettings(s, raw)
	applyPromptSettings(s, raw)
	applyHeadroomSettings(s, raw)
	applyCacheSettings(s, raw)
	applyObservabilitySettings(s, raw)
	applyFallbackSettings(s, raw)
	applyComboStrategies(s, raw)
	applyProviderStrategies(s, raw)
	applyProviderThinking(s, raw)
	applyCapacityAdapters(s, raw)
	applyProviderOverrides(s, raw)
	applySeatAttemptBudget(s, raw)
	applyRetryDedupSettings(s, raw)
	applyContextGuardSettings(s, raw)
	applyBudgetFallbackSettings(s, raw)
	applyDispatchGuardSettings(s, raw)
	applyWebExecSettings(s, raw)
	applyHiddenModelsSettings(s, raw)
	return s, true
}

// applyHiddenModelsSettings reads the FASE 10.3 global model hide list. The
// value is carried verbatim (the chat filter parses the JSON array itself, so
// one malformed write hides nothing rather than failing the read); absent or
// wrongly typed means "" — hide nothing.
func applyHiddenModelsSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["hiddenModels"].(string); ok {
		s.HiddenModels = v
	}
}

// applyWebExecSettings reads the FASE 10.0 web-executor session-pool switches.
// Absent or wrongly typed means the default: the executors are grey-zone ToS
// surface and ship OFF, so a hand-edited settings blob must never widen them
// by accident. The rate ceiling only takes a positive value; the default (20)
// covers everything else.
func applyWebExecSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["webExecDeepSeekEnabled"].(bool); ok {
		s.WebExecDeepSeekEnabled = v
	}
	if v, ok := raw["webExecGeminiEnabled"].(bool); ok {
		s.WebExecGeminiEnabled = v
	}
	if v, ok := raw["webExecChatGPTEnabled"].(bool); ok {
		s.WebExecChatGPTEnabled = v
	}
	if v, ok := raw["webExecMuseEnabled"].(bool); ok {
		s.WebExecMuseEnabled = v
	}
	if v, ok := raw["webExecMaxReqPerMin"].(float64); ok && v > 0 {
		s.WebExecMaxReqPerMin = int(v)
	}
}

// applyDispatchGuardSettings reads the SSRF dispatch-guard opt-in. Absent or
// wrongly typed means the default (false): a hand-edited settings blob must
// never widen the dispatch guard by accident.
func applyDispatchGuardSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["allowPrivateProviderURLs"].(bool); ok {
		s.AllowPrivateProviderURLs = v
	}
}

func applySeatAttemptBudget(s *SettingsData, raw map[string]any) {
	if sab, ok := raw["seatAttemptBudgetMs"].(float64); ok && sab >= 0 {
		s.SeatAttemptBudgetMs = int(sab)
	}
}

// applyRetryDedupSettings reads the B6 zero-retry dedup switch and the
// attempt-cap tiers. Absent keys leave the defaults (dedup off, no cap),
// which is the behaviour every install had before these settings existed.
func applyRetryDedupSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["retryDedupEnabled"].(bool); ok {
		s.RetryDedupEnabled = v
	}
	ac, ok := raw["attemptCap"].(map[string]any)
	if !ok {
		return
	}
	if g, ok := ac["global"].(float64); ok && g >= 0 {
		s.AttemptCap.Global = int(g)
	}
	if provs, ok := ac["providers"].(map[string]any); ok && len(provs) > 0 {
		s.AttemptCap.Providers = make(map[string]int, len(provs))
		for k, v := range provs {
			if n, ok := v.(float64); ok && n >= 0 {
				s.AttemptCap.Providers[k] = int(n)
			}
		}
	}
	if models, ok := ac["models"].(map[string]any); ok && len(models) > 0 {
		s.AttemptCap.Models = make(map[string]int, len(models))
		for k, v := range models {
			if n, ok := v.(float64); ok && n >= 0 {
				s.AttemptCap.Models[k] = int(n)
			}
		}
	}
}

// applyContextGuardSettings reads the A3 context window guard settings.
func applyContextGuardSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["contextGuardEnabled"].(bool); ok {
		s.ContextGuardEnabled = v
	}
	if v, ok := raw["contextGuardMode"].(string); ok && v != "" {
		s.ContextGuardMode = v
	}
}

// applyBudgetFallbackSettings reads the C1 budget fallback (Mode Hemat) settings.
func applyBudgetFallbackSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["budgetFallbackEnabled"].(bool); ok {
		s.BudgetFallbackEnabled = v
	}
	if v, ok := raw["budgetFallbackModel"].(string); ok && v != "" {
		s.BudgetFallbackModel = v
	}
}

func applyRTKSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["rtkEnabled"].(bool); ok {
		s.RTKEnabled = v
	}
	if v := handlerutil.GetString(raw, "rtkMode"); v != "" {
		s.RTKMode = v
	}
	if v := handlerutil.GetString(raw, "rtkIntensity"); v != "" {
		s.RTKIntensity = v
	}
	if v, ok := raw["rtkMaxLines"].(float64); ok && v > 0 {
		s.RTKMaxLines = int(v)
	}
	if v, ok := raw["rtkMaxChars"].(float64); ok && v > 0 {
		s.RTKMaxChars = int(v)
	}
	if v, ok := raw["rtkDeduplicate"].(bool); ok {
		s.RTKDeduplicate = v
	}
	if cats, ok := raw["rtkCategories"].(map[string]any); ok {
		s.RTKCategories = make(map[string]bool, len(cats))
		for k, cv := range cats {
			if b, ok := cv.(bool); ok {
				s.RTKCategories[k] = b
			}
		}
	}
	if filts, ok := raw["rtkFilters"].(map[string]any); ok {
		s.RTKFilters = make(map[string]bool, len(filts))
		for k, fv := range filts {
			if b, ok := fv.(bool); ok {
				s.RTKFilters[k] = b
			}
		}
	}
	if v := handlerutil.GetString(raw, "rtkRawRetention"); v != "" {
		s.RTKRawRetention = v
	}
}

func applyPromptSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["cavemanEnabled"].(bool); ok {
		s.CavemanEnabled = v
	}
	if v := handlerutil.GetString(raw, "cavemanMode"); v != "" {
		s.CavemanMode = v
	}
	if lvl := handlerutil.GetString(raw, "cavemanLevel"); lvl != "" {
		s.CavemanLevel = lvl
	}
	if v, ok := raw["cavemanAutoClarity"].(bool); ok {
		s.CavemanAutoClarity = v
	}
	if v := handlerutil.GetString(raw, "cavemanLanguage"); v != "" {
		s.CavemanLanguage = v
	}
	if v, ok := raw["cavemanInputMode"].(bool); ok {
		s.CavemanInputMode = v
	}
	if v := handlerutil.GetString(raw, "cavemanPreserveKeywords"); v != "" {
		s.CavemanPreserveKeywords = v
	}
	if v, ok := raw["ponytailEnabled"].(bool); ok {
		s.PonytailEnabled = v
	}
	if lvl := handlerutil.GetString(raw, "ponytailLevel"); lvl != "" {
		s.PonytailLevel = lvl
	}
	if v, ok := raw["adhdEnabled"].(bool); ok {
		s.ADHDEnabled = v
	}
	if lvl := handlerutil.GetString(raw, "adhdLevel"); lvl != "" {
		s.ADHDLevel = lvl
	}
	if lvl := handlerutil.GetString(raw, "antislopLevel"); lvl != "" {
		s.AntislopLevel = lvl
	}
	if v, ok := raw["systemPromptEnabled"].(bool); ok {
		s.SystemPromptEnabled = v
	}
	if v := handlerutil.GetString(raw, "systemPromptPrefix"); v != "" {
		s.SystemPromptPrefix = v
	}
	if v := handlerutil.GetString(raw, "systemPromptSuffix"); v != "" {
		s.SystemPromptSuffix = v
	}
}

func applyHeadroomSettings(s *SettingsData, raw map[string]any) {
	if v := handlerutil.GetString(raw, "headroomUrl"); v != "" {
		s.HeadroomUrl = v
	}
	if v, ok := raw["headroomEnabled"].(bool); ok {
		s.HeadroomEnabled = v
	}
	if v, ok := raw["headroomCodeAware"].(bool); ok {
		s.HeadroomCodeAware = v
	}
	if v, ok := raw["headroomKompress"].(bool); ok {
		s.HeadroomKompress = v
	}
	if v, ok := raw["headroomTimeoutMs"].(float64); ok && v > 0 {
		s.HeadroomTimeoutMs = int(v)
	}
}

func applyCacheSettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["semanticCacheEnabled"].(bool); ok {
		s.SemanticCacheEnabled = v
	}
	if v, ok := raw["semanticCacheTTL"].(float64); ok && v > 0 {
		s.SemanticCacheTTL = int(v)
	}
	if v, ok := raw["semanticCacheMaxEntries"].(float64); ok && v > 0 {
		s.SemanticCacheMaxEntries = int(v)
	}
	if v, ok := raw["autoUpdate"].(bool); ok {
		s.AutoUpdate = v
	}
	// Absent means the default (ON): an install that has never written the key
	// still checks for releases. Only an explicit false turns the check off.
	if v, ok := raw["updateCheckEnabled"].(bool); ok {
		s.UpdateCheckEnabled = v
	}
}

func applyObservabilitySettings(s *SettingsData, raw map[string]any) {
	if v, ok := raw["enableObservability"].(bool); ok {
		s.EnableObservability = v
	}
	if v := handlerutil.GetString(raw, "observabilityMode"); v != "" {
		s.ObservabilityMode = v
	}
	if v := settingsInt(raw["retentionDays"]); v >= 0 && raw["retentionDays"] != nil {
		s.RetentionDays = v
	}
	if v := settingsInt(raw["maxRequestRecords"]); v > 0 {
		s.MaxRequestRecords = v
	}
	// The ledger window. Explicit 0 means "keep forever" and is the default, so
	// the value is only taken when the key is actually present — a missing key
	// must not read as an instruction to prune at zero days.
	if _, present := raw["usageHistoryRetentionDays"]; present {
		s.UsageHistoryRetentionDays = settingsInt(raw["usageHistoryRetentionDays"])
	}
}

func applyFallbackSettings(s *SettingsData, raw map[string]any) {
	if fs := handlerutil.GetString(raw, "fallbackStrategy"); fs != "" {
		s.FallbackStrategy = fs
	}
	if v, ok := raw["stickyRoundRobinLimit"].(float64); ok && v > 0 {
		s.StickyRoundRobinLimit = int(v)
	}
	if v, ok := raw["forceFallback"].(bool); ok {
		s.ForceFallback = v
	}
	if cs := handlerutil.GetString(raw, "comboStrategy"); cs != "" {
		s.ComboStrategy = cs
	}
	if v, ok := raw["comboStickyRoundRobinLimit"].(float64); ok && v > 0 {
		s.ComboStickyRoundRobinLimit = int(v)
	}
}
