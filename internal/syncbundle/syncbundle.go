// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
// Package syncbundle exports and imports a portable snapshot of an
// installation's configuration — the CONFIG BUNDLE half of OmniRoute's
// "Sync Tokens & Config Bundle" feature (src/lib/sync/bundle.ts), ported for
// the `9routerx export-bundle` / `import-bundle` CLI pair instead of a REST
// endpoint.
//
// The bundle is a secret-aware snapshot, not a full backup (internal/backup
// already owns full restore): dashboard auth secrets are stripped, client API
// keys travel masked, and provider credentials are carried through the repo's
// live vault so an import re-seals them under the target install's key.
//
// # Losing the master key
//
// Encryption needs ROUTER_MASTER_KEY. Without one, Export still produces the
// plaintext JSON (an operator may want a portable, diffable snapshot) but
// Encrypt refuses: the bundle would otherwise land on disk carrying provider
// credentials with nothing saying so. Import accepts both shapes — the
// "sxb1:" prefix marks an encrypted blob, its absence a plaintext bundle.
package syncbundle

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	json "encoding/json/v2"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/models"
)

// BundleVersion is the schema version Export writes and Import requires.
// Bump it when a section's shape changes incompatibly; Import refuses a
// higher version rather than guessing at a future schema.
const BundleVersion = 1

// Gateway names the product a bundle came from. Import refuses a different
// value: the sections are this gateway's table shapes, and a foreign bundle
// would import into rows nothing reads.
const Gateway = "9RouterX"

// secretSettingKeys are the settings the bundle must never carry. Mirrors the
// dashboard's own secretSettingKeys (internal/handlers/dashboard/settings.go)
// plus the env-level auth secrets, so the list stays defensive against a
// future settings key rather than duplicating the dashboard's coupling here.
var secretSettingKeys = []string{
	"password",
	"oidcClientSecret",
	"jwtSecret",
	"apiKeySecret",
	"initialPassword",
	"masterKey",
}

// Bundle is one exported configuration snapshot. Sections a gateway does not
// know about are absent, and Import only ever touches the sections below —
// a bundle and a target install at different feature levels merge instead of
// one wiping the other.
type Bundle struct {
	Version   int               `json:"version"`
	CreatedAt string            `json:"createdAt"`
	Gateway   string            `json:"gateway"`
	Settings  map[string]any    `json:"settings,omitempty"`
	APIKeys   []KeyMeta         `json:"apiKeys,omitempty"`
	Conns     []ConnectionMeta  `json:"connections,omitempty"`
	Combos    []models.Combo    `json:"combos,omitempty"`
	Teams     []db.Team         `json:"teams,omitempty"`
	Webhooks  []db.WebhookTarget `json:"webhookTargets,omitempty"`
}

// KeyMeta is one client API key's metadata with the key value masked. The
// key column is the client-facing secret: a bundle carries what describes
// the key (limits, budget, expiry, plan), never a credential a stranger
// could replay from a stolen file.
type KeyMeta struct {
	ID        string  `json:"id"`
	KeyMasked string  `json:"keyMasked"`
	Name      *string `json:"name,omitempty"`
	MachineID *string `json:"machineId,omitempty"`
	IsActive  int     `json:"isActive"`
	CreatedAt string  `json:"createdAt"`
	// The governance fields, carried because the setters that restore them
	// all exist on the repo and a restored key must come back as restricted
	// as it was: a bundle that silently lifted a rate limit would turn a
	// config move into an accidental opening of the throttle.
	RateLimitRPM         *int    `json:"rateLimitRpm,omitempty"`
	RateLimitTPM         *int    `json:"rateLimitTpm,omitempty"`
	RateLimitConcurrency *int    `json:"rateLimitConcurrency,omitempty"`
	TokenQuota           *int    `json:"tokenQuota,omitempty"`
	QueueTimeoutMs       *int    `json:"queueTimeoutMs,omitempty"`
	LimitMicros          int64   `json:"limitMicros,omitempty"`
	LimitTokens          int64   `json:"limitTokens,omitempty"`
	BudgetPeriod         string  `json:"budgetPeriod,omitempty"`
	AlertPct             int     `json:"alertPct,omitempty"`
	HardCutoff           bool    `json:"hardCutoff,omitempty"`
	ExpiresAt            *string `json:"expiresAt,omitempty"`
	PlanID               *string `json:"planId,omitempty"`
	DailyCostLimit       float64 `json:"dailyCostLimit,omitempty"`
	IPWhitelist          string  `json:"ipWhitelist,omitempty"`
	SystemPrompt         string  `json:"systemPrompt,omitempty"`
	Permissions          string  `json:"permissions,omitempty"`
	RequestLimit         int64   `json:"requestLimit,omitempty"`
	RequestResetInterval int     `json:"requestResetInterval,omitempty"`
	BillingTeamID        string  `json:"billingTeamId,omitempty"`
}

