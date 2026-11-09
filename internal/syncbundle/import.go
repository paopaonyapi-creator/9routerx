// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Bundle import: decrypt (or accept plaintext), validate the schema, and
// upsert every section through the repo's own setters.
//
// Idempotency is the contract: every section upserts by its natural key, so
// importing the same bundle twice lands the same state and reports zeros in
// the created columns the second time. Nothing is deleted and nothing is
// merged away — a section the bundle does not carry, or a row the bundle does
// not name, stays exactly as the target install has it. That is what makes
// this a config sync and not a restore: internal/backup already owns the
// wipe-and-replace flavour.
package syncbundle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	json "encoding/json/v2"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/models"
)

// SectionCount is one section's import outcome. Skipped counts entries too
// malformed to apply (a missing id), so a report always sums to what the
// bundle carried.
type SectionCount struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

// ImportReport is what Import returns for the CLI to print.
type ImportReport struct {
	SettingsKeys int          `json:"settingsKeys"`
	Teams        SectionCount `json:"teams"`
	APIKeys      SectionCount `json:"apiKeys"`
	Connections  SectionCount `json:"connections"`
	Combos       SectionCount `json:"combos"`
	Webhooks     SectionCount `json:"webhookTargets"`
}

// String renders the report as the CLI's one-line-per-section summary.
func (r ImportReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "settings keys applied: %d\n", r.SettingsKeys)
	fmt.Fprintf(&b, "api keys:              %d created, %d updated, %d skipped (key values are masked; re-provision the secrets)\n",
		r.APIKeys.Created, r.APIKeys.Updated, r.APIKeys.Skipped)
	fmt.Fprintf(&b, "connections:           %d created, %d updated, %d skipped\n",
		r.Connections.Created, r.Connections.Updated, r.Connections.Skipped)
	fmt.Fprintf(&b, "combos:                %d created, %d updated, %d skipped\n",
		r.Combos.Created, r.Combos.Updated, r.Combos.Skipped)
	fmt.Fprintf(&b, "teams:                 %d created, %d updated, %d skipped\n",
		r.Teams.Created, r.Teams.Updated, r.Teams.Skipped)
	fmt.Fprintf(&b, "webhook targets:       %d created, %d updated, %d skipped",
		r.Webhooks.Created, r.Webhooks.Updated, r.Webhooks.Skipped)
	return b.String()
}

// Import parses blob (decrypting it when it carries the encrypted prefix),
// validates the schema version, and applies every section it carries.
func Import(repo *db.Repo, blob []byte) (ImportReport, error) {
	if repo == nil {
		return ImportReport{}, fmt.Errorf("syncbundle.Import: nil repo")
	}
	plain := blob
	if hasEncryptedPrefix(blob) {
		var err error
		if plain, err = Decrypt(blob); err != nil {
			return ImportReport{}, fmt.Errorf("syncbundle.Import: %w", err)
		}
	}

	var bundle Bundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		return ImportReport{}, fmt.Errorf("syncbundle.Import: parse bundle: %w", err)
	}
	if bundle.Version != BundleVersion {
		return ImportReport{}, fmt.Errorf("syncbundle.Import: unsupported bundle version %d (want %d)", bundle.Version, BundleVersion)
	}
	if bundle.Gateway != "" && bundle.Gateway != Gateway {
		return ImportReport{}, fmt.Errorf("syncbundle.Import: bundle is from %q, not %q", bundle.Gateway, Gateway)
	}

	report := ImportReport{}
	if len(bundle.Settings) > 0 {
		// UpdateSettingsRaw merges under the repo's write lock, so keys the
		// bundle does not carry (including the auth secrets it must never
		// carry) keep their live values — the stripped password does not
		// reset the dashboard's auth.
		if err := repo.UpdateSettingsRaw(bundle.Settings); err != nil {
			return report, fmt.Errorf("syncbundle.Import: settings: %w", err)
		}
		report.SettingsKeys = len(bundle.Settings)
	}

	if err := importTeams(repo, bundle.Teams, &report.Teams); err != nil {
		return report, err
	}
	if err := importAPIKeys(repo, bundle.APIKeys, &report.APIKeys); err != nil {
		return report, err
	}
	if err := importConnections(repo, bundle.Conns, &report.Connections); err != nil {
		return report, err
	}
	if err := importCombos(repo, bundle.Combos, &report.Combos); err != nil {
		return report, err
	}
	if err := importWebhooks(repo, bundle.Webhooks, &report.Webhooks); err != nil {
		return report, err
	}
	return report, nil
}

