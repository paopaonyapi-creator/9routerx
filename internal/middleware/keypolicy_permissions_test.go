// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9routerx/proxy/internal/models"
)

// keypolicy_permissions_test.go covers the per-key endpoint permissions
// (port(serenhope) TASK-1.6): the empty default that allows every family, the
// restriction list, the versioned path aliases, the unmapped-path pass-through
// and the fail-open parse.

func TestEndpointPermissionsGate(t *testing.T) {
	newPolicy := func() *KeyPolicy {
		p := NewKeyPolicy(nil)
		p.now = fixedClock(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
		return p
	}
	run := func(p *KeyPolicy, path string, perms string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		return runKeyPolicyReq(t, p, req, &models.APIKey{ID: "k-perm", Permissions: perms})
	}

	t.Run("empty_permissions_allow_every_family", func(t *testing.T) {
		p := newPolicy()
		for _, path := range []string{
			"/v1/chat/completions", "/v1/messages", "/v1/responses",
			"/v1/models", "/v1/embeddings", "/v1/files", "/v1/batches",
			"/images/generations", "/audio/speech", "/search",
		} {
			if rec := run(p, path, ""); rec.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200 with no permissions set", path, rec.Code)
			}
		}
	})

	t.Run("chat_only_restricts_the_other_families", func(t *testing.T) {
		p := newPolicy()
		perms := `{"endpoints":["chat"],"admin":false}`
		for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/responses", "/api/chat"} {
			if rec := run(p, path, perms); rec.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200 (chat family)", path, rec.Code)
			}
		}
		for _, path := range []string{"/v1/embeddings", "/v1/models", "/images/generations", "/search", "/v1/files"} {
			rec := run(p, path, perms)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403 outside the chat family", path, rec.Code)
				continue
			}
			if !strings.Contains(rec.Body.String(), `"permission_denied"`) {
				t.Errorf("%s: body = %s, want the permission_denied code", path, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "This API key does not have permission for this endpoint") {
				t.Errorf("%s: body = %s, want upstream's exact sentence", path, rec.Body.String())
			}
		}
	})

	t.Run("versioned_aliases_map_to_the_same_family", func(t *testing.T) {
		p := newPolicy()
		perms := `{"endpoints":["models"]}`
		for _, path := range []string{"/v1/models", "/api/v1/models", "/api/models", "/models/gpt-4o"} {
			if rec := run(p, path, perms); rec.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200 for the models family", path, rec.Code)
			}
		}
	})

	t.Run("unmapped_paths_are_never_gated", func(t *testing.T) {
		p := newPolicy()
		perms := `{"endpoints":["chat"]}`
		// Infra endpoints (headroom, usage streams) belong to no family, so a
		// restrictive key is not blocked from them — they are guarded by other
		// layers, and the admin surface never passes through key auth.
		for _, path := range []string{"/headroom/status", "/api/usage/stream"} {
			if rec := run(p, path, perms); rec.Code != http.StatusOK {
				t.Errorf("%s: status = %d, want 200 (unmapped path)", path, rec.Code)
			}
		}
	})

	t.Run("unparsable_permissions_fail_open", func(t *testing.T) {
		p := newPolicy()
		// A restore or hand edit can store garbage; a typo must not lock the
		// key's every endpoint down at once.
		for _, perms := range []string{"not json", `{"endpoints":["nope"]}`} {
			if rec := run(p, "/v1/chat/completions", perms); rec.Code != http.StatusOK {
				t.Errorf("perms %q: status = %d, want 200 (fail-open)", perms, rec.Code)
			}
		}
	})
}

func TestEndpointFamilyForPath(t *testing.T) {
	tests := []struct {
		path   string
		family models.KeyEndpointFamily
		ok     bool
	}{
		{"/chat/completions", models.KeyEndpointChat, true},
		{"/v1/chat/completions", models.KeyEndpointChat, true},
		{"/api/v1/chat/completions", models.KeyEndpointChat, true},
		{"/messages", models.KeyEndpointChat, true},
		{"/messages/count_tokens", models.KeyEndpointChat, true},
		{"/v1/responses/compact", models.KeyEndpointChat, true},
		{"/api/chat", models.KeyEndpointChat, true},
		{"/v1/models", models.KeyEndpointModels, true},
		{"/models/info", models.KeyEndpointModels, true},
		{"/embeddings", models.KeyEndpointEmbeddings, true},
		{"/rerank", models.KeyEndpointEmbeddings, true},
		{"/images/edits", models.KeyEndpointMedia, true},
		{"/audio/transcriptions", models.KeyEndpointMedia, true},
		{"/videos/generations", models.KeyEndpointMedia, true},
		{"/search", models.KeyEndpointWeb, true},
		{"/web/fetch", models.KeyEndpointWeb, true},
		{"/systemone", models.KeyEndpointWeb, true},
		{"/v1/files/x/content", models.KeyEndpointFiles, true},
		{"/batches", models.KeyEndpointBatches, true},
		{"/headroom/status", "", false},
		{"/api/usage/stats", "", false},
		{"/", "", false},
	}
	for _, tt := range tests {
		family, ok := models.EndpointFamilyForPath(tt.path)
		if ok != tt.ok || family != tt.family {
			t.Errorf("%s = (%q, %v), want (%q, %v)", tt.path, family, ok, tt.family, tt.ok)
		}
	}
}
