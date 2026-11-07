// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"fmt"
	"time"
)

// apikeys_perkey.go holds the writes behind the serenhope per-key governance
// columns (port(serenhope)): the daily cost accumulator, the IP whitelist,
// the per-key system prompt, the endpoint permissions and the per-interval
// request cap. The reads go through the shared apiKeyColumns/apiKeyScan pair
// in apikeys.go; these setters are the only writers, so a dashboard edit and
// an engine-side accounting bump cannot disagree about the row shape.

// SetApiKeyDailyCostLimit sets the USD-per-UTC-day ceiling for a key. 0 means
// unlimited — the same "0 is not set" convention every numeric limit column
// on this table uses.
func (r *Repo) SetApiKeyDailyCostLimit(id string, limit float64) error {
	_, err := r.db.Exec(`UPDATE apiKeys SET dailyCostLimit = ? WHERE id = ?`, limit, id)
	if err != nil {
		return fmt.Errorf("set api key daily cost limit %s: %w", id, err)
	}
	return nil
}

// SetApiKeyIPWhitelist stores the key's client-address allowlist: a JSON array
// of IP literals and CIDR ranges, or "" to allow every address. The JSON is
// validated by the dashboard handler before it reaches here.
func (r *Repo) SetApiKeyIPWhitelist(id, whitelist string) error {
	_, err := r.db.Exec(`UPDATE apiKeys SET ipWhitelist = ? WHERE id = ?`, whitelist, id)
	if err != nil {
		return fmt.Errorf("set api key ip whitelist %s: %w", id, err)
	}
	return nil
}

// SetApiKeySystemPrompt stores the system message injected into every request
// the key sends. "" disables the injection — the same fail-open contract the
// upstream injector keeps (open-sse/rtk/systemInject.js never fails a request).
func (r *Repo) SetApiKeySystemPrompt(id, prompt string) error {
	_, err := r.db.Exec(`UPDATE apiKeys SET systemPrompt = ? WHERE id = ?`, prompt, id)
	if err != nil {
		return fmt.Errorf("set api key system prompt %s: %w", id, err)
	}
	return nil
}

// SetApiKeyPermissions stores the key's endpoint-permission JSON object. ""
// means every endpoint family is allowed, which is what keys created before
// the column existed must keep meaning.
func (r *Repo) SetApiKeyPermissions(id, permissions string) error {
	_, err := r.db.Exec(`UPDATE apiKeys SET permissions = ? WHERE id = ?`, permissions, id)
	if err != nil {
		return fmt.Errorf("set api key permissions %s: %w", id, err)
	}
	return nil
}

// SetApiKeyRequestLimit caps the requests a key may send per interval. The
// interval is whole minutes; 0 makes the cap lifetime — the counter still
// grows, but it never rolls over, so an exhausted key stays exhausted until
// the operator raises the limit or resets it.
func (r *Repo) SetApiKeyRequestLimit(id string, limit int64, intervalMinutes int) error {
	_, err := r.db.Exec(
		`UPDATE apiKeys SET requestLimit = ?, requestResetInterval = ? WHERE id = ?`,
		limit, intervalMinutes, id,
	)
	if err != nil {
		return fmt.Errorf("set api key request limit %s: %w", id, err)
	}
	return nil
}

