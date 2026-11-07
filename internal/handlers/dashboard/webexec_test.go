// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/sessionvault"
	"9routerx/proxy/internal/webexec"
)

// errValidationFailed is the injected validator's verdict for the expired
// session case.
var errValidationFailed = errors.New("validation failed")

// setupWebExecTestDB extends the shared dashboard fixture with the
// notifications table the session-expired alert writes, and returns a handler
// wired to the caller's runner.
func setupWebExecTestDB(t *testing.T) (*db.Repo, func()) {
	t.Helper()
	repo, cleanup := setupTestDB(t)
	_, err := repo.RawDB().Exec(`CREATE TABLE IF NOT EXISTS notifications (
		id TEXT PRIMARY KEY,
		at TEXT NOT NULL,
		dedupeKey TEXT DEFAULT '',
		type TEXT NOT NULL,
		severity TEXT NOT NULL,
		title TEXT NOT NULL,
		body TEXT DEFAULT '',
		readAt TEXT DEFAULT '',
		ackAt TEXT DEFAULT ''
	)`)
	if err != nil {
		cleanup()
		t.Fatalf("failed to create notifications table: %v", err)
	}
	return repo, cleanup
}

func webExecRouter(repo *db.Repo, runner *webexec.HealthRunner) chi.Router {
	h := NewDashboardHandler(repo)
	h.WebExec = runner
	r := chi.NewRouter()
	RegisterRoutes(r, h)
	return r
}