// importTeams applies the teams first: API key billing attribution below
// points at these ids.
func importTeams(repo *db.Repo, teams []db.Team, count *SectionCount) error {
	for _, team := range teams {
		if team.ID == "" {
			count.Skipped++
			continue
		}
		existing, err := repo.GetTeam(team.ID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := repo.CreateTeam(team); err != nil {
				return fmt.Errorf("syncbundle.Import: team %s: %w", team.ID, err)
			}
			count.Created++
		case err != nil:
			return fmt.Errorf("syncbundle.Import: team %s: %w", team.ID, err)
		default:
			if err := repo.UpdateTeam(team); err != nil {
				return fmt.Errorf("syncbundle.Import: team %s: %w", team.ID, err)
			}
			count.Updated++
		}
		// UpdateTeam does not touch status — archiving is its own operation.
		// Only archive when the live row is not there yet, so re-importing
		// does not restamp archived_at and drift the state it claims to keep.
		if team.Status == db.TeamStatusArchived && existing.Status != db.TeamStatusArchived {
			if err := repo.ArchiveTeam(team.ID); err != nil {
				return fmt.Errorf("syncbundle.Import: team %s: archive: %w", team.ID, err)
			}
		}
	}
	return nil
}

// importAPIKeys upserts key metadata. A key the target does not have is
// created with an unusable placeholder value: the bundle deliberately carries
// no key material, so the row exists so its limits, expiry and plan survive
// the move, and the operator pastes the real secret on the target. Re-import
// of an existing row only touches metadata, never the live key value.
func importAPIKeys(repo *db.Repo, keys []KeyMeta, count *SectionCount) error {
	for _, k := range keys {
		if k.ID == "" {
			count.Skipped++
			continue
		}
		exists, err := apiKeyExists(repo, k.ID)
		if err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: %w", k.ID, err)
		}
		if !exists {
			// The bundle carries no key material, and a row created from a
			// placeholder would be a credential derived from public metadata:
			// anyone holding the bundle knows the id. Policy that cannot be
			// restored without a secret is not restored; the operator creates
			// the key on this instance instead.
			count.Skipped++
			continue
		}
		count.Updated++

		// The setters are unconditional: zero values mean "unset" in every
		// one of them, which is exactly what an absent bundle field means.
		if err := repo.SetApiKeyStatus(k.ID, k.IsActive != 0); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: status: %w", k.ID, err)
		}
		if k.Name != nil {
			if err := repo.SetApiKeyName(k.ID, *k.Name); err != nil {
				return fmt.Errorf("syncbundle.Import: api key %s: name: %w", k.ID, err)
			}
		}
		if err := repo.SetApiKeyRateLimit(k.ID,
			derefInt(k.RateLimitRPM, 0), derefInt(k.RateLimitTPM, 0), derefInt(k.RateLimitConcurrency, 0)); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: rate limit: %w", k.ID, err)
		}
		if err := repo.SetApiKeyBudget(k.ID,
			derefInt(k.TokenQuota, 0), derefInt(k.QueueTimeoutMs, 0)); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: budget: %w", k.ID, err)
		}
		if err := repo.SetApiKeyPeriodBudget(k.ID, k.LimitMicros, k.LimitTokens, k.BudgetPeriod, k.AlertPct, k.HardCutoff); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: period budget: %w", k.ID, err)
		}
		if err := repo.SetApiKeyExpiry(k.ID, derefString(k.ExpiresAt)); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: expiry: %w", k.ID, err)
		}
		if err := repo.SetApiKeyPlan(k.ID, derefString(k.PlanID)); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: plan: %w", k.ID, err)
		}
		if err := repo.SetApiKeyDailyCostLimit(k.ID, k.DailyCostLimit); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: daily cost limit: %w", k.ID, err)
		}
		if err := repo.SetApiKeyIPWhitelist(k.ID, k.IPWhitelist); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: ip whitelist: %w", k.ID, err)
		}
		if err := repo.SetApiKeySystemPrompt(k.ID, k.SystemPrompt); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: system prompt: %w", k.ID, err)
		}
		if err := repo.SetApiKeyPermissions(k.ID, k.Permissions); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: permissions: %w", k.ID, err)
		}
		if err := repo.SetApiKeyRequestLimit(k.ID, k.RequestLimit, k.RequestResetInterval); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: request limit: %w", k.ID, err)
		}
		if err := repo.SetApiKeyBillingTeam(k.ID, k.BillingTeamID); err != nil {
			return fmt.Errorf("syncbundle.Import: api key %s: billing team: %w", k.ID, err)
		}
	}
	return nil
}

