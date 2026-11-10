// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/resilience"
)

func TestStrategyBreakerState_ResolvesAliases(t *testing.T) {
	h, _ := newResilienceTestHandler(t)
	h.Resilience = resilience.NewController(resilience.BreakerOptions{
		FailureThreshold: 2,
		ResetTimeout:     time.Minute,
	}, resilience.SemaphoreOptions{})

	// Trip antigravity breaker by recording failures against canonical name
	b := resilience.BreakerFor("antigravity", resilience.BreakerOptions{FailureThreshold: 2, ResetTimeout: time.Minute})
	b.RecordFailure()
	b.RecordFailure()

	// Looking up with alias "ag/gemini-2.5-flash" must resolve alias and report OPEN
	st := h.strategyBreakerState("ag/gemini-2.5-flash")
	if st != "OPEN" {
		t.Errorf("strategyBreakerState(ag/gemini-2.5-flash) = %q, want OPEN", st)
	}

	// Un-tripped provider must report CLOSED
	stOpenAI := h.strategyBreakerState("openai/gpt-4o")
	if stOpenAI != "CLOSED" {
		t.Errorf("strategyBreakerState(openai/gpt-4o) = %q, want CLOSED", stOpenAI)
	}
}

func TestBreakerOpen_Detection(t *testing.T) {
	h, provider := newResilienceTestHandler(t)
	h.Resilience = resilience.NewController(resilience.BreakerOptions{
		FailureThreshold: 2,
		ResetTimeout:     time.Minute,
	}, resilience.SemaphoreOptions{})

	// Closed initially
	if h.breakerOpen(provider) {
		t.Fatal("breakerOpen returned true for healthy provider")
	}

	// Trip breaker
	b := resilience.BreakerFor(provider, resilience.BreakerOptions{FailureThreshold: 2, ResetTimeout: time.Minute})
	b.RecordFailure()
	b.RecordFailure()

	if !h.breakerOpen(provider) {
		t.Fatal("breakerOpen returned false for tripped breaker")
	}

	// Reset closes it
	b.Reset()
	if h.breakerOpen(provider) {
		t.Fatal("breakerOpen returned true after Reset")
	}
}

func TestComboSkipsOpenBreaker(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	resilience.ResetAllBreakers()
	defer resilience.ResetAllBreakers()

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	h.Resilience = resilience.NewController(resilience.BreakerOptions{
		FailureThreshold: 1,
		ResetTimeout:     time.Minute,
	}, resilience.SemaphoreOptions{})

	// Trip breaker for provider1
	b := resilience.BreakerFor("failing-prov", resilience.BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Minute})
	b.RecordFailure()

	ctx := context.Background()
	rec := NewRoutingRecorder()
	ctx = WithRoutingRecorder(ctx, rec)

	// Candidate model with open breaker should be skipped by breakerOpen
	if !h.breakerOpen("failing-prov") {
		t.Fatal("expected breakerOpen to be true for failing-prov")
	}
}

