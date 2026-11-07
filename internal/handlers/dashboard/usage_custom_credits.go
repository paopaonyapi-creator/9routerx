// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	json "encoding/json/v2"
)

const defaultQuotaPath = "/credits"

func isCustomQuotaProvider(provider string, psd map[string]any) bool {
	if strings.HasPrefix(provider, "openai-compatible-") || strings.HasPrefix(provider, "anthropic-compatible-") {
		return true
	}
	if psd != nil {
		if qp, ok := psd["quotaPath"].(string); ok && strings.TrimSpace(qp) != "" {
			return true
		}
	}
	return false
}

func normalizeQuotaPath(val any) string {
	s, _ := val.(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultQuotaPath
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	if !strings.HasPrefix(s, "/") {
		return "/" + s
	}
	return s
}

func providerLabel(psd map[string]any, data map[string]any) string {
	if psd != nil {
		if name, ok := psd["nodeName"].(string); ok && strings.TrimSpace(name) != "" {
			return name
		}
	}
	if data != nil {
		if nick, ok := data["nickname"].(string); ok && strings.TrimSpace(nick) != "" {
			return nick
		}
	}
	return "Custom provider"
}

func toFloatNumber(val any) (float64, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, true
		}
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func firstFiniteNumber(m map[string]any, keys ...string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	for _, k := range keys {
		if val, exists := m[k]; exists {
			if num, ok := toFloatNumber(val); ok {
				return num, true
			}
		}
	}
	return 0, false
}

// fetchCustomCreditsUsage fetches balance and remaining credits for custom compatible providers.
// Parity with serenhope open-sse/services/usage/customCredits.js.
func fetchCustomCreditsUsage(ctx context.Context, provider string, data map[string]any) (usageResult, bool) {
	_, apiKey, psd := usageCreds(data)
	if !isCustomQuotaProvider(provider, psd) {
		return usageResult{}, false
	}

	baseURL := ""
	if psd != nil {
		if u, ok := psd["baseUrl"].(string); ok {
			baseURL = strings.TrimSpace(u)
		}
	}
	if baseURL == "" && data != nil {
		if u, ok := data["baseUrl"].(string); ok {
			baseURL = strings.TrimSpace(u)
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	if baseURL == "" {
		return usageResult{message: fmt.Sprintf("%s: base URL is missing.", provider), bare: true}, true
	}
	if apiKey == "" {
		if token, ok := data["accessToken"].(string); ok {
			apiKey = strings.TrimSpace(token)
		}
	}
	if apiKey == "" {
		return usageResult{message: fmt.Sprintf("%s: API key is missing.", provider), bare: true}, true
	}

	quotaPath := normalizeQuotaPath(psd["quotaPath"])
	targetURL := quotaPath
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = baseURL + quotaPath
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return usageResult{message: fmt.Sprintf("Quota request failed: %v", err), bare: true}, true
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return usageResult{message: fmt.Sprintf("Quota request failed: %v", err), bare: true}, true
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return usageResult{
			message: fmt.Sprintf("%s: quota endpoint authentication failed. Check the API key.", provider),
			bare:    true,
		}, true
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errSnippet := strings.TrimSpace(string(bodyBytes))
		if len(errSnippet) > 120 {
			errSnippet = errSnippet[:120]
		}
		msg := fmt.Sprintf("%s: quota API error (%d)", provider, resp.StatusCode)
		if errSnippet != "" {
			msg += ": " + errSnippet
		}
		return usageResult{message: msg, bare: true}, true
	}

	var jsonMap map[string]any
	if err := json.Unmarshal(bodyBytes, &jsonMap); err != nil || jsonMap == nil {
		return usageResult{message: fmt.Sprintf("%s: quota endpoint did not return JSON.", provider), bare: true}, true
	}

	credits, hasCredits := firstFiniteNumber(jsonMap, "credits", "remaining", "remaining_credits", "balance")
	total, hasTotal := firstFiniteNumber(jsonMap, "total", "quota_total")
	used, hasUsed := firstFiniteNumber(jsonMap, "used", "quota_used")

	if !hasCredits {
		if hasTotal && hasUsed {
			credits = total - used
			hasCredits = true
		} else {
			return usageResult{
				message: fmt.Sprintf("%s: quota response must include a numeric credits field.", provider),
				bare:    true,
			}, true
		}
	}

	if !hasTotal {
		addUsed := 0.0
		if hasUsed {
			addUsed = used
		}
		total = math.Max(0, credits+addUsed)
	}
	if !hasUsed {
		used = math.Max(0, total-credits)
	}

	remaining := math.Max(0, credits)
	remainingPercentage := 0.0
	if total > 0 {
		remainingPercentage = math.Max(0, math.Min(100, (remaining/total)*100.0))
	} else if remaining > 0 {
		remainingPercentage = 100.0
	}

	quotas := map[string]any{
		"Credits": map[string]any{
			"used":                used,
			"total":               total,
			"remainingPercentage": remainingPercentage,
			"resetAt":             nil,
			"unlimited":           false,
			"isCreditBalance":     true,
			"currency":            "credits",
		},
	}

	return usageResult{
		plan:   providerLabel(psd, jsonMap),
		quotas: quotas,
	}, true
}