// BumpApiKeyCostSpent charges one request's realized cost to its key,
// mirroring the write side of upstream usageRepo.js saveRequestUsage: when
// costDay already names today the spend accumulates, and when it names an
// earlier day (or nothing) the write REPLACES the accumulator — the daily
// reset is lazy, so no timer ever has to run. The row is matched by id rather
// than by the upstream key string because hashed rows blank their `key`
// column; the id is stable across both row shapes.
//
// A zero-cost request changes nothing, exactly like upstream's
// `if (cost > 0)` guard: failed requests price nothing and free models leave
// the accumulator alone.
//
// The reset is guarded against the day-boundary race where two bumps land
// across midnight out of order: a request that completed at 23:59:59 (day =
// yesterday) can reach this statement after one that completed at 00:00:01
// (day = today) has already reset the accumulator. Letting the late write
// replace unconditionally would stamp costDay backwards — today's spend would
// read as zero through ApiKeyCostToday until the next bump, and the daily cap
// would under-count exactly the burst that crossed the boundary. The stored
// day is therefore never moved backwards: a bump for an already-closed day
// leaves the newer accumulator alone (its cost belonged to a bucket the lazy
// reset has already closed), and only a strictly older stamp is replaced.
// day is a fixed-width UTC date (2006-01-02), so the string comparison is a
// date comparison.
func (r *Repo) BumpApiKeyCostSpent(keyID string, cost float64, day string) error {
	if cost <= 0 {
		return nil
	}
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE apiKeys
		    SET costSpent = CASE
		          WHEN costDay = ? THEN COALESCE(costSpent, 0) + ?
		          WHEN COALESCE(costDay, '') < ? THEN ?
		          ELSE COALESCE(costSpent, 0) END,
		        costDay = CASE
		          WHEN COALESCE(costDay, '') < ? THEN ?
		          ELSE costDay END
		  WHERE id = ?`,
		day, cost, day, cost, day, day, keyID,
	)
	if err != nil {
		return fmt.Errorf("bump api key cost spent %s: %w", keyID, err)
	}
	return nil
}

// ApiKeyCostToday returns the spend a key has accumulated on the given UTC
// day. A costDay stamp from any other day reads as zero — the lazy reset
// means stale spend is neither charged again nor carried over. The gate reads
// this fresh rather than trusting the auth-cached key row: a cap enforced on
// a five-second-stale snapshot would let a burst of concurrent requests
// overshoot the ceiling by everything that landed inside the TTL.
func (r *Repo) ApiKeyCostToday(keyID, day string) (float64, error) {
	var costSpent float64
	var costDay string
	err := r.db.QueryRowContext(context.Background(),
		`SELECT costSpent, costDay FROM apiKeys WHERE id = ?`, keyID,
	).Scan(&costSpent, &costDay)
	if err != nil {
		return 0, fmt.Errorf("read api key cost spent %s: %w", keyID, err)
	}
	if costDay != day {
		return 0, nil
	}
	return costSpent, nil
}

// BumpApiKeyRequests counts one completed request against the key's
// per-interval cap and advances the window atomically, in one statement: when
// the stored window has rolled past requestResetInterval minutes the counter
// restarts at 1 and requestResetAt moves to now, otherwise the counter grows.
// Doing both in the UPDATE keeps two concurrent completions from reading the
// same stale counter and writing past the cap.
//
// julianday() parses the RFC3339 stamps both sides use, so the elapsed-minutes
// comparison stays inside SQLite and needs no round trip to Go. The window
// aligns to the request that opened it rather than to wall-clock boundaries —
// upstream advances lastResetAt by whole elapsed periods for the same reason.
func (r *Repo) BumpApiKeyRequests(keyID string, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(context.Background(),
		`UPDATE apiKeys SET
			usedRequests = CASE
				WHEN requestResetInterval > 0 AND requestResetAt <> ''
				     AND (julianday(?) - julianday(requestResetAt)) * 1440.0 >= requestResetInterval
				THEN 1
				ELSE COALESCE(usedRequests, 0) + 1 END,
			requestResetAt = CASE
				WHEN requestResetInterval > 0
				     AND (requestResetAt = ''
				          OR (julianday(?) - julianday(requestResetAt)) * 1440.0 >= requestResetInterval)
				THEN ?
				ELSE requestResetAt END
		  WHERE id = ?`,
		stamp, stamp, stamp, keyID,
	)
	if err != nil {
		return fmt.Errorf("bump api key requests %s: %w", keyID, err)
	}
	return nil
}
