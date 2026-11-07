// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Notification represents a system or operator notification for the dashboard bell.
type Notification struct {
	ID        string `json:"id"`
	At        string `json:"at"`
	DedupeKey string `json:"dedupeKey,omitempty"`
	Type      string `json:"type"`     // "quota", "key", "provider", "update", "security", etc.
	Severity  string `json:"severity"` // "info", "warn", "critical"
	Title     string `json:"title"`
	Body      string `json:"body"`
	ReadAt    string `json:"readAt,omitempty"`
	AckAt     string `json:"ackAt,omitempty"`
	Read      bool   `json:"read"`
	Acked     bool   `json:"acked"`
}

// Notify creates a notification or refreshes an existing un-acknowledged condition by dedupeKey.
// If dedupeKey already exists, updates title, body, and severity without moving the original timestamp.
// If it was previously acknowledged and the condition recurred, clears ackAt to re-alert.
func (r *Repo) Notify(ctx context.Context, id, at, dedupeKey, typ, severity, title, body string) (string, error) {
	if title == "" {
		return "", fmt.Errorf("notify: title is required")
	}
	if at == "" {
		at = time.Now().UTC().Format(time.RFC3339)
	}
	if severity == "" {
		severity = "info"
	}
	if typ == "" {
		typ = "info"
	}

	if dedupeKey != "" {
		notifID, handled, err := r.notifyDeduped(ctx, dedupeKey, typ, severity, title, body)
		if handled || err != nil {
			return notifID, err
		}
	}

	if id == "" {
		id = "ntf_" + uuid.New().String()
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notifications (id, at, dedupeKey, type, severity, title, body, readAt, ackAt)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', '')`,
		id, at, dedupeKey, typ, severity, title, body,
	)
	if err != nil {
		// Two processes can alert on the same condition in the same instant and
		// both pass the probe above; the partial unique index makes exactly one
		// insert win. The loser folds into the winner's row rather than
		// reporting a failure for a notification that does exist.
		if dedupeKey != "" && isUniqueConstraint(err) {
			notifID, handled, dedupeErr := r.notifyDeduped(ctx, dedupeKey, typ, severity, title, body)
			if handled && dedupeErr == nil {
				return notifID, nil
			}
		}
		return "", fmt.Errorf("insert notification: %w", err)
	}
	return id, nil
}

// notifyDeduped refreshes the existing notification a dedupeKey names. handled
// is false when no row matches, which is the caller's cue to insert. An acked
// row is re-armed: the condition recurred, so the badge must come back.
func (r *Repo) notifyDeduped(ctx context.Context, dedupeKey, typ, severity, title, body string) (string, bool, error) {
	var existingID, existingAckAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, ackAt FROM notifications WHERE dedupeKey = ? LIMIT 1`, dedupeKey,
	).Scan(&existingID, &existingAckAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("probe dedupe notification: %w", err)
	}
	if existingAckAt != "" {
		// cleared/acked before -> re-arm as unread
		_, err = r.db.ExecContext(ctx,
			`UPDATE notifications
			 SET title = ?, body = ?, severity = ?, type = ?, ackAt = '', readAt = ''
			 WHERE id = ?`,
			title, body, severity, typ, existingID,
		)
	} else {
		_, err = r.db.ExecContext(ctx,
			`UPDATE notifications
			 SET title = ?, body = ?, severity = ?, type = ?
			 WHERE id = ?`,
			title, body, severity, typ, existingID,
		)
	}
	if err != nil {
		return "", false, fmt.Errorf("update dedupe notification %s: %w", dedupeKey, err)
	}
	return existingID, true, nil
}

// isUniqueConstraint reports a UNIQUE/PK violation, narrowly: the fallback
// paths that call it must only swallow the lost-race case, not a CHECK or FK
// failure, which mean the row was not stored.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "PK constraint failed")
}

// ListNotifications returns notifications ordered by at DESC, capped by limit (default 50).
func (r *Repo) ListNotifications(ctx context.Context, limit int) ([]Notification, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, at, dedupeKey, type, severity, title, body, readAt, ackAt
		 FROM notifications ORDER BY at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	list := make([]Notification, 0)
	for rows.Next() {
		var n Notification
		var dedupeKey, body, readAt, ackAt sql.NullString
		if err := rows.Scan(
			&n.ID, &n.At, &dedupeKey, &n.Type, &n.Severity, &n.Title, &body, &readAt, &ackAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		if dedupeKey.Valid {
			n.DedupeKey = dedupeKey.String
		}
		if body.Valid {
			n.Body = body.String
		}
		if readAt.Valid {
			n.ReadAt = readAt.String
		}
		if ackAt.Valid {
			n.AckAt = ackAt.String
		}
		n.Read = n.ReadAt != ""
		n.Acked = n.AckAt != ""
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate notifications: %w", err)
	}

	unread, err := r.GetUnreadNotificationCount(ctx)
	if err != nil {
		return list, 0, err
	}
	return list, unread, nil
}

// GetUnreadNotificationCount returns the number of notifications that have not been read and not acked.
func (r *Repo) GetUnreadNotificationCount(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM notifications WHERE (readAt IS NULL OR readAt = '') AND (ackAt IS NULL OR ackAt = '')`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

// MarkNotificationRead marks one notification (or all if id == "" or "all") as read.
func (r *Repo) MarkNotificationRead(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if id == "" || id == "all" {
		_, err := r.db.ExecContext(ctx,
			`UPDATE notifications SET readAt = ? WHERE readAt IS NULL OR readAt = ''`,
			now,
		)
		if err != nil {
			return fmt.Errorf("mark all notifications read: %w", err)
		}
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE notifications SET readAt = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("mark notification read %s: %w", id, err)
	}
	return nil
}

// AckNotification marks a notification as acknowledged (retires the condition from active alert).
func (r *Repo) AckNotification(ctx context.Context, id, dedupeKey string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if id == "all" {
		_, err := r.db.ExecContext(ctx,
			`UPDATE notifications
			 SET ackAt = ?, readAt = CASE WHEN readAt = '' THEN ? ELSE readAt END
			 WHERE ackAt IS NULL OR ackAt = ''`,
			now, now,
		)
		if err != nil {
			return fmt.Errorf("ack all notifications: %w", err)
		}
		return nil
	}
	if id != "" {
		_, err := r.db.ExecContext(ctx,
			`UPDATE notifications
			 SET ackAt = ?, readAt = CASE WHEN readAt = '' THEN ? ELSE readAt END
			 WHERE id = ?`,
			now, now, id,
		)
		if err != nil {
			return fmt.Errorf("ack notification %s: %w", id, err)
		}
		return nil
	}
	if dedupeKey != "" {
		_, err := r.db.ExecContext(ctx,
			`UPDATE notifications
			 SET ackAt = ?, readAt = CASE WHEN readAt = '' THEN ? ELSE readAt END
			 WHERE dedupeKey = ?`,
			now, now, dedupeKey,
		)
		if err != nil {
			return fmt.Errorf("ack notification by dedupeKey %s: %w", dedupeKey, err)
		}
		return nil
	}
	return nil
}

// ClearNotification retires a condition dedupeKey so that when condition returns, it can re-alert.
func (r *Repo) ClearNotification(ctx context.Context, dedupeKey string) error {
	return r.AckNotification(ctx, "", dedupeKey)
}
