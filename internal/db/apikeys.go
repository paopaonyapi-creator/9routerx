// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/models"
)

// errNoSuchApiKey signals an update that matched no row.
var errNoSuchApiKey = errors.New("no such api key")

// ValidateApiKey checks if the given API key exists and is active.
// Legacy plaintext path; hashed rows are resolved through
// FindApiKeyByLookup plus an argon2id verification.
func (r *Repo) ValidateApiKey(key string) (bool, error) {
	var active int
	err := r.db.QueryRow("SELECT isActive FROM apiKeys WHERE key = ? LIMIT 1", key).Scan(&active)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return active == 1, nil
}

const apiKeyColumns = `SELECT id, key, name, machineId, isActive, createdAt,
	rateLimitRPM, rateLimitTPM, rateLimitConcurrency,
	tokenQuota, queueTimeoutMs,
	keyHash, lookupHash, keyDisplay,
	expiresAt, lastUsedAt, usedCount, metadata, planId,
	limitMicros, limitTokens, budgetPeriod, alertPct, hardCutoff`

// apiKeyColumnsLegacy is the pre-F-2 read: everything above except the five
// budget columns.
const apiKeyColumnsLegacy = `SELECT id, key, name, machineId, isActive, createdAt,
	rateLimitRPM, rateLimitTPM, rateLimitConcurrency,
	tokenQuota, queueTimeoutMs,
	keyHash, lookupHash, keyDisplay,
	expiresAt, lastUsedAt, usedCount, metadata, planId`

// apiKeyColumnsPerKey is the full read plus the serenhope per-key governance
// columns (port(serenhope)): daily cost accounting, IP whitelist, per-key
// system prompt, endpoint permissions, and the per-interval request cap.
const apiKeyColumnsPerKey = apiKeyColumns + `,
	dailyCostLimit, costSpent, costDay,
	ipWhitelist, systemPrompt, permissions,
	requestLimit, usedRequests, requestResetInterval, requestResetAt`

// apiKeySchemaLevel names how much of the apiKeys governance column set the
// table in hand carries. The levels stack: a table that answers the per-key
// read also carries the budget columns, and a budget table also carries the
// base ones. The level exists because the gateway runs against databases it
// did not create — a shared 9Router Pro file may predate either set — and a
// key read must degrade to the columns the table actually has rather than
// failing every listing.
type apiKeySchemaLevel int

const (
	apiKeySchemaLegacy apiKeySchemaLevel = iota // pre-F-2 columns only
	apiKeySchemaBudget                          // + F-2 budget columns
	apiKeySchemaPerKey                          // + serenhope per-key columns
)

// perKeyColumnsPresent reports whether the apiKeys table carries the serenhope
// per-key governance columns. The answer is cached because it is a property of
// the schema, which does not change while the process runs — the same caching
// budgetColumnsPresent applies to its own probe.
func (r *Repo) perKeyColumnsPresent() bool {
	if r == nil || r.db == nil {
		return false
	}
	r.perKeyColsOnce.Do(func() {
		rows, err := r.db.QueryContext(context.Background(), `PRAGMA table_info(apiKeys)`)
		if err != nil {
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notNull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
				return
			}
			if name == "dailyCostLimit" {
				r.perKeyCols = true
				return
			}
		}
		// A mid-iteration failure would otherwise leave the probe answering
		// "no per-key columns" for a database that has them, which silently
		// degrades every key read to the narrower query.
		_ = rows.Err()
	})
	return r.perKeyCols
}

// usageTeamColumnPresent reports whether usageHistory carries the tenant
// cost-center column (billingTeamId). The probe is cached like its apiKeys
// siblings: the answer is a property of the schema, and the insert path pays
// it once per Repo instead of once per ledger row.
func (r *Repo) usageTeamColumnPresent() bool {
	if r == nil || r.db == nil {
		return false
	}
	r.usageTeamColOnce.Do(func() {
		rows, err := r.db.QueryContext(context.Background(), `PRAGMA table_info(usageHistory)`)
		if err != nil {
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notNull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
				return
			}
			if name == "billingTeamId" {
				r.usageTeamCol = true
				return
			}
		}
		// A mid-iteration failure would otherwise leave the probe answering
		// "no team column" for a ledger that has it, silently dropping every
		// team attribution for the life of the process.
		_ = rows.Err()
	})
	return r.usageTeamCol
}

