// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/models"
)

// keypolicy_test.go covers the serenhope per-key budget gates
// (port(serenhope) TASK-1.2): the USD-per-UTC-day cost ceiling, its
// Retry-After-to-midnight contract, its fail-open read behavior and the lazy
// reset across a day boundary — all judged by an injected clock so no test
// sleeps or depends on the host's timezone.

// fakeCostReader answers ApiKeyCostToday from a map, or with a fixed error.
type fakeCostReader struct {
	spend map[string]float64
	err   error
}

func (f *fakeCostReader) ApiKeyCostToday(keyID, day string) (float64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.spend[keyID+"|"+day], nil
}

// fixedClock returns a KeyPolicy whose now() answers one instant.
func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func runKeyPolicy(t *testing.T, p *KeyPolicy, apiKey *models.APIKey) *httptest.ResponseRecorder {
	t.Helper()
	handler := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if apiKey != nil {
		req = req.WithContext(withAPIKey(req, apiKey))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// runKeyPolicyReq is the request-shaped variant, for cases that need to set
// the socket address or forwarded headers directly.
func runKeyPolicyReq(t *testing.T, p *KeyPolicy, req *http.Request, apiKey *models.APIKey) *httptest.ResponseRecorder {
	t.Helper()
	handler := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if apiKey != nil {
		req = req.WithContext(withAPIKey(req, apiKey))
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestIPWhitelistGate covers the per-key client-address allowlist
// (port(serenhope) TASK-1.3): exact and CIDR entries, the empty-allow-all
// default, the refusal envelope, and the trust-proxy boundary — a spoofed
// X-Forwarded-For is ignored while the immediate socket address decides,
// unless the operator opted into trusted proxies.
func TestIPWhitelistGate(t *testing.T) {
	const keyID = "k-ip"
	mkKey := func(list string) *models.APIKey { return &models.APIKey{ID: keyID, IPWhitelist: list} }

	t.Run("empty_whitelist_allows_every_address", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		p.now = fixedClock(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
		if rec := runKeyPolicy(t, p, mkKey("")); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 with no whitelist", rec.Code)
		}
	})

	t.Run("exact_and_cidr_entries_match", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		p.now = fixedClock(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
		for _, tt := range []struct {
			list string
			addr string
		}{
			{`["203.0.113.7"]`, "203.0.113.7"},
			{`["10.0.0.0/8"]`, "10.9.9.9"},
			{`203.0.113.7, 10.0.0.0/8`, "10.9.9.9"}, // the comma shape a restore carries
			{`["::1", "10.0.0.0/8"]`, "10.9.9.9"},
		} {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.RemoteAddr = tt.addr + ":5555"
			if rec := runKeyPolicyReq(t, p, req, mkKey(tt.list)); rec.Code != http.StatusOK {
				t.Errorf("whitelist %q addr %s: status = %d, want 200", tt.list, tt.addr, rec.Code)
			}
		}
	})

	t.Run("mismatch_is_refused_403_ip_not_allowed", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		p.now = fixedClock(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.RemoteAddr = "198.51.100.9:4321"
		rec := runKeyPolicyReq(t, p, req, mkKey(`["203.0.113.7","10.0.0.0/8"]`))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var body struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Error.Code != "ip_not_allowed" {
			t.Errorf("code = %q, want ip_not_allowed", body.Error.Code)
		}
		if body.Error.Type != "permission_error" {
			t.Errorf("type = %q, want permission_error", body.Error.Type)
		}
		if body.Error.Message != "IP 198.51.100.9 is not whitelisted for this key" {
			t.Errorf("message = %q, want the address named and nothing else", body.Error.Message)
		}
	})

	t.Run("spoofed_xff_is_ignored_without_trust_proxy", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		p.now = fixedClock(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.RemoteAddr = "203.0.113.7:5000"
		req.Header.Set("X-Forwarded-For", "10.0.0.99")
		// The whitelist names only the spoofed value: the socket address must
		// decide, and it is not in the list.
		if rec := runKeyPolicyReq(t, p, req, mkKey(`["10.0.0.99"]`)); rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (XFF must not win without trust)", rec.Code)
		}
		// ...and the real socket address is in the list, so it passes.
		if rec := runKeyPolicyReq(t, p, req, mkKey(`["203.0.113.7"]`)); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for the real socket address", rec.Code)
		}
	})

	t.Run("xff_is_honored_under_trust_proxy", func(t *testing.T) {
		t.Setenv("TRUST_PROXY", "true")
		p := NewKeyPolicy(nil)
		p.now = fixedClock(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.RemoteAddr = "127.0.0.1:6000" // a hop inside DefaultTrustedProxies
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		if rec := runKeyPolicyReq(t, p, req, mkKey(`["203.0.113.7"]`)); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (trusted proxy hands over the client address)", rec.Code)
		}
		// A chain of trusted intermediates: the walk skips every trusted hop
		// and lands on the first untrusted address — the client.
		req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.99")
		if rec := runKeyPolicyReq(t, p, req, mkKey(`["203.0.113.7"]`)); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (multi-hop walk lands on the client)", rec.Code)
		}
		// The address the walk lands on is the one judged: a different client
		// behind the same trusted proxy is refused by name.
		req.Header.Set("X-Forwarded-For", "198.51.100.5, 10.0.0.99")
		rec := runKeyPolicyReq(t, p, req, mkKey(`["203.0.113.7"]`))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "IP 198.51.100.5 is not whitelisted") {
			t.Errorf("body = %s, want the walked client address named", rec.Body.String())
		}
	})
}