// decodeProviderStatuses reads the GET /api/webexec payload.
func decodeProviderStatuses(t *testing.T, body []byte) []webexec.ProviderStatus {
	t.Helper()
	var parsed struct {
		Providers []webexec.ProviderStatus `json:"providers"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal providers: %v", err)
	}
	return parsed.Providers
}

// decodeProviderStatus reads the single-provider payload the toggle writes.
func decodeProviderStatus(t *testing.T, body []byte) webexec.ProviderStatus {
	t.Helper()
	var st webexec.ProviderStatus
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("unmarshal provider status: %v", err)
	}
	return st
}

func statusFor(t *testing.T, body []byte, provider string) webexec.ProviderStatus {
	t.Helper()
	for _, st := range decodeProviderStatuses(t, body) {
		if st.Provider == provider {
			return st
		}
	}
	t.Fatalf("provider %q missing from status list", provider)
	return webexec.ProviderStatus{}
}

func TestHandleGetWebExecDefaults(t *testing.T) {
	repo, cleanup := setupWebExecTestDB(t)
	defer cleanup()
	router := webExecRouter(repo, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/webexec", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	statuses := decodeProviderStatuses(t, rec.Body.Bytes())
	if len(statuses) != len(webexec.Providers) {
		t.Fatalf("got %d providers, want %d", len(statuses), len(webexec.Providers))
	}
	for _, st := range statuses {
		if st.Enabled {
			t.Errorf("%s Enabled = true, want the default off", st.Provider)
		}
		if st.SessionValid {
			t.Errorf("%s SessionValid = true, want false before any check", st.Provider)
		}
		if st.LastUsed != "" {
			t.Errorf("%s LastUsed = %q, want empty", st.Provider, st.LastUsed)
		}
		if st.RateLimitRemaining != webexec.DefaultMaxReqPerMin {
			t.Errorf("%s RateLimitRemaining = %d, want %d",
				st.Provider, st.RateLimitRemaining, webexec.DefaultMaxReqPerMin)
		}
	}
}

func TestHandleToggleWebExec(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		enabled  bool
		wantCode int
	}{
		{name: "enables deepseek-web", provider: webexec.ProviderDeepSeek, enabled: true, wantCode: http.StatusOK},
		{name: "disables gemini-web", provider: webexec.ProviderGemini, enabled: false, wantCode: http.StatusOK},
		{name: "enables chatgpt-web", provider: webexec.ProviderChatGPT, enabled: true, wantCode: http.StatusOK},
		{name: "enables muse-web", provider: webexec.ProviderMuse, enabled: true, wantCode: http.StatusOK},
		{name: "unknown provider is refused", provider: "claude", enabled: true, wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, cleanup := setupWebExecTestDB(t)
			defer cleanup()
			router := webExecRouter(repo, nil)

			body, err := json.Marshal(map[string]bool{"enabled": tt.enabled})
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/webexec/"+tt.provider+"/toggle", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("toggle status = %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			st := decodeProviderStatus(t, rec.Body.Bytes())
			if st.Provider != tt.provider {
				t.Fatalf("response provider = %q, want %q", st.Provider, tt.provider)
			}
			if st.Enabled != tt.enabled {
				t.Fatalf("response Enabled = %v, want %v", st.Enabled, tt.enabled)
			}
			// The switch is the settings row, so a reboot reads it back.
			settings, err := repo.GetSettings()
			if err != nil {
				t.Fatalf("GetSettings: %v", err)
			}
			if got := webexec.ProviderEnabled(settings, tt.provider); got != tt.enabled {
				t.Fatalf("settings switch = %v, want %v", got, tt.enabled)
			}

			// GET reports the same switch.
			rec = httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/webexec", nil))
			if got := statusFor(t, rec.Body.Bytes(), tt.provider).Enabled; got != tt.enabled {
				t.Fatalf("GET Enabled = %v, want %v", got, tt.enabled)
			}
		})
	}
}

func TestHandleToggleWebExecInvalidBody(t *testing.T) {
	repo, cleanup := setupWebExecTestDB(t)
	defer cleanup()
	router := webExecRouter(repo, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/webexec/deepseek-web/toggle", bytes.NewReader([]byte("not json")))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d, want 400", rec.Code)
	}
}

func TestHandleTestWebExecWithoutRunner(t *testing.T) {
	repo, cleanup := setupWebExecTestDB(t)
	defer cleanup()
	router := webExecRouter(repo, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/webexec/deepseek-web/test", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the health job is not running", rec.Code)
	}
}

func TestHandleTestWebExec(t *testing.T) {
	t.Run("expired session fails and notifies", func(t *testing.T) {
		repo, cleanup := setupWebExecTestDB(t)
		defer cleanup()
		// One stored account, whose validation fails: the injected validator
		// stands in for the per-provider probe the executors register.
		store := sessionvault.New(repo)
		if err := store.Store(t.Context(), webexec.ProviderDeepSeek, "acc-1", "cookie=expired"); err != nil {
			t.Fatalf("Store: %v", err)
		}
		runner := webexec.NewHealthRunner(repo, func(_ context.Context, provider, accountID string) error {
			return errValidationFailed
		})
		router := webExecRouter(repo, runner)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/webexec/deepseek-web/test", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the endpoint worked, the session did not): %s",
				rec.Code, rec.Body.String())
		}
		var result webexec.CheckResult
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if result.OK || result.Accounts != 1 || len(result.Failures) != 1 {
			t.Fatalf("result = %+v, want ok=false with one failure", result)
		}
		// The deduped warning exists for the expired account.
		var dedupeKey string
		err := repo.RawDB().QueryRow(
			`SELECT dedupeKey FROM notifications WHERE dedupeKey = ?`,
			webexec.SessionExpiredDedupeKey(webexec.ProviderDeepSeek, "acc-1"),
		).Scan(&dedupeKey)
		if err != nil {
			t.Fatalf("expected a session-expired notification: %v", err)
		}
		// The pool cooled the provider down and the status reflects it.
		if !runner.Pool.CooldownActive(webexec.ProviderDeepSeek) {
			t.Fatalf("provider must be cooling down after the failed check")
		}
	})

	t.Run("valid session passes and stamps lastValidated", func(t *testing.T) {
		repo, cleanup := setupWebExecTestDB(t)
		defer cleanup()
		store := sessionvault.New(repo)
		if err := store.Store(t.Context(), webexec.ProviderDeepSeek, "acc-1", "cookie=ok"); err != nil {
			t.Fatalf("Store: %v", err)
		}
		runner := webexec.NewHealthRunner(repo, nil) // presence check passes
		router := webExecRouter(repo, runner)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/webexec/deepseek-web/test", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var result webexec.CheckResult
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if !result.OK || len(result.Failures) != 0 {
			t.Fatalf("result = %+v, want ok=true with no failures", result)
		}
		entries, err := store.ListByProvider(t.Context(), webexec.ProviderDeepSeek)
		if err != nil || len(entries) != 1 {
			t.Fatalf("ListByProvider = %d entries (err %v), want 1", len(entries), err)
		}
		if entries[0].LastValidated == "" {
			t.Fatalf("lastValidated must be stamped after a passing check")
		}

		// The status endpoint reads the same pool: the session reads valid.
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/webexec", nil))
		st := statusFor(t, rec.Body.Bytes(), webexec.ProviderDeepSeek)
		if !st.SessionValid {
			t.Fatalf("SessionValid = false, want true after the passing check")
		}
	})
}
