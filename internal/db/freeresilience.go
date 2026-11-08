// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"encoding/json"
	"fmt"
	"time"

	"9routerx/proxy/internal/freeresilience"
)

// freeresilience.go persists the free-tier resilience suite's state:
//
//   - freeTierRecovery — one row per provider-level limit event. The row is
//     the provider's parking ticket: while it is `cooling` and its resetAt is
//     in the future, the fallback chain skips the provider. Rows are never
//     deleted; the status flips to `recovered` when a success arrives early
//     or the reset passes, so the dashboard can show the last condition.
//   - freeProviderHealth — one row per provider holding the 24h outcome ring
//     as JSON, written by the scheduler's flush pass so a restart keeps the
//     health score.
//
// The methods implement freeresilience.RecoveryStore; the tracker owns the
// in-memory state and the policy, this file only moves bytes.

// FreeTierRecoveryEvent is one stored limit event.
type FreeTierRecoveryEvent struct {
	ID          int64
	Provider    string
	Model       string
	ErrorKind   string
	Regime      string
	EventAt     time.Time
	ResetAt     time.Time
	OpenEnded   bool
	Attempts    int
	LastError   string
	Status      string
	RecoveredAt time.Time
}

// RecordFreeTierLimitEvent records (or bumps) a provider's parking ticket.
// When an active cooling row already exists for the provider, the event
// updates that row — attempts increments, the newest error and timestamp win,
// and the reset only ever moves LATER (a second 429 must not shorten a wait a
// monthly regime already set). Without an active row it inserts one.
func (r *Repo) RecordFreeTierLimitEvent(provider, model, kind, regime string, resetAt *time.Time, lastError string, now time.Time) error {
	if provider == "" {
		return fmt.Errorf("record free tier limit event: empty provider")
	}
	eventAt := now.UTC().Format(time.RFC3339)
	resetStr := ""
	if resetAt != nil && !resetAt.IsZero() {
		resetStr = resetAt.UTC().Format(time.RFC3339)
	}

	// Bump path: an open ticket for this provider gets the new evidence.
	res, err := r.db.Exec(
		`UPDATE freeTierRecovery
		    SET attempts = attempts + 1,
		        model = CASE WHEN ? != '' THEN ? ELSE model END,
		        errorKind = ?,
		        regime = CASE WHEN ? != '' THEN ? ELSE regime END,
		        eventAt = ?,
		        resetAt = CASE WHEN ? = '' THEN resetAt
		                       WHEN resetAt = '' THEN ?
		                       WHEN ? > resetAt THEN ?
		                       ELSE resetAt END,
		        lastError = ?
		  WHERE provider = ? AND status = 'cooling'`,
		model, model, kind, regime, regime, eventAt,
		resetStr, resetStr, resetStr, resetStr, lastError, provider,
	)
	if err != nil {
		return fmt.Errorf("record free tier limit event %s: %w", provider, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}

	_, err = r.db.Exec(
		`INSERT INTO freeTierRecovery (provider, model, errorKind, regime, eventAt, resetAt, attempts, lastError, status)
		 VALUES (?, ?, ?, ?, ?, ?, 1, ?, 'cooling')`,
		provider, model, kind, regime, eventAt, resetStr, lastError,
	)
	if err != nil {
		return fmt.Errorf("record free tier limit event %s: insert: %w", provider, err)
	}
	return nil
}

// ActiveFreeTierCooldowns returns every provider currently parked. A row is
// active while status = 'cooling' AND (resetAt is a future instant OR empty —
// an empty resetAt is the open-ended wait of a credit/one-time regime).
func (r *Repo) ActiveFreeTierCooldowns(now time.Time) ([]freeresilience.FreeTierCooldown, error) {
	rows, err := r.db.Query(
		`SELECT provider, model, errorKind, regime, eventAt, resetAt, attempts, lastError
		   FROM freeTierRecovery
		  WHERE status = 'cooling'`,
	)
	if err != nil {
		return nil, fmt.Errorf("active free tier cooldowns: %w", err)
	}
	defer rows.Close()

	nowUTC := now.UTC()
	var out []freeresilience.FreeTierCooldown
	for rows.Next() {
		var ev FreeTierRecoveryEvent
		var eventAt, resetAt string
		if err := rows.Scan(&ev.Provider, &ev.Model, &ev.ErrorKind, &ev.Regime, &eventAt, &resetAt, &ev.Attempts, &ev.LastError); err != nil {
			return nil, fmt.Errorf("active free tier cooldowns: scan: %w", err)
		}
		cd := freeresilience.FreeTierCooldown{
			Provider:  ev.Provider,
			Model:     ev.Model,
			Kind:      ev.ErrorKind,
			Regime:    ev.Regime,
			LastError: ev.LastError,
		}
		if ev.Attempts > 0 {
			// Carried for observability; the tracker keeps its own counter.
			_ = ev.Attempts
		}
		if t, perr := time.Parse(time.RFC3339, eventAt); perr == nil {
			cd.EventAt = t
		}
		if resetAt == "" {
			cd.OpenEnded = true
		} else if t, perr := time.Parse(time.RFC3339, resetAt); perr == nil {
			if t.After(nowUTC) {
				cd.ResetAt = t
			} else {
				// The reset passed while nobody was looking; the scheduler
				// will mark it recovered. Report it as no longer blocking.
				continue
			}
		} else {
			// An unparseable reset cannot be trusted to unlock: treat it as
			// open-ended so a success (or the operator) clears it.
			cd.OpenEnded = true
		}
		out = append(out, cd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active free tier cooldowns: %w", err)
	}
	return out, nil
}

// ClearFreeTierCooldowns lifts a provider's active wait early — the smart
// recovery path: the provider answered again before its estimated reset.
func (r *Repo) ClearFreeTierCooldowns(provider string, now time.Time, reason string) error {
	if provider == "" {
		return nil
	}
	_, err := r.db.Exec(
		`UPDATE freeTierRecovery
		    SET status = 'recovered', recoveredAt = ?, lastError = lastError || ?
		  WHERE provider = ? AND status = 'cooling'`,
		now.UTC().Format(time.RFC3339), " ["+reason+"]", provider,
	)
	if err != nil {
		return fmt.Errorf("clear free tier cooldowns %s: %w", provider, err)
	}
	return nil
}

// ExpireFreeTierCooldowns marks every cooling row whose reset has passed as
// recovered, returning the providers that rejoined rotation. Open-ended rows
// (credit/one-time) are deliberately never expired: they have no clock.
func (r *Repo) ExpireFreeTierCooldowns(now time.Time) ([]string, error) {
	nowStr := now.UTC().Format(time.RFC3339)
	rows, err := r.db.Query(
		`SELECT DISTINCT provider FROM freeTierRecovery
		  WHERE status = 'cooling' AND resetAt != '' AND resetAt <= ?`,
		nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("expire free tier cooldowns: %w", err)
	}
	var due []string
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			rows.Close()
			return nil, fmt.Errorf("expire free tier cooldowns: scan: %w", err)
		}
		due = append(due, provider)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("expire free tier cooldowns: %w", err)
	}
	if len(due) == 0 {
		return nil, nil
	}
	for _, provider := range due {
		if _, err := r.db.Exec(
			`UPDATE freeTierRecovery
			    SET status = 'recovered', recoveredAt = ?
			  WHERE provider = ? AND status = 'cooling' AND resetAt != '' AND resetAt <= ?`,
			nowStr, provider, nowStr,
		); err != nil {
			return due, fmt.Errorf("expire free tier cooldowns %s: %w", provider, err)
		}
	}
	return due, nil
}

