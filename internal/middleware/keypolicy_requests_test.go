// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"9routerx/proxy/internal/models"
)

// keypolicy_requests_test.go covers the per-interval request cap
// (port(serenhope) TASK-1.4): admission counting inside the window, the 429
// with Retry-After to the boundary, the window roll, seeding from the
// persisted counters, the lifetime variant, and atomicity under parallel
// writers (run with -race).

func TestRequestLimitCap(t *testing.T) {
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	key := func(limit int64, intervalMin int) *models.APIKey {
		return &models.APIKey{ID: "k-req", RequestLimit: limit, RequestResetInterval: intervalMin}
	}

	t.Run("no_limit_is_never_refused", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		p.now = fixedClock(at)
		if rec := runKeyPolicy(t, p, key(0, 0)); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 with no cap", rec.Code)
		}
	})

	t.Run("inside_window_is_counted_then_refused", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		now := at
		p.now = func() time.Time { return now }
		k := key(3, 60)
		for i := 0; i < 3; i++ {
			if rec := runKeyPolicy(t, p, k); rec.Code != http.StatusOK {
				t.Fatalf("admission %d: status = %d, want 200", i+1, rec.Code)
			}
		}
		now = now.Add(time.Minute)
		rec := runKeyPolicy(t, p, k)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 at the cap", rec.Code)
		}
		var body struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Error.Message != "API key request limit exceeded" {
			t.Errorf("message = %q, want upstream's exact sentence", body.Error.Message)
		}
		// Retry-After names the boundary: the window opened at 10:00 with a
		// 60-minute interval, so a refusal at 10:01 waits 59 minutes.
		if got, want := rec.Header().Get("Retry-After"), strconv.Itoa(59*60); got != want {
			t.Errorf("Retry-After = %q, want %q", got, want)
		}
		if got := rec.Header().Get("X-Request-Limit-Remaining"); got != "0" {
			t.Errorf("X-Request-Limit-Remaining = %q, want 0", got)
		}
	})

	t.Run("window_rolls_by_whole_periods", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		now := at
		p.now = func() time.Time { return now }
		k := key(1, 60)
		if rec := runKeyPolicy(t, p, k); rec.Code != http.StatusOK {
			t.Fatalf("first admission: status = %d, want 200", rec.Code)
		}
		// 61 minutes later the window has rolled: served again, and the next
		// boundary aligns to the second window's opening.
		now = now.Add(61 * time.Minute)
		if rec := runKeyPolicy(t, p, k); rec.Code != http.StatusOK {
			t.Fatalf("after roll: status = %d, want 200", rec.Code)
		}
		rec := runKeyPolicy(t, p, k)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 (one per window)", rec.Code)
		}
		if got, want := rec.Header().Get("Retry-After"), "3540"; got != want {
			// The refusal lands at 11:01 and the rolled window's boundary is
			// 12:00 — 3540 s of wait, aligned to when traffic started.
			t.Errorf("Retry-After = %q, want %q (boundary aligned to the rolled window)", got, want)
		}
	})

	t.Run("lifetime_cap_never_rolls_and_carries_no_retry_after", func(t *testing.T) {
		p := NewKeyPolicy(nil)
		now := at
		p.now = func() time.Time { return now }
		k := key(1, 0)
		if rec := runKeyPolicy(t, p, k); rec.Code != http.StatusOK {
			t.Fatalf("first admission: status = %d, want 200", rec.Code)
		}
		now = now.Add(365 * 24 * time.Hour)
		rec := runKeyPolicy(t, p, k)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 (lifetime cap)", rec.Code)
		}
		if got := rec.Header().Get("Retry-After"); got != "" {
			t.Errorf("Retry-After = %q, want none (waiting never helps)", got)
		}
	})

	t.Run("restart_seeds_from_persisted_counter", func(t *testing.T) {
		// A fresh process over a key that already spent its window: the first
		// request is refused, because seeding reads the persisted row.
		p := NewKeyPolicy(nil)
		p.now = fixedClock(at)
		k := &models.APIKey{
			ID: "k-req", RequestLimit: 3, RequestResetInterval: 60,
			UsedRequests: 3, RequestResetAt: at.Add(-time.Minute).Format(time.RFC3339),
		}
		if rec := runKeyPolicy(t, p, k); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 (persisted spend counts after restart)", rec.Code)
		}
		// ...and the same seed with an expired stamp opens a fresh window.
		fresh := &models.APIKey{
			ID: "k-req2", RequestLimit: 3, RequestResetInterval: 60,
			UsedRequests: 3, RequestResetAt: at.Add(-2 * time.Hour).Format(time.RFC3339),
		}
		if rec := runKeyPolicy(t, p, fresh); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (expired stamp opens a fresh window)", rec.Code)
		}
	})
}

// TestRequestLimitCapConcurrent proves admissions are counted atomically:
// N parallel requests against a cap of exactly N let every one through, and
// one more is refused — no lost updates, no overshoot. Run with -race.
func TestRequestLimitCapConcurrent(t *testing.T) {
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := NewKeyPolicy(nil)
	p.now = fixedClock(at)
	k := &models.APIKey{ID: "k-race", RequestLimit: 32, RequestResetInterval: 60}

	handler := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	admitted := make([]bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req = req.WithContext(withAPIKey(req, k))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			admitted[i] = rec.Code == http.StatusOK
		}(i)
	}
	wg.Wait()
	got := 0
	for _, ok := range admitted {
		if ok {
			got++
		}
	}
	if got != 32 {
		t.Errorf("admitted = %d, want exactly 32 (lost updates or overshoot)", got)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(withAPIKey(req, k))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status after the cap = %d, want 429", rec.Code)
	}
}