// importConnections re-creates each connection through the create path that
// seals on write, so credentials land in the target's sealed columns under
// its own master key. An existing row gets the payload replaced in place —
// the dashboard's own overwrite path — keeping its rotation position.
func importConnections(repo *db.Repo, conns []ConnectionMeta, count *SectionCount) error {
	for _, c := range conns {
		if c.ID == "" || c.Provider == "" {
			count.Skipped++
			continue
		}
		existing, err := repo.GetProviderConnectionByID(c.ID)
		if err != nil {
			return fmt.Errorf("syncbundle.Import: connection %s: %w", c.ID, err)
		}
		if existing == nil {
			if err := repo.CreateProviderConnectionFull(c.ID, c.Provider, c.AuthType, c.Name, &c.Priority, c.Data); err != nil {
				return fmt.Errorf("syncbundle.Import: connection %s: create: %w", c.ID, err)
			}
			count.Created++
		} else {
			if err := repo.ReplaceProviderConnectionPayload(c.ID, c.Name, c.Data); err != nil {
				return fmt.Errorf("syncbundle.Import: connection %s: update: %w", c.ID, err)
			}
			count.Updated++
		}
		// Neither create path takes isActive, and no setter exists for it —
		// a disabled connection must not come back enabled just because the
		// bundle said so on the create path too.
		if c.IsActive == 0 {
			if err := setConnectionInactive(repo, c.ID); err != nil {
				return fmt.Errorf("syncbundle.Import: connection %s: %w", c.ID, err)
			}
		}
	}
	return nil
}

// importCombos upserts by id. The bundle's strategy travels as resolved at
// export; smart match rules travel only when the source combo had them, so a
// rule-less combo never clears rules the target operator added by hand.
func importCombos(repo *db.Repo, combos []models.Combo, count *SectionCount) error {
	for _, c := range combos {
		if c.ID == "" || c.Name == "" {
			count.Skipped++
			continue
		}
		existing, err := repo.GetComboById(c.ID)
		if err != nil {
			return fmt.Errorf("syncbundle.Import: combo %s: %w", c.ID, err)
		}
		if existing == nil {
			if err := repo.CreateCombo(c.ID, c.Name, derefString(c.Kind), c.Models, c.Strategy); err != nil {
				return fmt.Errorf("syncbundle.Import: combo %s: create: %w", c.ID, err)
			}
			count.Created++
		} else {
			if err := repo.UpdateCombo(c.ID, c.Name, derefString(c.Kind), c.Models, c.Strategy); err != nil {
				return fmt.Errorf("syncbundle.Import: combo %s: update: %w", c.ID, err)
			}
			count.Updated++
		}
		if c.MatchRules != "" && c.MatchRules != "[]" {
			if err := repo.SetComboRules(context.Background(), c.ID, c.MatchRules); err != nil {
				return fmt.Errorf("syncbundle.Import: combo %s: rules: %w", c.ID, err)
			}
		}
	}
	return nil
}

// importWebhooks leans on SaveWebhookTarget's native upsert.
func importWebhooks(repo *db.Repo, targets []db.WebhookTarget, count *SectionCount) error {
	ctx := context.Background()
	for _, t := range targets {
		if t.ID == "" {
			count.Skipped++
			continue
		}
		existing, err := repo.GetWebhookTarget(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("syncbundle.Import: webhook target %s: %w", t.ID, err)
		}
		if err := repo.SaveWebhookTarget(ctx, t); err != nil {
			return fmt.Errorf("syncbundle.Import: webhook target %s: %w", t.ID, err)
		}
		if existing == nil {
			count.Created++
		} else {
			count.Updated++
		}
	}
	return nil
}

// apiKeyExists probes the apiKeys table by id. There is no exported
// by-id getter (the read paths key on the key value or its hash), and the
// probe is one indexed point query.
func apiKeyExists(repo *db.Repo, id string) (bool, error) {
	var one int
	err := repo.RawDB().QueryRow(`SELECT 1 FROM apiKeys WHERE id = ? LIMIT 1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// setConnectionInactive applies a bundle's disabled flag. One guarded UPDATE
// on two literal columns, mirroring SetConnectionSealed's style: the repo has
// no exported active toggle, and adding one to internal/db for a single
// importer would put a second write path on a hot table.
func setConnectionInactive(repo *db.Repo, id string) error {
	_, err := repo.RawDB().Exec(
		`UPDATE providerConnections SET isActive = 0, updatedAt = ? WHERE id = ?`,
		nowUTC(), id)
	if err != nil {
		return err
	}
	return nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// hasEncryptedPrefix reports whether the blob claims to be ciphertext.
func hasEncryptedPrefix(blob []byte) bool {
	return strings.HasPrefix(string(blob), encryptedPrefix)
}