func TestDailyCostLimitGate(t *testing.T) {
	day := time.Date(2026, 11, 5, 13, 0, 0, 0, time.UTC)
	key := &models.APIKey{ID: "k1", DailyCostLimit: 10.0}

	t.Run("under_limit_is_served", func(t *testing.T) {
		p := NewKeyPolicy(&fakeCostReader{spend: map[string]float64{"k1|2026-11-05": 9.99}})
		p.now = fixedClock(day)
		if rec := runKeyPolicy(t, p, key); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 under the ceiling", rec.Code)
		}
	})

	t.Run("at_limit_is_refused_429_until_midnight", func(t *testing.T) {
		p := NewKeyPolicy(&fakeCostReader{spend: map[string]float64{"k1|2026-11-05": 10.0}})
		p.now = fixedClock(day)
		rec := runKeyPolicy(t, p, key)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 at the ceiling", rec.Code)
		}
		if got, want := rec.Header().Get("Retry-After"), "39600"; got != want {
			// 13:00 UTC is 11 hours (39600 s) from the 00:00 UTC rollover.
			t.Errorf("Retry-After = %q, want %q", got, want)
		}
		if got := rec.Header().Get("X-Cost-Limit"); got != "exceeded" {
			t.Errorf("X-Cost-Limit = %q, want exceeded", got)
		}
		if got := rec.Header().Get("X-Cost-Limit-Limit"); got != "10" {
			t.Errorf("X-Cost-Limit-Limit = %q, want 10", got)
		}
		if got := rec.Header().Get("X-Cost-Limit-Spent"); got != "10" {
			t.Errorf("X-Cost-Limit-Spent = %q, want 10", got)
		}
		var body struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Error.Message != "API key daily cost limit exceeded" {
			t.Errorf("message = %q", body.Error.Message)
		}
		if body.Error.Type != "rate_limit_error" || body.Error.Code != "rate_limit_exceeded" {
			t.Errorf("type/code = %s/%s, want rate_limit_error/rate_limit_exceeded", body.Error.Type, body.Error.Code)
		}
	})

	t.Run("zero_limit_is_unlimited_despite_spend", func(t *testing.T) {
		unlimited := &models.APIKey{ID: "k2", DailyCostLimit: 0}
		p := NewKeyPolicy(&fakeCostReader{spend: map[string]float64{"k2|2026-11-05": 999}})
		p.now = fixedClock(day)
		if rec := runKeyPolicy(t, p, unlimited); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 with no ceiling configured", rec.Code)
		}
	})

	t.Run("other_day_spend_does_not_count", func(t *testing.T) {
		// Yesterday's spend must not block today: the lazy reset reads as zero
		// until something is charged to today's stamp.
		p := NewKeyPolicy(&fakeCostReader{spend: map[string]float64{"k1|2026-11-04": 999}})
		p.now = fixedClock(day)
		if rec := runKeyPolicy(t, p, key); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 with only a stale-day stamp", rec.Code)
		}
	})

	t.Run("read_error_fails_open", func(t *testing.T) {
		p := NewKeyPolicy(&fakeCostReader{err: errors.New("db hiccup")})
		p.now = fixedClock(day)
		if rec := runKeyPolicy(t, p, key); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 on a reader failure (fail-open)", rec.Code)
		}
	})

	t.Run("unauthenticated_request_is_not_gated", func(t *testing.T) {
		p := NewKeyPolicy(&fakeCostReader{spend: map[string]float64{"k1|2026-11-05": 999}})
		p.now = fixedClock(day)
		if rec := runKeyPolicy(t, p, nil); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 without an authenticated key", rec.Code)
		}
	})
}

// TestDailyCostLimitEndToEnd drives the gate against a real SQLite repo: cost
// is charged to the key row the way the usage writer does, the next request is
// refused, and advancing the injected clock past the UTC boundary re-arms the
// key with nothing carried over.
func TestDailyCostLimitEndToEnd(t *testing.T) {
	conn, err := db.OpenDatabase(filepath.Join(t.TempDir(), "data.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	defer conn.Close()
	if err := db.EnsureCoreSchema(conn); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	repo := db.NewRepo(conn)

	if err := repo.CreateApiKey("ke2e", "sk-e2e-cost", "cost e2e", ""); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := repo.SetApiKeyDailyCostLimit("ke2e", 1.0); err != nil {
		t.Fatalf("SetApiKeyDailyCostLimit: %v", err)
	}

	day := time.Date(2026, 11, 5, 22, 30, 0, 0, time.UTC)
	if err := repo.BumpApiKeyCostSpent("ke2e", 1.0, utcDayKey(day)); err != nil {
		t.Fatalf("BumpApiKeyCostSpent: %v", err)
	}

	p := NewKeyPolicy(repo)
	p.now = fixedClock(day)
	rec := runKeyPolicy(t, p, &models.APIKey{ID: "ke2e", DailyCostLimit: 1.0})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 with the daily ceiling spent", rec.Code)
	}

	// Past midnight UTC the accumulator reads as zero: the key is served
	// again, and the request itself re-charges the new day's stamp.
	nextDay := day.Add(2 * time.Hour)
	p.now = fixedClock(nextDay)
	rec = runKeyPolicy(t, p, &models.APIKey{ID: "ke2e", DailyCostLimit: 1.0})
	if rec.Code != http.StatusOK {
		t.Fatalf("status after rollover = %d, want 200 (lazy daily reset)", rec.Code)
	}
}