func TestComboFallback_SkipsOpenBreakerDirectly(t *testing.T) {
	var primaryHits atomic.Int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"primary answered"}}]}`))
	}))
	defer primaryServer.Close()

	var fallbackHits atomic.Int32
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"fallback answered"}}]}`))
	}))
	defer fallbackServer.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	resilience.ResetAllBreakers()
	defer resilience.ResetAllBreakers()

	dataPrimary, _ := json.Marshal(map[string]any{"apiKey": "sk-mock", "baseUrl": primaryServer.URL})
	database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES ('conn-p', 'openai', 'apikey', 'pri', 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(dataPrimary))

	dataFallback, _ := json.Marshal(map[string]any{"apiKey": "sk-mock", "baseUrl": fallbackServer.URL})
	database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES ('conn-f', 'deepseek', 'apikey', 'fb', 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(dataFallback))

	modelsCombo, _ := json.Marshal([]string{"openai/gpt-4o", "deepseek/deepseek-chat"})
	database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES ('c-breaker', 'breaker-combo', 'fallback', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(modelsCombo))

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	h.Resilience = resilience.NewController(resilience.BreakerOptions{
		FailureThreshold: 1,
		ResetTimeout:     time.Minute,
	}, resilience.SemaphoreOptions{})

	// Trip breaker for openai
	b := resilience.BreakerFor("openai", resilience.BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Minute})
	for range 10 {
		b.RecordFailure()
		if b.State() == resilience.StateOpen {
			break
		}
	}
	if b.State() != resilience.StateOpen {
		t.Fatalf("failed to trip breaker for openai: state=%s", b.State())
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"breaker-combo","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	recorder := NewRoutingRecorder()
	req = req.WithContext(WithRoutingRecorder(req.Context(), recorder))
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from fallback combo, got %d: %s", rec.Code, rec.Body.String())
	}

	// Primary server must NEVER be contacted because its breaker is OPEN
	if got := primaryHits.Load(); got != 0 {
		t.Errorf("primaryHits = %d, want 0 (open breaker must skip without dialing)", got)
	}
	if got := fallbackHits.Load(); got != 1 {
		t.Errorf("fallbackHits = %d, want 1", got)
	}

	// Routing recorder must record that openai/gpt-4o was skipped due to circuit breaker
	snap := recorder.Snapshot()
	foundSkip := false
	for _, ev := range snap.Evaluated {
		if ev.Model == "openai/gpt-4o" && ev.Status == "skipped" && strings.Contains(ev.Reason, "circuit breaker open") {
			foundSkip = true
			break
		}
	}
	if !foundSkip {
		t.Errorf("expected skip evaluation with circuit breaker reason in routing metadata, got: %+v", snap.Evaluated)
	}
}

func TestMessagesComboFallback_SkipsOpenBreakerDirectly(t *testing.T) {
	var primaryHits atomic.Int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"type":"message","role":"assistant","content":[{"type":"text","text":"primary"}]}`))
	}))
	defer primaryServer.Close()

	var fallbackHits atomic.Int32
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"type":"message","role":"assistant","content":[{"type":"text","text":"fallback"}]}`))
	}))
	defer fallbackServer.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	resilience.ResetAllBreakers()
	defer resilience.ResetAllBreakers()

	dataPrimary, _ := json.Marshal(map[string]any{"apiKey": "sk-mock", "baseUrl": primaryServer.URL})
	database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES ('conn-p-msg', 'anthropic', 'apikey', 'pri', 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(dataPrimary))

	dataFallback, _ := json.Marshal(map[string]any{"apiKey": "sk-mock", "baseUrl": fallbackServer.URL})
	database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES ('conn-f-msg', 'deepseek', 'apikey', 'fb', 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(dataFallback))

	modelsCombo, _ := json.Marshal([]string{"anthropic/claude-3-5-sonnet", "deepseek/deepseek-chat"})
	database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES ('c-breaker-msg', 'breaker-msg-combo', 'fallback', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(modelsCombo))

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	h.Resilience = resilience.NewController(resilience.BreakerOptions{
		FailureThreshold: 1,
		ResetTimeout:     time.Minute,
	}, resilience.SemaphoreOptions{})

	// Trip breaker for anthropic
	b := resilience.BreakerFor("anthropic", resilience.BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Minute})
	b.RecordFailure()

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"breaker-msg-combo","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`))
	recorder := NewRoutingRecorder()
	req = req.WithContext(WithRoutingRecorder(req.Context(), recorder))
	rec := httptest.NewRecorder()
	h.HandleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from fallback combo, got %d: %s", rec.Code, rec.Body.String())
	}

	if got := primaryHits.Load(); got != 0 {
		t.Errorf("primaryHits = %d, want 0", got)
	}
	if got := fallbackHits.Load(); got != 1 {
		t.Errorf("fallbackHits = %d, want 1", got)
	}
}
