// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/handlerutil"
)

// Handler serves the /api/webhooks dashboard API (FASE 10.4): CRUD over the
// webhookTargets table plus a per-target test ping. It is mounted behind the
// dashboard auth group — a webhook URL is a credential (a Discord or Slack
// webhook URL grants posting) and a write target, so an engine client API key
// must not be able to read or aim one.
type Handler struct {
	Repo *db.Repo
}

// NewHandler builds the API over a repository.
func NewHandler(repo *db.Repo) *Handler {
	return &Handler{Repo: repo}
}

// Register mounts the webhook routes on r. chi is the router the server
// already runs, so the {id} params resolve the same way the rest of the
// dashboard API's do.
func (h *Handler) Register(r chi.Router) {
	r.Get("/api/webhooks", h.HandleList)
	r.Post("/api/webhooks", h.HandleCreate)
	r.Put("/api/webhooks/{id}", h.HandleUpdate)
	r.Delete("/api/webhooks/{id}", h.HandleDelete)
	r.Post("/api/webhooks/{id}/test", h.HandleTest)
}

// targetRequest is a create/update payload. Every field is a pointer so an
// update that omits one keeps the stored value instead of resetting it.
type targetRequest struct {
	Type         *string `json:"type"`
	URL          *string `json:"url"`
	Enabled      *bool   `json:"enabled"`
	FilterEvents *string `json:"filterEvents"`
}

// HandleList handles GET /api/webhooks.
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	targets, err := h.Repo.ListWebhookTargets(r.Context())
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, targets)
}

// HandleCreate handles POST /api/webhooks.
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeTargetRequest(w, r)
	if !ok {
		return
	}
	created, err := h.apply(db.WebhookTarget{Enabled: true, FilterEvents: "*"}, req)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	created.ID = "wh_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	if err := h.Repo.SaveWebhookTarget(r.Context(), created); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, created)
}

// HandleUpdate handles PUT /api/webhooks/{id}.
func (h *Handler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing webhook target id")
		return
	}
	existing, err := h.Repo.GetWebhookTarget(r.Context(), id)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "webhook target not found")
		return
	}
	req, ok := decodeTargetRequest(w, r)
	if !ok {
		return
	}
	updated, err := h.apply(*existing, req)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Repo.SaveWebhookTarget(r.Context(), updated); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, updated)
}

// HandleDelete handles DELETE /api/webhooks/{id}.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing webhook target id")
		return
	}
	if err := h.Repo.DeleteWebhookTarget(r.Context(), id); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": id})
}

// HandleTest handles POST /api/webhooks/{id}/test: one synchronous test.ping
// to that target, whose error — if any — is reported to the operator instead
// of logged. It deliberately bypasses the dispatcher's rate limit: the button
// must answer honestly twice in a row.
func (h *Handler) HandleTest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := h.Repo.GetWebhookTarget(r.Context(), id)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if row == nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "webhook target not found")
		return
	}
	t := NewTarget(row.Type, row.URL)
	if t == nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "unknown target type: "+row.Type)
		return
	}
	if !t.Enabled() {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "webhook target is not fully configured")
		return
	}
	err = t.Send(Event{
		Type:     EventTestPing,
		Title:    "9RouterX test ping",
		Body:     "A test notification from the dashboard.",
		Severity: SeverityInfo,
	})
	if err != nil {
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// decodeTargetRequest reads and parses the request body. The bool return is
// false when the error response has already been written.
func decodeTargetRequest(w http.ResponseWriter, r *http.Request) (targetRequest, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return targetRequest{}, false
	}
	defer r.Body.Close()

	var req targetRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
			return targetRequest{}, false
		}
	}
	return req, true
}

// apply merges a dashboard payload onto a target row and validates the result,
// so a create and an update enforce the identical contract. The returned row
// carries no id: the caller assigns it (a new one on create, the existing one
// on update).
func (h *Handler) apply(base db.WebhookTarget, req targetRequest) (db.WebhookTarget, error) {
	if req.Type != nil {
		base.Type = strings.TrimSpace(*req.Type)
	}
	if req.URL != nil {
		base.URL = strings.TrimSpace(*req.URL)
	}
	if req.Enabled != nil {
		base.Enabled = *req.Enabled
	}
	if req.FilterEvents != nil {
		base.FilterEvents = strings.TrimSpace(*req.FilterEvents)
		if base.FilterEvents == "" {
			base.FilterEvents = "*"
		}
	}
	if base.Type == "" {
		return db.WebhookTarget{}, errBadRequest("type is required")
	}
	if base.URL == "" {
		return db.WebhookTarget{}, errBadRequest("url is required")
	}
	if err := ValidateTarget(base.Type, base.URL); err != nil {
		return db.WebhookTarget{}, err
	}
	if !validFilterEvents(base.FilterEvents) {
		return db.WebhookTarget{}, errBadRequest(
			`filterEvents must be "*" or a JSON array of event types`)
	}
	return base, nil
}

// validFilterEvents reports whether raw is a storable filterEvents value:
// "*" or a JSON array of event type strings. MatchesFilter is fail-open on an
// unparseable value, so the boundary must not store one.
func validFilterEvents(raw string) bool {
	if raw == "*" {
		return true
	}
	var filters []string
	return json.Unmarshal([]byte(raw), &filters) == nil
}

// errBadRequest shapes a validation failure. A thin wrapper so apply's error
// paths read the same whether they come from this package's validation or from
// the SSRF guard.
func errBadRequest(message string) error {
	return &requestError{message: message}
}

type requestError struct{ message string }

func (e *requestError) Error() string { return e.message }
