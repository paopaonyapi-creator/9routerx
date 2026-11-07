// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

// DefaultAuditLogRetentionDays is how long a compliance audit row is kept when
// the operator has not asked for anything else. The retention loop prunes on a
// daily ticker; the default is deliberately long (180 days) because the log
// exists to answer "who changed what" long after the change.
const DefaultAuditLogRetentionDays = 180

// AuditLogEntry is one admin action in the compliance trail (FASE 10.6). It is
// a separate record from the securityLog: that one logs request-level security
// events, this one logs operator mutations with their before/after state.
type AuditLogEntry struct {
	ID         int64  `json:"id,omitempty"`
	Timestamp  string `json:"timestamp"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	Target     string `json:"target,omitempty"`
	BeforeJSON string `json:"before,omitempty"`
	AfterJSON  string `json:"after,omitempty"`
	IP         string `json:"ip,omitempty"`
}

// auditClock stamps entries and derives the prune cutoff. A variable so the
// offline test suite can inject a fixed clock; production leaves it on UTC now.
var auditClock = func() time.Time { return time.Now().UTC() }

// InsertAuditLog persists one audit entry.
//
// It is write-ahead by contract: callers invoke it BEFORE performing the
// action it describes, so a mutation that fails (or a process that dies
// mid-request) still leaves a trace. A failed insert must never fail the
// operator's action — the usual call shape is a detached goroutine that drops
// the error — but the entry is still written as completely as the caller
// provided it.
func (r *Repo) InsertAuditLog(ctx context.Context, entry AuditLogEntry) error {
	if entry.Timestamp == "" {
		entry.Timestamp = auditClock().Format(time.RFC3339Nano)
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO auditLog (timestamp, actor, action, target, beforeJSON, afterJSON, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.Timestamp, entry.Actor, entry.Action, entry.Target,
		entry.BeforeJSON, entry.AfterJSON, entry.IP,
	); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

// ListAuditLogs returns recent audit entries, newest first. actor and action
// are optional equality filters; limit is clamped to a page the dashboard can
// render (default 100, max 1000) so a caller cannot walk the whole table.
func (r *Repo) ListAuditLogs(ctx context.Context, limit int, actor, action string) ([]AuditLogEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	query := `SELECT id, timestamp, actor, action, target, beforeJSON, afterJSON, ip
	          FROM auditLog`
	var args []any
	switch {
	case actor != "" && action != "":
		query += ` WHERE actor = ? AND action = ?`
		args = append(args, actor, action)
	case actor != "":
		query += ` WHERE actor = ?`
		args = append(args, actor)
	case action != "":
		query += ` WHERE action = ?`
		args = append(args, action)
	}
	// id DESC breaks ties inside one timestamp: RFC3339Nano stamps from the
	// same write-ahead burst can collide, and the write order is the read
	// order an auditor expects.
	query += ` ORDER BY timestamp DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	entries := make([]AuditLogEntry, 0)
	for rows.Next() {
		var e AuditLogEntry
		var target, before, after, ip sql.NullString
		if err := rows.Scan(
			&e.ID, &e.Timestamp, &e.Actor, &e.Action, &target, &before, &after, &ip,
		); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		e.Target, e.BeforeJSON, e.AfterJSON, e.IP = target.String, before.String, after.String, ip.String
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit logs: %w", err)
	}
	return entries, nil
}

// PruneAuditLogs deletes entries older than retentionDays and reports how many
// rows went. days <= 0 keeps everything, the same convention the usage ledger
// prune uses.
func (r *Repo) PruneAuditLogs(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := auditClock().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	return r.pruneAuditLogsBefore(ctx, cutoff.Format(time.RFC3339Nano))
}

// pruneAuditLogsBefore deletes entries stamped before cutoff. RFC3339 stamps
// with a constant zone compare correctly as strings, so the cutoff is applied
// with one indexed range delete.
func (r *Repo) pruneAuditLogsBefore(ctx context.Context, cutoff string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM auditLog WHERE timestamp < ?`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("prune audit logs before %s: %w", cutoff, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune audit logs: rows affected: %w", err)
	}
	return n, nil
}

// auditRetention guards StartAuditLogRetention against double starts, the same
// one-loop-per-process rule the requestDetails retention loop follows.
var auditRetention struct {
	mu      sync.Mutex
	started bool
}

// auditRetentionInitialDelay defers the first prune past the boot traffic
// burst; the ticker then runs once a day, which is the granularity a disk-side
// retention concern needs.
const (
	auditRetentionInitialDelay = time.Minute
	auditRetentionInterval     = 24 * time.Hour
)

// StartAuditLogRetention prunes the audit log on a daily ticker until ctx is
// cancelled. Safe to call more than once; the extra calls are no-ops. A failed
// pass is logged and retried on the next tick — a pruning failure costs disk,
// never correctness.
func StartAuditLogRetention(ctx context.Context, repo *Repo, retentionDays int) {
	if repo == nil {
		return
	}
	if retentionDays <= 0 {
		retentionDays = DefaultAuditLogRetentionDays
	}

	auditRetention.mu.Lock()
	if auditRetention.started {
		auditRetention.mu.Unlock()
		return
	}
	auditRetention.started = true
	auditRetention.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(auditRetentionInitialDelay):
		}

		ticker := time.NewTicker(auditRetentionInterval)
		defer ticker.Stop()

		for {
			passCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			pruned, err := repo.PruneAuditLogs(passCtx, retentionDays)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[AUDIT] prune failed: %v", err)
				}
			} else if pruned > 0 {
				log.Printf("[AUDIT] pruned %d auditLog rows older than %d days", pruned, retentionDays)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
