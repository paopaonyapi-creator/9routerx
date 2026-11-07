// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	json "encoding/json/v2"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/notifications"
)

// maxNotificationLimit caps the ?limit= parameter. The bell asks for 20 and
// the feed for 50; a larger value only serves a caller probing how much the
// table holds, so it is clamped rather than honored.
const maxNotificationLimit = 200

// HandleGetNotifications handles GET /api/notifications.
func (h *DashboardHandler) HandleGetNotifications(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxNotificationLimit {
		limit = maxNotificationLimit
	}

	list, unread, err := h.Repo.ListNotifications(r.Context(), limit)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"notifications": list,
		"unread":        unread,
	})
}

// HandleGetUnreadNotificationCount handles GET /api/notifications/unread-count.
func (h *DashboardHandler) HandleGetUnreadNotificationCount(w http.ResponseWriter, r *http.Request) {
	unread, err := h.Repo.GetUnreadNotificationCount(r.Context())
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"unread": unread,
	})
}

// HandlePostNotifications handles POST /api/notifications (actions: "read", "ack", "clear").
func (h *DashboardHandler) HandlePostNotifications(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		Action    string `json:"action"`
		ID        string `json:"id"`
		DedupeKey string `json:"dedupeKey"`
		All       bool   `json:"all"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "read":
		target := req.ID
		if req.All {
			target = "all"
		}
		if err := h.Repo.MarkNotificationRead(r.Context(), target); err != nil {
			handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

	case "ack", "clear":
		target := req.ID
		if req.All {
			target = "all"
		}
		if err := h.Repo.AckNotification(r.Context(), target, req.DedupeKey); err != nil {
			handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

	default:
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "unknown action: "+action)
		return
	}

	unread, _ := h.Repo.GetUnreadNotificationCount(r.Context())
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"unread": unread,
	})
}

// HandleAckNotifications handles POST /api/notifications/ack.
func (h *DashboardHandler) HandleAckNotifications(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		ID        string `json:"id"`
		DedupeKey string `json:"dedupeKey"`
		All       bool   `json:"all"`
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &req)
	}

	target := req.ID
	if req.All {
		target = "all"
	}
	if err := h.Repo.AckNotification(r.Context(), target, req.DedupeKey); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	unread, _ := h.Repo.GetUnreadNotificationCount(r.Context())
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"unread": unread,
	})
}

// HandleScanNotifications handles POST /api/notifications/scan to trigger an on-demand condition check.
func (h *DashboardHandler) HandleScanNotifications(w http.ResponseWriter, r *http.Request) {
	scanner := notifications.NewScanner(h.Repo)
	res, err := scanner.Scan(r.Context())
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, res)
}