// budgetColumnsPresent reports whether the apiKeys table carries the F-2 budget
// columns. The answer is cached because it is a property of the schema, which
// does not change while the process runs, and the probe sits on the dashboard's
// boot path.
func (r *Repo) budgetColumnsPresent() bool {
	if r == nil || r.db == nil {
		return false
	}
	r.budgetColsOnce.Do(func() {
		rows, err := r.db.QueryContext(context.Background(), `PRAGMA table_info(apiKeys)`)
		if err != nil {
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notNull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
				return
			}
			if name == "limitMicros" {
				r.budgetCols = true
				return
			}
		}
		// A mid-iteration failure would otherwise leave the probe answering
		// "no budget columns" for a database that has them, which silently
		// degrades every key read to the legacy query.
		_ = rows.Err()
	})
	return r.budgetCols
}

// apiKeySchema resolves the level the apiKeys table in hand supports: the
// per-key read when the serenhope columns exist, otherwise the budget read
// when the F-2 columns exist, otherwise the pre-F-2 read.
func (r *Repo) apiKeySchema() apiKeySchemaLevel {
	if r.perKeyColumnsPresent() {
		return apiKeySchemaPerKey
	}
	if r.budgetColumnsPresent() {
		return apiKeySchemaBudget
	}
	return apiKeySchemaLegacy
}

// apiKeyColumnsFor returns the key read for the schema in hand. A legacy
// database reads every key with its budget and per-key axes at zero rather
// than failing the whole listing — the same degradation quotaResetId already
// gets.
func (r *Repo) apiKeyColumnsFor() string {
	switch r.apiKeySchema() {
	case apiKeySchemaPerKey:
		return apiKeyColumnsPerKey
	case apiKeySchemaBudget:
		return apiKeyColumns
	default:
		return apiKeyColumnsLegacy
	}
}

// apiKeyScan returns the Scan destinations for the key read plus an applier
// that must run after Scan has filled the nullable columns.
//
// level selects between the three reads, so the destination count always
// matches the column count of the query that produced the row. Passing the
// full destinations against a narrower row is a scan error, not a zero —
// which is how the legacy-schema test caught this.
func apiKeyScan(k *models.APIKey, level apiKeySchemaLevel) (dest []any, apply func()) {
	var nameVal, machineVal sql.NullString
	var rateRPM, rateTPM, rateConc sql.NullInt64
	var tokenQuota, queueTimeout sql.NullInt64
	var keyHash, lookupHash, keyDisplay sql.NullString
	var expiresAt, lastUsedAt, metadata sql.NullString
	var usedCount sql.NullInt64
	var planID sql.NullString
	// F-2 per-period budget. limitMicros and limitTokens are plain int64 on
	// the model rather than pointers: 0 is the meaningful "not set" value, so
	// there is no NULL to preserve.
	var limitMicros, limitTokens int64
	var budgetPeriod string
	var alertPct, hardCutoff int
	// serenhope per-key governance columns. Every column in the set carries a
	// DEFAULT, so a row written before the columns existed scans as zero
	// values — the same "0 means unset" convention the budget columns use.
	var dailyCostLimit, costSpent float64
	var costDay, ipWhitelist, systemPrompt, permissions string
	var requestLimit, usedRequests int64
	var requestResetInterval int
	var requestResetAt string
	dest = []any{&k.ID, &k.Key, &nameVal, &machineVal, &k.IsActive, &k.CreatedAt,
		&rateRPM, &rateTPM, &rateConc, &tokenQuota, &queueTimeout, &keyHash, &lookupHash, &keyDisplay,
		&expiresAt, &lastUsedAt, &usedCount, &metadata, &planID}
	if level >= apiKeySchemaBudget {
		dest = append(dest, &limitMicros, &limitTokens, &budgetPeriod, &alertPct, &hardCutoff)
	}
	if level >= apiKeySchemaPerKey {
		dest = append(dest, &dailyCostLimit, &costSpent, &costDay,
			&ipWhitelist, &systemPrompt, &permissions,
			&requestLimit, &usedRequests, &requestResetInterval, &requestResetAt)
	}
	apply = func() {
		applyApiKeyNulls(k, nameVal, machineVal, rateRPM, rateTPM, rateConc, tokenQuota, queueTimeout, usedCount,
			keyHash, lookupHash, keyDisplay, expiresAt, lastUsedAt, metadata, planID)
		if level >= apiKeySchemaBudget {
			k.LimitMicros = limitMicros
			k.LimitTokens = limitTokens
			k.BudgetPeriod = budgetPeriod
			k.AlertPct = alertPct
			k.HardCutoff = hardCutoff != 0
		}
		if level >= apiKeySchemaPerKey {
			k.DailyCostLimit = dailyCostLimit
			k.CostSpent = costSpent
			k.CostDay = costDay
			k.IPWhitelist = ipWhitelist
			k.SystemPrompt = systemPrompt
			k.Permissions = permissions
			k.RequestLimit = requestLimit
			k.UsedRequests = usedRequests
			k.RequestResetInterval = requestResetInterval
			k.RequestResetAt = requestResetAt
		}
	}
	return dest, apply
}

