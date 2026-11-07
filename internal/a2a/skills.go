// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package a2a

import (
	"context"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/resilience"
)

// skillDef is one skill on the Agent Card.
type skillDef struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Examples    []string `json:"examples"`
}

// skillDefs is the Agent Card skill list, in card order. The set is
// deliberately read-only: every skill maps to one dashboard read, so the
// surface an external agent can touch is exactly the surface the dashboard
// already guards — no skill routes a chat completion or spends a token.
var skillDefs = []skillDef{
	{
		ID:          "list-keys",
		Name:        "List API Keys",
		Description: "Lists API keys with metadata (no raw secrets)",
		Tags:        []string{"keys", "security"},
		Examples:    []string{"List my API keys"},
	},
	{
		ID:          "health-check",
		Name:        "Health Check",
		Description: "Gateway health, version and read counts",
		Tags:        []string{"health"},
		Examples:    []string{"Check gateway health"},
	},
	{
		ID:          "list-models",
		Name:        "List Models",
		Description: "Lists custom models registered on this gateway",
		Tags:        []string{"models", "discovery"},
		Examples:    []string{"What models are available?"},
	},
	{
		ID:          "list-combos",
		Name:        "List Combos",
		Description: "Lists combo configurations with routing strategies",
		Tags:        []string{"combos", "routing"},
		Examples:    []string{"Show my combos"},
	},
	{
		ID:          "usage-report",
		Name:        "Usage Report",
		Description: "Aggregates usage and latency per provider over the last week",
		Tags:        []string{"usage", "cost"},
		Examples:    []string{"Report usage for the last 7 days"},
	},
	{
		ID:          "resilience-status",
		Name:        "Resilience Status",
		Description: "Circuit breaker state per provider",
		Tags:        []string{"health", "resilience"},
		Examples:    []string{"Show circuit breaker status"},
	},
}

// skillIDs lists the skill ids in card order, for the status endpoint.
func skillIDs() []string {
	ids := make([]string, 0, len(skillDefs))
	for _, s := range skillDefs {
		ids = append(ids, s.ID)
	}
	return ids
}

// skillFunc is one skill implementation: a read over the repo that returns the
// skill's payload or an error (surfaced as a failed task, not a protocol
// error).
type skillFunc func(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error)

// skillHandlers maps a skill id to its implementation. Read-only by contract;
// see skillDefs for what an agent can reach.
var skillHandlers = map[string]skillFunc{
	"list-keys":         skillListKeys,
	"health-check":      skillHealthCheck,
	"list-models":       skillListModels,
	"list-combos":       skillListCombos,
	"usage-report":      skillUsageReport,
	"resilience-status": skillResilienceStatus,
}

// skillListKeys mirrors the dashboard's key list: metadata only, never raw
// secrets.
func skillListKeys(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error) {
	keys, err := repo.GetApiKeys()
	if err != nil {
		return nil, err
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

// skillHealthCheck reports gateway identity plus the read counts a caller
// needs to decide whether this gateway has anything to offer.
func skillHealthCheck(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error) {
	keys, err := repo.GetApiKeys()
	if err != nil {
		return nil, err
	}
	combos, err := repo.GetCombos()
	if err != nil {
		return nil, err
	}
	conns, err := repo.GetProviderConnections("", true)
	if err != nil {
		return nil, err
	}
	entries, err := repo.GetSecurityLogs(ctx, 5, "")
	if err != nil {
		return nil, err
	}
	_, unread, err := repo.ListNotifications(ctx, 5)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":                    "ok",
		"name":                      agentName,
		"version":                   agentVersion(),
		"apiKeys":                   len(keys),
		"combos":                    len(combos),
		"activeProviderConnections": len(conns),
		"recentSecurityEvents":      len(entries),
		"unreadNotifications":       unread,
	}, nil
}

// skillListModels lists the custom models registered on this gateway. The live
// catalog (provider-builtin models included) is a dashboard endpoint, not a
// read, so the result points at it rather than duplicating it.
func skillListModels(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error) {
	models, err := repo.GetCustomModels()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		out = append(out, map[string]any{
			"providerAlias": m.ProviderAlias,
			"id":            m.ID,
			"name":          m.Name,
			"contextWindow": m.ContextWindow,
			"maxOutput":     m.MaxOutput,
		})
	}
	return map[string]any{
		"models": out,
		"total":  len(out),
		"note":   "Live catalog (including provider-builtin models) is served at GET /v1/models",
	}, nil
}

// skillListCombos mirrors the dashboard's combo list.
func skillListCombos(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error) {
	combos, err := repo.GetCombos()
	if err != nil {
		return nil, err
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

// skillUsageReport aggregates usageHistory per provider over the last week
// plus the mean-latency rows, so a caller sees both volume and responsiveness.
func skillUsageReport(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error) {
	rows, err := repo.GetUsageGroupByProviderSince(usageWindowStart())
	if err != nil {
		return nil, err
	}
	latency, err := repo.GetModelLatency()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"windowDays": usageWindowDays,
		"since":      usageWindowStart(),
		"providers":  rows,
		"latency":    latency,
	}, nil
}

// skillResilienceStatus reports the live circuit breaker state per provider.
func skillResilienceStatus(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error) {
	breakers := resilience.AllBreakerStatuses()
	open := 0
	for _, b := range breakers {
		if b.State == resilience.StateOpen {
			open++
		}
	}
	return map[string]any{
		"breakers": breakers,
		"open":     open,
		"total":    len(breakers),
	}, nil
}