// ConnectionMeta is one provider connection with its (hydrated) data payload.
// The payload carries the credentials the vault would re-seal on import, so
// the bundle — plaintext or encrypted — must be treated as a secret file.
// lastUsedAt and consecutiveUseCount are rotation bookkeeping, not config,
// and stay behind.
type ConnectionMeta struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	AuthType string `json:"authType"`
	Name     string `json:"name,omitempty"`
	Priority int    `json:"priority"`
	IsActive int    `json:"isActive"`
	Data     string `json:"data"`
}

// Export reads the repo's configuration sections into a plaintext JSON
// bundle. The repo must have a vault attached (repo.SetVault) for provider
// credentials to be included: without one the connections hydrate as they
// sit on disk, which is redacted on a sealed install.
func Export(repo *db.Repo) ([]byte, error) {
	if repo == nil {
		return nil, fmt.Errorf("syncbundle.Export: nil repo")
	}
	bundle := Bundle{
		Version:   BundleVersion,
		CreatedAt: nowUTC(),
		Gateway:   Gateway,
	}

	settings, err := repo.GetSettingsRaw()
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Export: settings: %w", err)
	}
	stripSecretSettings(settings)
	if len(settings) > 0 {
		bundle.Settings = settings
	}

	if bundle.Conns, err = exportConnections(repo); err != nil {
		return nil, err
	}
	if bundle.APIKeys, err = exportAPIKeys(repo); err != nil {
		return nil, err
	}

	combos, err := repo.GetCombos()
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Export: combos: %w", err)
	}
	// The smart rules live in their own column GetCombos does not return, so
	// they are fetched in one indexed read and attached here. A rule-less
	// combo stays rule-less in the bundle — the import never clears the
	// target's rules on its behalf.
	rules, err := repo.ComboRulesByID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Export: combo rules: %w", err)
	}
	for _, c := range combos {
		if c == nil {
			continue
		}
		if raw := rules[c.ID]; raw != "" && raw != "[]" {
			c.MatchRules = raw
		}
		bundle.Combos = append(bundle.Combos, *c)
	}

	// "all" keeps archived teams in the bundle: the key attribution history
	// names them, and a team missing on import would leave the budget report
	// pointing at nothing.
	teams, err := repo.ListTeams("all")
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Export: teams: %w", err)
	}
	bundle.Teams = teams

	webhooks, err := repo.ListWebhookTargets(context.Background())
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Export: webhook targets: %w", err)
	}
	bundle.Webhooks = webhooks

	// Deterministic output so the same state exports byte-identically — the
	// property an operator diffing two bundles (or checksumming one twice)
	// actually wants.
	return json.Marshal(bundle, json.Deterministic(true))
}

