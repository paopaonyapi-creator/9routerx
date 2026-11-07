// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// WebhookTarget is one multi-target webhook destination (FASE 10.4). Type is
// telegram|discord|slack|generic; URL is the destination the adapter posts to —
// for a telegram target it carries the Bot API token and the chat_id query
// parameter, since a Bot API send has no separate credential column.
// FilterEvents is "*" or a JSON array of event types the target subscribed to.
type WebhookTarget struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	Enabled      bool   `json:"enabled"`
	FilterEvents string `json:"filterEvents"`
	CreatedAt    string `json:"createdAt"`
}

// ListWebhookTargets returns every stored destination in creation order.
func (r *Repo) ListWebhookTargets(ctx context.Context) ([]WebhookTarget, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, type, url, enabled, filterEvents, createdAt FROM webhookTargets ORDER BY createdAt, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list webhook targets: %w", err)
	}
	defer rows.Close()

	targets := make([]WebhookTarget, 0)
	for rows.Next() {
		var t WebhookTarget
		var enabled int
		if err := rows.Scan(&t.ID, &t.Type, &t.URL, &enabled, &t.FilterEvents, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan webhook target: %w", err)
		}
		t.Enabled = enabled != 0
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhook targets: %w", err)
	}
	return targets, nil
}

// SaveWebhookTarget inserts or replaces one destination. An empty CreatedAt is
// stamped here so a caller that only knows the fields it edited still stores a
// complete row.
func (r *Repo) SaveWebhookTarget(ctx context.Context, target WebhookTarget) error {
	if target.ID == "" {
		return fmt.Errorf("save webhook target: id is required")
	}
	if target.CreatedAt == "" {
		target.CreatedAt = nowUTC()
	}
	enabled := 0
	if target.Enabled {
		enabled = 1
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO webhookTargets (id, type, url, enabled, filterEvents, createdAt)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   type = excluded.type, url = excluded.url, enabled = excluded.enabled,
		   filterEvents = excluded.filterEvents`,
		target.ID, target.Type, target.URL, enabled, target.FilterEvents, target.CreatedAt,
	); err != nil {
		return fmt.Errorf("save webhook target %s: %w", target.ID, err)
	}
	return nil
}

// DeleteWebhookTarget removes one destination. Deleting an unknown id is not an
// error: the destination the caller wanted gone is gone.
func (r *Repo) DeleteWebhookTarget(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM webhookTargets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete webhook target %s: %w", id, err)
	}
	return nil
}

// GetWebhookTarget returns one destination, or nil when the id is unknown.
func (r *Repo) GetWebhookTarget(ctx context.Context, id string) (*WebhookTarget, error) {
	var t WebhookTarget
	var enabled int
	err := r.db.QueryRowContext(ctx,
		`SELECT id, type, url, enabled, filterEvents, createdAt FROM webhookTargets WHERE id = ?`, id,
	).Scan(&t.ID, &t.Type, &t.URL, &enabled, &t.FilterEvents, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get webhook target %s: %w", id, err)
	}
	t.Enabled = enabled != 0
	return &t, nil
}
