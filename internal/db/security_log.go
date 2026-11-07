// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SecurityLogEntry represents one security event stored in SQLite.
type SecurityLogEntry struct {
	ID        int64  `json:"id"`
	At        string `json:"at"`
	Type      string `json:"type"`
	Severity  string `json:"severity"` // "info", "warn", "critical"
	IP        string `json:"ip"`
	Actor     string `json:"actor"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	UserAgent string `json:"userAgent"`
}

// SecurityLogSummary gives headline counts for the security log view.
type SecurityLogSummary struct {
	Counts         map[string]int `json:"counts"`
	Logins         SecurityLogins `json:"logins"`
	UniqueLoginIPs int            `json:"uniqueLoginIps"`
	Total          int            `json:"total"`
}

// SecurityLogins summarizes login statistics.
type SecurityLogins struct {
	Success   int `json:"success"`
	Failed    int `json:"failed"`
	ViaApiKey int `json:"viaApiKey"`
}

// InsertSecurityLog persists one security event.
func (r *Repo) InsertSecurityLog(ctx context.Context, at, typ, severity, ip, actor, method, path string, status int, detail, userAgent string) (int64, error) {
	if at == "" {
		at = time.Now().UTC().Format(time.RFC3339)
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO securityLog (at, type, severity, ip, actor, method, path, status, detail, userAgent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		at, typ, severity, ip, actor, method, path, status, detail, userAgent,
	)
	if err != nil {
		return 0, fmt.Errorf("insert security log: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		// The row is stored, but the caller asked for its id and got neither a
		// row id nor a silent success: a 0 here would read as "no such event"
		// to a caller that looks the event up again.
		return 0, fmt.Errorf("insert security log: last insert id: %w", err)
	}
	return id, nil
}

// TrimSecurityLog trims oldest rows so that no more than maxKeep rows remain.
func (r *Repo) TrimSecurityLog(ctx context.Context, maxKeep int) (int64, error) {
	if maxKeep <= 0 {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM securityLog WHERE id <= (SELECT MAX(id) - ? FROM securityLog)`,
		maxKeep,
	)
	if err != nil {
		return 0, fmt.Errorf("trim security log: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GetSecurityLogs retrieves recent security events ordered by id DESC.
func (r *Repo) GetSecurityLogs(ctx context.Context, limit int, severity string) ([]SecurityLogEntry, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	var rows *sql.Rows
	var err error
	if severity != "" {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, at, type, severity, ip, actor, method, path, status, detail, userAgent
			 FROM securityLog WHERE severity = ? ORDER BY id DESC LIMIT ?`,
			severity, limit,
		)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, at, type, severity, ip, actor, method, path, status, detail, userAgent
			 FROM securityLog ORDER BY id DESC LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query security log: %w", err)
	}
	defer rows.Close()

	entries := make([]SecurityLogEntry, 0)
	for rows.Next() {
		var e SecurityLogEntry
		var actor, method, path, detail, userAgent sql.NullString
		var status sql.NullInt64
		if err := rows.Scan(
			&e.ID, &e.At, &e.Type, &e.Severity, &e.IP,
			&actor, &method, &path, &status, &detail, &userAgent,
		); err != nil {
			return nil, fmt.Errorf("scan security log: %w", err)
		}
		if actor.Valid {
			e.Actor = actor.String
		}
		if method.Valid {
			e.Method = method.String
		}
		if path.Valid {
			e.Path = path.String
		}
		if status.Valid {
			e.Status = int(status.Int64)
		}
		if detail.Valid {
			e.Detail = detail.String
		}
		if userAgent.Valid {
			e.UserAgent = userAgent.String
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate security log: %w", err)
	}
	return entries, nil
}

// ClearSecurityLogs deletes all events from the security log table.
func (r *Repo) ClearSecurityLogs(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM securityLog`)
	if err != nil {
		return fmt.Errorf("clear security log: %w", err)
	}
	return nil
}
