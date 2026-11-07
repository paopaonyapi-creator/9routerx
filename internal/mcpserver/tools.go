// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package mcpserver

import (
	"context"
	"encoding/json"
)

// executeTool dispatches one tools/call to its implementation. Every tool
// returns a map[string]any (the result content) or an error (surfaced as a
// tool-level error in the response, not a JSON-RPC protocol error).
func (s *MCPServer) executeTool(ctx context.Context, name string, args json.RawMessage) (any, *jsonrpcError) {
	if s.repo == nil {
		return nil, &jsonrpcError{Code: errInternal, Message: "repo not available"}
	}

	var argMap map[string]any
	if len(args) > 0 {
		_ = json.Unmarshal(args, &argMap)
	}

	switch name {
	case "list_keys":
		return s.toolListKeys(ctx)
	case "get_health":
		return s.toolGetHealth(ctx)
	case "list_models":
		return s.toolListModels(ctx)
	case "list_combos":
		return s.toolListCombos(ctx)
	case "cache_stats":
		return s.toolCacheStats(ctx)
	case "usage_summary":
		return s.toolUsageSummary(ctx)
	case "security_log":
		return s.toolSecurityLog(ctx, argMap)
	case "notifications":
		return s.toolNotifications(ctx)
	case "resilience_status":
		return s.toolResilienceStatus(ctx)
	case "team_usage":
		return s.toolTeamUsage(ctx, argMap)
	case "backup_create":
		return s.toolBackupCreate(ctx)
	case "quota_check":
		return s.toolQuotaCheck(ctx)
	case "list_providers":
		return s.toolListProviders(ctx)
	case "get_settings":
		return s.toolGetSettings(ctx)
	default:
		return nil, &jsonrpcError{Code: errInvalidRequest, Message: "unknown tool: " + name}
	}
}

func (s *MCPServer) toolError(err error) *jsonrpcError {
	return &jsonrpcError{Code: errInternal, Message: err.Error()}
}

func (s *MCPServer) toolListKeys(ctx context.Context) (any, *jsonrpcError) {
	keys, err := s.repo.GetApiKeys()
	if err != nil {
		return nil, s.toolError(err)
	}
	type keyMeta struct {
		ID        string  `json:"id"`
		Name      *string `json:"name"`
		IsActive  int     `json:"isActive"`
		ExpiresAt *string `json:"expiresAt"`
		UsedCount *int    `json:"usedCount"`
		Display   *string `json:"display"`
	}
	out := make([]keyMeta, 0, len(keys))
	for _, k := range keys {
		if k == nil {
			continue
		}
		out = append(out, keyMeta{
			ID: k.ID, Name: k.Name, IsActive: k.IsActive,
			ExpiresAt: k.ExpiresAt, UsedCount: k.UsedCount, Display: k.KeyDisplay,
		})
	}
	return map[string]any{"keys": out, "total": len(out)}, nil
}

func (s *MCPServer) toolGetHealth(ctx context.Context) (any, *jsonrpcError) {
	return map[string]any{"status": "ok", "name": "9RouterX"}, nil
}

func (s *MCPServer) toolListModels(ctx context.Context) (any, *jsonrpcError) {
	return map[string]any{"note": "Use GET /v1/models for the live catalog"}, nil
}

func (s *MCPServer) toolListCombos(ctx context.Context) (any, *jsonrpcError) {
	combos, err := s.repo.GetCombos()
	if err != nil {
		return nil, s.toolError(err)
	}
	type comboMeta struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Strategy string `json:"strategy"`
		Models   string `json:"models"`
	}
	out := make([]comboMeta, 0, len(combos))
	for _, c := range combos {
		if c == nil {
			continue
		}
		out = append(out, comboMeta{ID: c.ID, Name: c.Name, Strategy: c.Strategy, Models: c.Models})
	}
	return map[string]any{"combos": out, "total": len(out)}, nil
}

func (s *MCPServer) toolCacheStats(ctx context.Context) (any, *jsonrpcError) {
	return map[string]any{"note": "Use GET /api/cache for live semantic cache stats"}, nil
}

func (s *MCPServer) toolUsageSummary(ctx context.Context) (any, *jsonrpcError) {
	stats, err := s.repo.GetModelLatency()
	if err != nil {
		return nil, s.toolError(err)
	}
	return map[string]any{"latencyRows": len(stats), "note": "Use GET /api/usage/stats for the full dashboard summary"}, nil
}

func (s *MCPServer) toolSecurityLog(ctx context.Context, args map[string]any) (any, *jsonrpcError) {
	limit := 50
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	entries, err := s.repo.GetSecurityLogs(ctx, limit, "")
	if err != nil {
		return nil, s.toolError(err)
	}
	return map[string]any{"events": entries, "total": len(entries)}, nil
}

func (s *MCPServer) toolNotifications(ctx context.Context) (any, *jsonrpcError) {
	list, unread, err := s.repo.ListNotifications(ctx, 20)
	if err != nil {
		return nil, s.toolError(err)
	}
	return map[string]any{"notifications": list, "unread": unread}, nil
}

func (s *MCPServer) toolResilienceStatus(ctx context.Context) (any, *jsonrpcError) {
	return map[string]any{"note": "Use GET /api/resilience/status for live breaker states"}, nil
}

func (s *MCPServer) toolTeamUsage(ctx context.Context, args map[string]any) (any, *jsonrpcError) {
	teamID, _ := args["team_id"].(string)
	if teamID == "" {
		return nil, &jsonrpcError{Code: errInvalidRequest, Message: "team_id is required"}
	}
	report, err := s.repo.TeamUsageReport(ctx, teamID, "", "")
	if err != nil {
		return nil, s.toolError(err)
	}
	return report, nil
}

func (s *MCPServer) toolBackupCreate(ctx context.Context) (any, *jsonrpcError) {
	return map[string]any{"note": "Use POST /api/settings/auto-backup with action=backup"}, nil
}

func (s *MCPServer) toolQuotaCheck(ctx context.Context) (any, *jsonrpcError) {
	return map[string]any{"note": "Use GET /api/usage/{connectionId} for per-connection quota"}, nil
}

func (s *MCPServer) toolListProviders(ctx context.Context) (any, *jsonrpcError) {
	conns, err := s.repo.GetProviderConnections("", true)
	if err != nil {
		return nil, s.toolError(err)
	}
	type connMeta struct {
		ID       string  `json:"id"`
		Provider string  `json:"provider"`
		Name     *string `json:"name"`
		IsActive int     `json:"isActive"`
	}
	out := make([]connMeta, 0, len(conns))
	for _, c := range conns {
		if c == nil {
			continue
		}
		out = append(out, connMeta{ID: c.ID, Provider: c.Provider, Name: c.Name, IsActive: c.IsActive})
	}
	return map[string]any{"providers": out, "total": len(out)}, nil
}

func (s *MCPServer) toolGetSettings(ctx context.Context) (any, *jsonrpcError) {
	settings, err := s.repo.GetSettings()
	if err != nil {
		return nil, s.toolError(err)
	}
	return map[string]any{
		"rtkEnabled":           settings.RTKEnabled,
		"cavemanEnabled":       settings.CavemanEnabled,
		"semanticCacheEnabled": settings.SemanticCacheEnabled,
		"autoUpdate":           settings.AutoUpdate,
		"note":                 "Full settings via GET /api/settings",
	}, nil
}