// exportConnections walks every connection row and re-reads it hydrated, so
// the data payload carries the credentials the sealed columns hold. Rows are
// id-listed first and closed before the per-row reads: the pool is four
// connections wide, and holding an open rows set across nested queries is
// how it deadlocks (see db.GetCombos for the same discipline).
func exportConnections(repo *db.Repo) ([]ConnectionMeta, error) {
	ids, err := queryColumn(repo.RawDB(), `SELECT id FROM providerConnections ORDER BY provider, id`)
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Export: list connections: %w", err)
	}
	out := make([]ConnectionMeta, 0, len(ids))
	for _, id := range ids {
		conn, err := repo.GetProviderConnectionByID(id)
		if err != nil {
			return nil, fmt.Errorf("syncbundle.Export: connection %s: %w", id, err)
		}
		if conn == nil {
			// Deleted between the two reads; the export just skips it.
			continue
		}
		meta := ConnectionMeta{
			ID:       conn.ID,
			Provider: conn.Provider,
			AuthType: conn.AuthType,
			Priority: derefInt(conn.Priority, 1),
			IsActive: conn.IsActive,
			Data:     conn.Data,
		}
		if conn.Name != nil {
			meta.Name = *conn.Name
		}
		out = append(out, meta)
	}
	return out, nil
}

// exportAPIKeys maps every key row to its masked metadata form.
func exportAPIKeys(repo *db.Repo) ([]KeyMeta, error) {
	keys, err := repo.GetApiKeys()
	if err != nil {
		return nil, fmt.Errorf("syncbundle.Export: api keys: %w", err)
	}
	out := make([]KeyMeta, 0, len(keys))
	for _, k := range keys {
		if k == nil {
			continue
		}
		meta := KeyMeta{
			ID:                   k.ID,
			KeyMasked:            maskKey(k.Key),
			Name:                 k.Name,
			MachineID:            k.MachineID,
			IsActive:             k.IsActive,
			CreatedAt:            k.CreatedAt,
			RateLimitRPM:         k.RateLimitRPM,
			RateLimitTPM:         k.RateLimitTPM,
			RateLimitConcurrency: k.RateLimitConcurrency,
			TokenQuota:           k.TokenQuota,
			QueueTimeoutMs:       k.QueueTimeoutMs,
			LimitMicros:          k.LimitMicros,
			LimitTokens:          k.LimitTokens,
			BudgetPeriod:         k.BudgetPeriod,
			AlertPct:             k.AlertPct,
			HardCutoff:           k.HardCutoff,
			ExpiresAt:            k.ExpiresAt,
			PlanID:               k.PlanID,
			DailyCostLimit:       k.DailyCostLimit,
			IPWhitelist:          k.IPWhitelist,
			SystemPrompt:         k.SystemPrompt,
			Permissions:          k.Permissions,
			RequestLimit:         k.RequestLimit,
			RequestResetInterval: k.RequestResetInterval,
		}
		// The billing attribution lives in its own table read per key; an
		// error here degrades to "no team" rather than failing the export —
		// the row still travels, just unattributed.
		meta.BillingTeamID = repo.BillingTeamIDForKey(k.ID)
		out = append(out, meta)
	}
	return out, nil
}

// stripSecretSettings removes the auth keys in place. Export owns the map it
// was handed (fresh from GetSettingsRaw), so no copy is needed.
func stripSecretSettings(settings map[string]any) {
	for _, key := range secretSettingKeys {
		delete(settings, key)
	}
}

// maskKey keeps the shape a human needs to recognise a key ("sk-ab…wxyz")
// and nothing a client could authenticate with.
func maskKey(key string) string {
	switch {
	case key == "":
		return ""
	case len(key) <= 8:
		return "****"
	default:
		return key[:3] + "****" + key[len(key)-4:]
	}
}

// queryColumn drains a single-text-column query fully before returning, so
// the caller can run per-row queries afterward without racing the pool.
func queryColumn(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, query string) ([]string, error) {
	rows, err := q.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// derefInt is the nil-safe pointer read the connection priority needs.
func derefInt(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

// nowUTC is the timestamp format the bundle header is stamped with.
func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }
