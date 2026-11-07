// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchCustomCreditsUsage(t *testing.T) {
	t.Run("missing_base_url_reports_error", func(t *testing.T) {
		res, ok := fetchCustomCreditsUsage(context.Background(), "openai-compatible-custom", map[string]any{
			"apiKey": "sk-123",
		})
		if !ok || res.message == "" {
			t.Fatalf("expected error message for missing base URL, got %+v", res)
		}
	})

	t.Run("missing_api_key_reports_error", func(t *testing.T) {
		res, ok := fetchCustomCreditsUsage(context.Background(), "openai-compatible-custom", map[string]any{
			"baseUrl": "https://custom.api/v1",
		})
		if !ok || res.message == "" {
			t.Fatalf("expected error message for missing API key, got %+v", res)
		}
	})

	t.Run("parses_credits_response_successfully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/credits" {
				http.NotFound(w, r)
				return
			}
			if r.Header.Get("Authorization") != "Bearer sk-custom" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"credits": 75.5, "total": 100.0, "used": 24.5}`))
		}))
		defer srv.Close()

		res, ok := fetchCustomCreditsUsage(context.Background(), "openai-compatible-custom", map[string]any{
			"apiKey": "sk-custom",
			"providerSpecificData": map[string]any{
				"baseUrl":  srv.URL,
				"nodeName": "My Node",
			},
		})
		if !ok {
			t.Fatal("expected ok = true")
		}
		if res.plan != "My Node" {
			t.Errorf("plan = %q, want 'My Node'", res.plan)
		}
		cr, ok := res.quotas["Credits"].(map[string]any)
		if !ok {
			t.Fatalf("credits quota missing: %+v", res.quotas)
		}
		if cr["used"] != 24.5 || cr["total"] != 100.0 || cr["remainingPercentage"] != 75.5 {
			t.Errorf("credits mismatch: %+v", cr)
		}
	})

	t.Run("handles_401_authentication_failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		res, ok := fetchCustomCreditsUsage(context.Background(), "anthropic-compatible-test", map[string]any{
			"apiKey": "sk-bad",
			"baseUrl": srv.URL,
		})
		if !ok || res.message == "" {
			t.Fatalf("expected 401 message: %+v", res)
		}
	})
}
