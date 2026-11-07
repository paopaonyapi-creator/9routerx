// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"9routerx/proxy/internal/db"
)

func TestNotificationEndpoints(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	h := NewDashboardHandler(repo, nil)
	ctx := context.Background()

	// Seed notifications
	id1, err := repo.Notify(ctx, "", "", "quota:k1", "quota", "critical", "Quota Exceeded", "Key has spent 100% tokens")
	if err != nil {
		t.Fatalf("notify 1: %v", err)
	}
	_, err = repo.Notify(ctx, "", "", "update:1.9.0", "update", "info", "Update available", "Version 1.9.0 is out")
	if err != nil {
		t.Fatalf("notify 2: %v", err)
	}

	t.Run("GET /api/notifications lists notifications and unread count", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/notifications", nil)
		rec := httptest.NewRecorder()
		h.HandleGetNotifications(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Notifications []db.Notification `json:"notifications"`
			Unread        int               `json:"unread"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if len(resp.Notifications) != 2 || resp.Unread != 2 {
			t.Fatalf("len = %d, unread = %d, want 2 and 2", len(resp.Notifications), resp.Unread)
		}
	})

	t.Run("GET /api/notifications/unread-count", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/notifications/unread-count", nil)
		rec := httptest.NewRecorder()
		h.HandleGetUnreadNotificationCount(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Unread int `json:"unread"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if resp.Unread != 2 {
			t.Fatalf("unread = %d, want 2", resp.Unread)
		}
	})

	t.Run("POST /api/notifications action: read", func(t *testing.T) {
		body := []byte(`{"action":"read","id":"` + id1 + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/notifications", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandlePostNotifications(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp struct {
			Ok     bool `json:"ok"`
			Unread int  `json:"unread"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if !resp.Ok || resp.Unread != 1 {
			t.Fatalf("ok = %v, unread = %d, want true and 1", resp.Ok, resp.Unread)
		}
	})

	t.Run("POST /api/notifications/ack acks notification", func(t *testing.T) {
		body := []byte(`{"id":"` + id1 + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/notifications/ack", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleAckNotifications(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		list, _, _ := repo.ListNotifications(ctx, 10)
		for _, n := range list {
			if n.ID == id1 && !n.Acked {
				t.Fatalf("notification %s should be acked", id1)
			}
		}
	})

	t.Run("POST /api/notifications/scan triggers condition check", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/notifications/scan", nil)
		rec := httptest.NewRecorder()
		h.HandleScanNotifications(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}
