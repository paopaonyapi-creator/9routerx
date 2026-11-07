// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/keikey"
	"9routerx/proxy/internal/log"

	"9routerx/proxy/internal/handlers/chat"
)

// Per-key model visibility (FASE 10.3) — dashboard surface.
//
// Hidden is the visibility axis: the patterns here are dropped from the key's
// /v1/models listing and a dispatch attempt answers 404, so a client cannot
// tell a hidden model from one that does not exist. This file mirrors
// apikeys_models.go (the allowlist surface) on purpose: same routes one
// segment apart, same validation limits, different column and different
// failure semantics.

// HandleGetApiKeyHiddenModels handles GET /api/keys/{id}/hidden-models.
// Returns the key's hide-list patterns; an empty list hides nothing.
func (h *DashboardHandler) HandleGetApiKeyHiddenModels(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing apiKey id")
		return
	}
	raw, err := h.Repo.GetApiKeyHiddenModels(id)
	if err != nil {
		log.Error("apikeys", "read hidden models failed", "error", err, "id", id)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to read hidden models")
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"id":       id,
		"patterns": parseHiddenPatternsJSON(raw),
	})
}

// HandleSetApiKeyHiddenModels handles PUT /api/keys/{id}/hidden-models.
// Accepts {"patterns": ["a", "b"]}. An empty list hides nothing and restores
// the pre-feature default. The stored form is the JSON array the chat filter
// parses.
func (h *DashboardHandler) HandleSetApiKeyHiddenModels(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing apiKey id")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	patterns, err := parseHiddenModelPatterns(body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	stored, err := json.Marshal(patterns)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to encode hidden models")
		return
	}
	if err := h.Repo.SetApiKeyHiddenModels(id, string(stored)); err != nil {
		log.Error("apikeys", "write hidden models failed", "error", err, "id", id)
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to save hidden models")
		return
	}
	// The chat-side visibility filter caches merged lists for 30s. A policy
	// write is not a latency trade-off: the operator just hid a model, so the
	// cached list must go now — the same contract keikey's auth-cache
	// invalidation follows for the other governance writes.
	chat.InvalidateHiddenModelsCache()
	keikey.DefaultAuthCache().InvalidateByID(id)

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"id":       id,
		"patterns": patterns,
	})
}

// parseHiddenModelPatterns accepts the wrapped object form and the bare array
// form, like the allowlist endpoint. Entries are trimmed and blanks dropped;
// the size limits match the allowlist's, because both lists are matched
// against every candidate id on every request.
func parseHiddenModelPatterns(body []byte) ([]string, error) {
	if len(body) == 0 {
		return []string{}, nil
	}
	var wrapped struct {
		Patterns []string `json:"patterns"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Patterns != nil {
		return validateHiddenModelPatterns(cleanModelPatterns(wrapped.Patterns))
	}
	var bare []string
	if err := json.Unmarshal(body, &bare); err != nil {
		return nil, fmt.Errorf("patterns must be a string array")
	}
	return validateHiddenModelPatterns(cleanModelPatterns(bare))
}

// validateHiddenModelPatterns rejects an oversized hide list with a message
// naming the limit.
func validateHiddenModelPatterns(patterns []string) ([]string, error) {
	if len(patterns) > maxAllowlistEntries {
		return nil, fmt.Errorf("too many hidden model patterns: %d, maximum is %d", len(patterns), maxAllowlistEntries)
	}
	for _, p := range patterns {
		if len(p) > maxAllowlistEntryLength {
			return nil, fmt.Errorf("hidden model pattern exceeds %d characters: %q", maxAllowlistEntryLength, truncateForError(p))
		}
	}
	return patterns, nil
}

// parseHiddenPatternsJSON decodes a stored hide list for the wire. A stored
// value the dashboard did not write (a hand edit) reads as an empty list
// rather than failing the read.
func parseHiddenPatternsJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var patterns []string
	if err := json.Unmarshal([]byte(raw), &patterns); err != nil {
		return []string{}
	}
	return patterns
}
