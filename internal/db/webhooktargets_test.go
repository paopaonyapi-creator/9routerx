// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package db

import (
	"context"
	"testing"
)

func TestWebhookTargetCRUDRoundTrip(t *testing.T) {
	r := openAuditTestRepo(t)
	ctx := context.Background()

	// Create.
	first := WebhookTarget{
		ID: "wh-1", Type: "discord", URL: "https://discord.com/api/webhooks/1/abc",
		Enabled: true, FilterEvents: `["quota.exceeded"]`, CreatedAt: "2026-01-01T00:00:00Z",
	}
	if err := r.SaveWebhookTarget(ctx, first); err != nil {
		t.Fatalf("SaveWebhookTarget(create): %v", err)
	}
	second := WebhookTarget{
		ID: "wh-2", Type: "slack", URL: "https://hooks.slack.com/services/a/b/c",
		Enabled: false, FilterEvents: "*",
	}
	if err := r.SaveWebhookTarget(ctx, second); err != nil {
		t.Fatalf("SaveWebhookTarget(create 2): %v", err)
	}
	// A caller that omits CreatedAt gets a stamp: the struct is passed by
	// value, so the stamp is checked on the stored row, not on the local copy.
	stored, err := r.GetWebhookTarget(ctx, "wh-2")
	if err != nil || stored == nil {
		t.Fatalf("GetWebhookTarget(wh-2) = %v, %v; want the row", stored, err)
	}
	if stored.CreatedAt == "" {
		t.Error("stored row has an empty CreatedAt; SaveWebhookTarget should stamp it")
	}

	// Read back.
	got, err := r.GetWebhookTarget(ctx, "wh-1")
	if err != nil {
		t.Fatalf("GetWebhookTarget: %v", err)
	}
	if got == nil || got.Type != "discord" || !got.Enabled || got.FilterEvents != `["quota.exceeded"]` {
		t.Fatalf("round-tripped target = %+v, want the stored discord row", got)
	}

	list, err := r.ListWebhookTargets(ctx)
	if err != nil {
		t.Fatalf("ListWebhookTargets: %v", err)
	}
	if len(list) != 2 || list[0].ID != "wh-1" || list[1].ID != "wh-2" {
		t.Fatalf("list = %+v, want wh-1 then wh-2 in creation order", list)
	}
	if list[1].Enabled {
		t.Error("disabled target read back as enabled")
	}

	// Update replaces the row in place, keeping its creation stamp.
	updated := first
	updated.URL = "https://discord.com/api/webhooks/1/rotated"
	updated.Enabled = false
	if err := r.SaveWebhookTarget(ctx, updated); err != nil {
		t.Fatalf("SaveWebhookTarget(update): %v", err)
	}
	list, err = r.ListWebhookTargets(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list after update = %d entries (err %v), want 2", len(list), err)
	}
	if list[0].URL != updated.URL || list[0].Enabled || list[0].CreatedAt != first.CreatedAt {
		t.Errorf("updated row = %+v, want the new URL, disabled, original createdAt", list[0])
	}

	// Delete.
	if err := r.DeleteWebhookTarget(ctx, "wh-1"); err != nil {
		t.Fatalf("DeleteWebhookTarget: %v", err)
	}
	gone, err := r.GetWebhookTarget(ctx, "wh-1")
	if err != nil || gone != nil {
		t.Fatalf("GetWebhookTarget after delete = %v, %v; want nil, nil", gone, err)
	}
	// Deleting an unknown id is not an error: the row is gone either way.
	if err := r.DeleteWebhookTarget(ctx, "wh-unknown"); err != nil {
		t.Errorf("DeleteWebhookTarget(unknown) = %v, want nil", err)
	}
	if list, err := r.ListWebhookTargets(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list after delete = %d entries (err %v), want 1", len(list), err)
	}
}

func TestSaveWebhookTargetRequiresID(t *testing.T) {
	r := openAuditTestRepo(t)
	if err := r.SaveWebhookTarget(context.Background(), WebhookTarget{Type: "generic"}); err == nil {
		t.Fatal("SaveWebhookTarget without id = nil error, want a failure")
	}
}
