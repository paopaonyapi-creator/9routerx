// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"9routerx/proxy/internal/db"
)

// newAPITestHandler opens a repo with the webhookTargets table and mounts the
// real route table over it.
func newAPITestHandler(t *testing.T) (chi.Router, *db.Repo) {
	t.Helper()
	conn, err := db.OpenDatabase(t.TempDir() + "/api.sqlite")
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.EnsureKeiRouterTables(conn); err != nil {
		t.Fatalf("EnsureKeiRouterTables: %v", err)
	}
	repo := db.NewRepo(conn)
	r := chi.NewRouter()
	NewHandler(repo).Register(r)
	return r, repo
}

func doJSON(t *testing.T, r chi.Router, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestWebhookAPICRUDRoundTrip(t *testing.T) {
	r, repo := newAPITestHandler(t)
	ctx := context.Background()

	dest, hits, _ := captureServer(t)
	pointAtTestServer(t)

	// Create.
	rec := doJSON(t, r, http.MethodPost, "/api/webhooks", map[string]any{
		"type": TargetGeneric, "url": dest.URL, "enabled": true,
		"filterEvents": `["quota.exceeded","test.ping"]`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/webhooks = %d: %s", rec.Code, rec.Body.String())
	}
	var created db.WebhookTarget
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created target: %v", err)
	}
	if !strings.HasPrefix(created.ID, "wh_") || created.Type != TargetGeneric {
		t.Fatalf("created = %+v, want a wh_ id and the generic type", created)
	}

	// List.
	rec = doJSON(t, r, http.MethodGet, "/api/webhooks", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/webhooks = %d", rec.Code)
	}
	var list []db.WebhookTarget
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v, want the created row", list)
	}

	// Update: rotate the URL and disable; the stored row keeps its id.
	rec = doJSON(t, r, http.MethodPut, "/api/webhooks/"+created.ID, map[string]any{
		"enabled": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/webhooks/%s = %d: %s", created.ID, rec.Code, rec.Body.String())
	}
	stored, err := repo.GetWebhookTarget(ctx, created.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetWebhookTarget after update = %v, %v", stored, err)
	}
	if stored.Enabled || stored.URL != dest.URL {
		t.Errorf("updated row = %+v, want disabled with the original url kept", stored)
	}

	// Test ping: delivers a test.ping envelope to the destination and says so.
	rec = doJSON(t, r, http.MethodPost, "/api/webhooks/"+created.ID+"/test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST test = %d: %s", rec.Code, rec.Body.String())
	}
	var testResult map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &testResult); err != nil {
		t.Fatalf("decode test result: %v", err)
	}
	if testResult["ok"] != true {
		t.Fatalf("test result = %v, want ok", testResult)
	}
	if hits.Load() != 1 {
		t.Fatalf("destination hits = %d, want 1 (the synchronous test ping)", hits.Load())
	}

	// Delete.
	rec = doJSON(t, r, http.MethodDelete, "/api/webhooks/"+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d", rec.Code)
	}
	if list, err := repo.ListWebhookTargets(ctx); err != nil || len(list) != 0 {
		t.Fatalf("list after delete = %d entries (err %v), want 0", len(list), err)
	}
}

// TestWebhookAPITestPingCarriesEvent proves the test endpoint sends the
// test.ping event shape, not just any payload.
func TestWebhookAPITestPingCarriesEvent(t *testing.T) {
	r, _ := newAPITestHandler(t)
	dest, _, bodies := captureServer(t)
	pointAtTestServer(t)

	rec := doJSON(t, r, http.MethodPost, "/api/webhooks", map[string]any{
		"type": TargetGeneric, "url": dest.URL,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var created db.WebhookTarget
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	rec = doJSON(t, r, http.MethodPost, "/api/webhooks/"+created.ID+"/test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("test = %d", rec.Code)
	}
	var body map[string]any
	select {
	case body = <-bodies:
	default:
		t.Fatal("destination received no test ping")
	}
	if body["event"] != EventTestPing || body["severity"] != SeverityInfo {
		t.Errorf("ping envelope = %v, want event %s severity %s", body, EventTestPing, SeverityInfo)
	}
}

func TestWebhookAPIValidation(t *testing.T) {
	r, _ := newAPITestHandler(t)

	cases := []struct {
		name       string
		method     string
		path       string
		body       map[string]any
		wantStatus int
		wantInBody string
	}{
		{
			name: "unknown type is refused", method: http.MethodPost, path: "/api/webhooks",
			body:       map[string]any{"type": "pigeon", "url": "https://example.com/hook"},
			wantStatus: http.StatusBadRequest, wantInBody: "unknown target type",
		},
		{
			name: "missing url is refused", method: http.MethodPost, path: "/api/webhooks",
			body:       map[string]any{"type": TargetDiscord},
			wantStatus: http.StatusBadRequest, wantInBody: "url is required",
		},
		{
			name: "invalid filter is refused", method: http.MethodPost, path: "/api/webhooks",
			body:       map[string]any{"type": TargetDiscord, "url": "https://example.com/hook", "filterEvents": "{oops"},
			wantStatus: http.StatusBadRequest, wantInBody: "filterEvents",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, r, tc.method, tc.path, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d (body %s), want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantInBody) {
				t.Errorf("body %q does not mention %q", rec.Body.String(), tc.wantInBody)
			}
		})
	}
}

// TestWebhookAPISSRFRejected proves the write path refuses an internal
// destination with the REAL guard in place, before anything is stored.
func TestWebhookAPISSRFRejected(t *testing.T) {
	r, repo := newAPITestHandler(t)

	rec := doJSON(t, r, http.MethodPost, "/api/webhooks", map[string]any{
		"type": TargetDiscord, "url": "http://127.0.0.1:9090/hook",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (body %s), want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "blocked URL") {
		t.Errorf("body = %s, want the SSRF block reason", rec.Body.String())
	}
	if list, err := repo.ListWebhookTargets(context.Background()); err != nil || len(list) != 0 {
		t.Fatalf("list after refused create = %d entries (err %v), want 0", len(list), err)
	}
}

func TestWebhookAPITestTargetNotFound(t *testing.T) {
	r, _ := newAPITestHandler(t)
	rec := doJSON(t, r, http.MethodPost, "/api/webhooks/wh_nope/test", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("test on unknown id = %d, want 404", rec.Code)
	}
	rec = doJSON(t, r, http.MethodPut, "/api/webhooks/wh_nope", map[string]any{"enabled": false})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update on unknown id = %d, want 404", rec.Code)
	}
}

// TestWebhookAPITestReportsDeliveryError proves the test button answers
// honestly: a destination that refuses reports the failure instead of "ok".
func TestWebhookAPITestReportsDeliveryError(t *testing.T) {
	r, _ := newAPITestHandler(t)
	pointAtTestServer(t)

	// A Discord target aimed at a server that answers 403.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	rec := doJSON(t, r, http.MethodPost, "/api/webhooks", map[string]any{
		"type": TargetDiscord, "url": srv.URL,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d", rec.Code)
	}
	var created db.WebhookTarget
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	rec = doJSON(t, r, http.MethodPost, "/api/webhooks/"+created.ID+"/test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("test = %d", rec.Code)
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["ok"] != false || !strings.Contains(toString(result["error"]), "403") {
		t.Errorf("result = %v, want ok=false with the delivery error", result)
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}
