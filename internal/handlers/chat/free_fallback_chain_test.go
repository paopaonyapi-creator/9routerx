// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/freeresilience"
	"9routerx/proxy/internal/providers"
)

// chatCompletionBody is a minimal 200 answer the second upstream serves.
const chatCompletionBody = `{"id":"chatcmpl-chain","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"served by the fallback"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

// TestFreeFallbackChain_Groq429ToNvidia is the B.1 contract end to end:
// provider A (groq, free) answers 429, the chain retries the same model on
// provider B (nvidia, free, catalogued with the same id) which answers 200.
// The client gets the successful completion, and the response names the chain
// it walked.
func TestFreeFallbackChain_Groq429ToNvidia(t *testing.T) {
	refusals := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached","type":"FreeTierError"}}`))
	}))
	defer refusals.Close()

	serves := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatCompletionBody))
	}))
	defer serves.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedConnDB(t, database, "groq", "conn-groq", "sk-groq", refusals.URL)
	seedConnDB(t, database, "nvidia", "conn-nvidia", "sk-nvidia", serves.URL)
	handler := NewChatHandler(repo)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"groq/openai/gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 via the fallback chain, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(HeaderFallbackChain); got != "groq→nvidia" {
		t.Errorf("fallback chain header = %q, want %q", got, "groq→nvidia")
	}
	if got := rec.Header().Get(HeaderFallbackPaid); got != "" {
		t.Errorf("paid header must be empty on a free chain, got %q", got)
	}
	if !strings.Contains(rec.Body.String(), "served by the fallback") {
		t.Errorf("body must be the fallback provider's answer, got: %s", rec.Body.String())
	}

	// The refusal opened a provider-level cooldown on groq — the row the
	// recovery scheduler later expires.
	var status string
	if err := database.QueryRow(`SELECT status FROM freeTierRecovery WHERE provider = 'groq'`).Scan(&status); err != nil {
		t.Fatalf("no recovery row for groq: %v", err)
	}
	if status != "cooling" {
		t.Errorf("groq recovery status = %s, want cooling", status)
	}
}

// TestFreeFallbackChain_PaidProviderNeverChained: a 429 on a PAID provider
// must reach the client verbatim — no chain, no cooldown, no reroute.
func TestFreeFallbackChain_PaidProviderNeverChained(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached"}}`))
	}))
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedConnDB(t, database, "openai", "conn-openai", "sk-openai", upstream.URL)
	// A healthy free provider exists and serves nothing for this model — it
	// must not be dialled.
	idle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("free provider must not be dialled for a paid provider's failure")
	}))
	defer idle.Close()
	seedConnDB(t, database, "nvidia", "conn-nvidia", "sk-nvidia", idle.URL)
	handler := NewChatHandler(repo)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"openai/gpt-5.5","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("paid provider failure must pass through verbatim, got %d", rec.Code)
	}
	if got := rec.Header().Get(HeaderFallbackChain); got != "" {
		t.Errorf("no chain header on a paid provider, got %q", got)
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d, want 1 (no retry)", hits)
	}
	var count int
	_ = database.QueryRow(`SELECT COUNT(1) FROM freeTierRecovery`).Scan(&count)
	if count != 0 {
		t.Errorf("freeTierRecovery rows = %d, want 0 for a paid provider", count)
	}
}