// SaveFreeProviderHealth persists one provider's outcome ring. The ring is
// JSON-encoded whole: one row per provider, rewritten on flush.
func (r *Repo) SaveFreeProviderHealth(provider string, outcomes []freeresilience.Outcome, now time.Time) error {
	if provider == "" {
		return nil
	}
	blob, err := json.Marshal(outcomes)
	if err != nil {
		return fmt.Errorf("save free provider health %s: %w", provider, err)
	}
	nowStr := now.UTC().Format(time.RFC3339)
	_, err = r.db.Exec(
		`INSERT INTO freeProviderHealth (provider, outcomes, updatedAt)
		 VALUES (?, ?, ?)
		 ON CONFLICT(provider) DO UPDATE SET outcomes = excluded.outcomes, updatedAt = excluded.updatedAt`,
		provider, string(blob), nowStr,
	)
	if err != nil {
		return fmt.Errorf("save free provider health %s: %w", provider, err)
	}
	return nil
}

// LoadFreeProviderHealth reads every persisted outcome ring back.
func (r *Repo) LoadFreeProviderHealth() (map[string][]freeresilience.Outcome, error) {
	rows, err := r.db.Query(`SELECT provider, outcomes FROM freeProviderHealth`)
	if err != nil {
		return nil, fmt.Errorf("load free provider health: %w", err)
	}
	defer rows.Close()

	out := map[string][]freeresilience.Outcome{}
	for rows.Next() {
		var provider, blob string
		if err := rows.Scan(&provider, &blob); err != nil {
			return nil, fmt.Errorf("load free provider health: scan: %w", err)
		}
		if blob == "" {
			continue
		}
		var outcomes []freeresilience.Outcome
		if err := json.Unmarshal([]byte(blob), &outcomes); err != nil {
			// A ring we cannot parse is dropped, not fatal: the score
			// rebuilds from live traffic.
			continue
		}
		out[provider] = outcomes
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load free provider health: %w", err)
	}
	return out, nil
}

// LatestFreeTierRecoveryEvents returns the most recent limit events, newest
// first, for the dashboard's provider health panel.
func (r *Repo) LatestFreeTierRecoveryEvents(limit int) ([]FreeTierRecoveryEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Query(
		`SELECT id, provider, model, errorKind, regime, eventAt, resetAt, attempts, lastError, status, recoveredAt
		   FROM freeTierRecovery ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("latest free tier recovery events: %w", err)
	}
	defer rows.Close()

	var out []FreeTierRecoveryEvent
	for rows.Next() {
		var ev FreeTierRecoveryEvent
		var eventAt, resetAt, recoveredAt string
		if err := rows.Scan(&ev.ID, &ev.Provider, &ev.Model, &ev.ErrorKind, &ev.Regime, &eventAt, &resetAt, &ev.Attempts, &ev.LastError, &ev.Status, &recoveredAt); err != nil {
			return nil, fmt.Errorf("latest free tier recovery events: scan: %w", err)
		}
		if t, perr := time.Parse(time.RFC3339, eventAt); perr == nil {
			ev.EventAt = t
		}
		if t, perr := time.Parse(time.RFC3339, resetAt); perr == nil {
			ev.ResetAt = t
		} else {
			ev.OpenEnded = resetAt == ""
		}
		if t, perr := time.Parse(time.RFC3339, recoveredAt); perr == nil {
			ev.RecoveredAt = t
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("latest free tier recovery events: %w", err)
	}
	return out, nil
}