// applyApiKeyNulls copies the scanned nullable columns onto the model.
func applyApiKeyNulls(k *models.APIKey, nameVal, machineVal sql.NullString,
	rateRPM, rateTPM, rateConc, tokenQuota, queueTimeout, usedCount sql.NullInt64,
	keyHash, lookupHash, keyDisplay sql.NullString,
	expiresAt, lastUsedAt, metadata, planID sql.NullString) {
	if nameVal.Valid {
		k.Name = &nameVal.String
	}
	if machineVal.Valid {
		k.MachineID = &machineVal.String
	}
	if rateRPM.Valid {
		v := int(rateRPM.Int64)
		k.RateLimitRPM = &v
	}
	if rateTPM.Valid {
		v := int(rateTPM.Int64)
		k.RateLimitTPM = &v
	}
	if rateConc.Valid {
		v := int(rateConc.Int64)
		k.RateLimitConcurrency = &v
	}
	if tokenQuota.Valid {
		v := int(tokenQuota.Int64)
		k.TokenQuota = &v
	}
	if queueTimeout.Valid {
		v := int(queueTimeout.Int64)
		k.QueueTimeoutMs = &v
	}
	if keyHash.Valid {
		k.KeyHash = &keyHash.String
	}
	if lookupHash.Valid {
		k.LookupHash = &lookupHash.String
	}
	if keyDisplay.Valid {
		k.KeyDisplay = &keyDisplay.String
	}
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.String
	}
	if lastUsedAt.Valid {
		k.LastUsedAt = &lastUsedAt.String
	}
	if usedCount.Valid {
		v := int(usedCount.Int64)
		k.UsedCount = &v
	}
	if metadata.Valid {
		k.Metadata = &metadata.String
	}
	if planID.Valid {
		k.PlanID = &planID.String
	}
}

// GetApiKeyByKey retrieves detailed APIKey information by plaintext key.
// Kept for legacy rows whose `key` column still holds the secret; hashed
// rows have a blank `key` and are found via FindApiKeyByLookup.
func (r *Repo) GetApiKeyByKey(key string) (*models.APIKey, error) {
	return r.queryApiKey("FROM apiKeys WHERE key = ? LIMIT 1", key)
}

// FindApiKeyByLookup retrieves an APIKey by its SHA-256 lookup hash (F-6).
// This is the primary lookup path for hashed rows; it never compares
// plaintext. It returns (nil, nil) when no row matches.
func (r *Repo) FindApiKeyByLookup(lookupHash string) (*models.APIKey, error) {
	if lookupHash == "" {
		return nil, nil
	}
	return r.queryApiKey("FROM apiKeys WHERE lookupHash = ? LIMIT 1", lookupHash)
}