// TestFreeFallbackChain_BadRequestStaysVisible: a 400 is a request/config bug
// and must not disappear into a retry on another provider.
func TestFreeFallbackChain_BadRequestStaysVisible(t *testing.T) {
	var nvidiaHits int
	refusals := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"messages: request is malformed"}}`))
	}))
	defer refusals.Close()
	serves := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nvidiaHits++
		_, _ = w.Write([]byte(chatCompletionBody))
	}))
	defer serves.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedConnDB(t, database, "groq", "conn-groq", "sk-groq", refusals.URL)
	seedConnDB(t, database, "nvidia", "conn-nvidia", "sk-nvidia", serves.URL)
	handler := NewChatHandler(repo)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"groq/openai/gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("400 must pass through verbatim, got %d", rec.Code)
	}
	if got := rec.Header().Get(HeaderFallbackChain); got != "" {
		t.Errorf("no chain header on a 400, got %q", got)
	}
	if nvidiaHits != 0 {
		t.Errorf("nvidia hits = %d, want 0 (a 400 never falls back)", nvidiaHits)
	}
}

// TestFreeFallbackChain_PaidAfterFreeExhausted: every free candidate fails,
// the paid lane serves, and the response says so in both headers. The model
// is openai/gpt-oss-120b — catalogued free on groq and nvidia, listed by the
// paid siliconflow.
func TestFreeFallbackChain_PaidAfterFreeExhausted(t *testing.T) {
	refusals := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer refusals.Close()
	serves := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatCompletionBody))
	}))
	defer serves.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedConnDB(t, database, "groq", "conn-groq", "sk-groq", refusals.URL)
	seedConnDB(t, database, "nvidia", "conn-nvidia", "sk-nvidia", refusals.URL)
	seedConnDB(t, database, "siliconflow", "conn-siliconflow", "sk-siliconflow", serves.URL)
	handler := NewChatHandler(repo)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"groq/openai/gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected the paid fallback to serve, got %d: %s", rec.Code, rec.Body.String())
	}
	// runcrate also lists the id on the paid side and sorts before
	// siliconflow; with no connection configured it fails fast and the walk
	// continues. The paid header names the provider that actually served.
	if got := rec.Header().Get(HeaderFallbackChain); got != "groq→nvidia→runcrate→siliconflow" {
		t.Errorf("chain header = %q, want %q", got, "groq→nvidia→runcrate→siliconflow")
	}
	if got := rec.Header().Get(HeaderFallbackPaid); got != "siliconflow" {
		t.Errorf("paid header = %q, want siliconflow", got)
	}
}

// TestFreeFallbackCandidates_SkipCoolingAndRejoin covers the B.2 rotation
// contract at the candidate level: a cooling provider is skipped from the
// chain without being deleted, and rejoins when its cooldown lifts.
func TestFreeFallbackCandidates_SkipCoolingAndRejoin(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	handler := NewChatHandler(repo)
	tracker := handler.FreeResilience
	if tracker == nil {
		t.Fatal("handler must carry a tracker when built with a repo")
	}

	candidates := handler.freeFallbackCandidates("groq", "openai/gpt-oss-120b", map[string]bool{})
	if len(candidates) == 0 || candidates[0] != "nvidia" {
		t.Fatalf("candidates = %v, want [nvidia] (the only other free provider catalogued with the id)", candidates)
	}

	// nvidia hits its limit: it is skipped, not deleted.
	tracker.RecordOutcome("nvidia", "openai/gpt-oss-120b", 429, []byte(`rate limit`), 0, nil)
	if candidates = handler.freeFallbackCandidates("groq", "openai/gpt-oss-120b", map[string]bool{}); len(candidates) != 0 {
		t.Fatalf("cooling nvidia must be skipped, got %v", candidates)
	}

	// A success (early recovery) puts it straight back.
	tracker.RecordOutcome("nvidia", "openai/gpt-oss-120b", 200, nil, 50, nil)
	if candidates = handler.freeFallbackCandidates("groq", "openai/gpt-oss-120b", map[string]bool{}); len(candidates) != 1 || candidates[0] != "nvidia" {
		t.Fatalf("after recovery candidates = %v, want [nvidia]", candidates)
	}
}

// TestFreeFallbackChain_DisabledByConfig: FALLBACK_FREE_ENABLED=false turns
// the whole suite off.
func TestFreeFallbackChain_DisabledByConfig(t *testing.T) {
	refusals := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer refusals.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedConnDB(t, database, "groq", "conn-groq", "sk-groq", refusals.URL)
	handler := NewChatHandler(repo)
	// Simulate the disabled configuration without touching process env.
	orig := handler.FreeResilience
	t.Cleanup(func() { handler.FreeResilience = orig })
	handler.FreeResilience = freeresilience.NewTracker(
		freeresilience.FallbackConfig{Enabled: false, MaxAttempts: 3}, repo, nil)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"groq/openai/gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("with the chain disabled the refusal must pass through, got %d", rec.Code)
	}
	if got := rec.Header().Get(HeaderFallbackChain); got != "" {
		t.Errorf("no chain header when disabled, got %q", got)
	}
}

// TestIsFreeRoutableProvider pins the eligibility set: catalogued providers
// and registry-free sections in, paid providers out.
func TestIsFreeRoutableProvider(t *testing.T) {
	for _, pid := range []string{"groq", "nvidia", "qoder", "antigravity", "opencode", "gemini", "tokenharbor"} {
		if !isFreeRoutableProvider(pid) {
			t.Errorf("isFreeRoutableProvider(%q) = false, want true", pid)
		}
	}
	for _, pid := range []string{"openai", "anthropic", "azure", "bedrock"} {
		if isFreeRoutableProvider(pid) {
			t.Errorf("isFreeRoutableProvider(%q) = true, want false", pid)
		}
	}
	if isFreeRoutableProvider("") {
		t.Error("empty provider must not be free-routable")
	}
	// Aliases resolve.
	if !isFreeRoutableProvider("af") {
		t.Error("alias af (api-airforce) must be free-routable")
	}
	_ = providers.KnownProviders
}