// queryApiKey runs a single-row apiKeys read and maps the scanned columns.
func (r *Repo) queryApiKey(whereClause string, arg string) (*models.APIKey, error) {
	var apiKey models.APIKey
	level := r.apiKeySchema()
	dest, apply := apiKeyScan(&apiKey, level)
	err := r.db.QueryRowContext(context.Background(), r.apiKeyColumnsFor()+" "+whereClause, arg).Scan(dest...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apply()
	return &apiKey, nil
}

// hashedKeyPrefix marks a `key` column as a hashed row standing in for the
// original secret. `apiKeys.key` is TEXT UNIQUE NOT NULL, so every row cannot
// simply be blanked to the same value — the second hashed key would collide
// with the first. The sentinel is derived from that row's lookup hash, which
// makes it unique per row, and carries a prefix no client ever sends, so a
// legacy `WHERE key = ?` lookup can never authenticate against it.
const hashedKeyPrefix = "\x00argon2id:"

func hashedKeySentinel(lookupHash string) string {
	return hashedKeyPrefix + lookupHash
}

// IsHashedKeyRow reports whether a row is a pre-#199 hashed row: its `key`
// column holds the sentinel rather than a secret, so the plaintext is
// unrecoverable. Such a row still authenticates through its argon2id verifier
// (see middleware.resolveApiKey) but cannot be revealed in the dashboard.
func IsHashedKeyRow(k *models.APIKey) bool {
	return k != nil && strings.HasPrefix(k.Key, hashedKeyPrefix)
}

// LedgerIdentity returns the secret spelling the usage ledger and the budget
// reader match for this key. A legacy row still holds its plaintext, so the
// ledger can carry it verbatim; a hash-only row has no plaintext left and the
// mask recorded at issuance is the only form its spend can be matched on.
// MaskAPIKey is idempotent, so a caller that still expects a raw key can pass
// either spelling to the existing readers unchanged.
func LedgerIdentity(k *models.APIKey) string {
	if k == nil {
		return ""
	}
	if !IsHashedKeyRow(k) && k.Key != "" {
		return k.Key
	}
	if k.KeyDisplay != nil {
		return *k.KeyDisplay
	}
	return ""
}

// LedgerMask returns the masked spelling this gateway records in usageHistory
// for the key: the mask of the plaintext while a legacy row still holds one,
// otherwise the mask stored when the secret was issued.
func LedgerMask(k *models.APIKey) string {
	if k == nil {
		return ""
	}
	if !IsHashedKeyRow(k) && k.Key != "" {
		return handlerutil.MaskAPIKey(k.Key)
	}
	if k.KeyDisplay != nil {
		return *k.KeyDisplay
	}
	return ""
}

// HashApiKeyRow fills the F-6 hash columns for a row and replaces the
// plaintext `key` column with the hashed-row sentinel. A sentinel `key` is how
// a hashed row is recognised; legacy rows keep their plaintext and
// authenticate through GetApiKeyByKey until this runs. After it runs the secret
// exists only as an argon2id verifier and a masked display value.
func (r *Repo) HashApiKeyRow(id, keyHash, lookupHash, keyDisplay string) error {
	res, err := r.db.Exec(
		`UPDATE apiKeys SET key = ?, keyHash = ?, lookupHash = ?, keyDisplay = ? WHERE id = ?`,
		hashedKeySentinel(lookupHash), keyHash, lookupHash, keyDisplay, id,
	)
	if err != nil {
		return fmt.Errorf("hash api key row %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("hash api key row %s: %w", id, errNoSuchApiKey)
	}
	return nil
}

// GetApiKeyByID retrieves one key by its row id rather than by its secret.
// The dashboard policy editor uses it so a partial update can read the current
// values without ever touching the credential columns.
func (r *Repo) GetApiKeyByID(id string) (*models.APIKey, error) {
	return r.queryApiKey("FROM apiKeys WHERE id = ? LIMIT 1", id)
}

// RotateApiKeySecret replaces a row's secret with a fresh argon2id verifier and
// the mask recorded for it.
//
// The plaintext never reaches the row: `key` becomes the per-row sentinel, so a
// database dump after a rotation exposes no usable credential. The verifier and
// lookup index replace the old pair in the same statement, which is what makes
// the previous secret stop authenticating the moment the cache entry is
// invalidated.
func (r *Repo) RotateApiKeySecret(id, keyHash, lookupHash, keyDisplay string) error {
	res, err := r.db.Exec(
		`UPDATE apiKeys SET key = ?, keyHash = ?, lookupHash = ?, keyDisplay = ? WHERE id = ?`,
		hashedKeySentinel(lookupHash), keyHash, lookupHash, keyDisplay, id,
	)
	if err != nil {
		return fmt.Errorf("rotate api key secret %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("rotate api key secret %s: %w", id, errNoSuchApiKey)
	}
	return nil
}

// SetApiKeyDisplay records the masked form of a row's secret without touching
// the secret itself.
func (r *Repo) SetApiKeyDisplay(id, keyDisplay string) error {
	res, err := r.db.Exec(`UPDATE apiKeys SET keyDisplay = ? WHERE id = ?`, keyDisplay, id)
	if err != nil {
		return fmt.Errorf("set api key display %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("set api key display %s: %w", id, errNoSuchApiKey)
	}
	return nil
}
