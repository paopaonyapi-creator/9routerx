// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// telegram_test.go covers the push integration without touching the network:
// token shape, Markdown-v1 escaping, the subscription filter, and Dispatch
// against an httptest server (success, non-2xx, invalid-token short circuit,
// SSRF refusal). The real api.telegram.org host is never contacted.

// testToken matches ^\d+:[A-Za-z0-9_-]{35,}$.
var testToken = "1234567890:" + strings.Repeat("A", 35)

// countingServer is a Bot API stand-in that counts the requests it receives.
func countingServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// pointAtTestServer redirects Dispatch at the httptest origin and disables
// the SSRF guard for its loopback address, restoring both afterwards. Tests
// that need the REAL guard leave it alone.
func pointAtTestServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	origBase, origGuard := telegramAPIBase, assertPublicURL
	telegramAPIBase = srv.URL
	assertPublicURL = func(string) error { return nil }
	t.Cleanup(func() { telegramAPIBase, assertPublicURL = origBase, origGuard })
}

func TestValidateBotToken(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{testToken, true},
		{"1234567890:" + strings.Repeat("aZ0-9_-", 6), true}, // 42 chars, full alphabet
		{"1234567890:" + strings.Repeat("A", 34), false},     // one short of 35
		{"", false},
		{"nope", false},
		{"abc:" + strings.Repeat("A", 35), false}, // bot id must be numeric
		{"1234567890", false},                     // no colon segment
		{"1234567890:" + strings.Repeat("A", 35) + " extra", false},
		{"1234567890:" + strings.Repeat("A", 34) + "!", false}, // bad char, 35 after colon
		{"1234567890: " + strings.Repeat("A", 35), false},      // space breaks the token
	}
	for _, tc := range cases {
		if got := ValidateBotToken(tc.token); got != tc.want {
			t.Errorf("ValidateBotToken(%q) = %v, want %v", tc.token, got, tc.want)
		}
	}
}

func TestEscapeMarkdownV1(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"", ""},
		{"a_b", "a\\_b"},
		{"*bold*", "\\*bold\\*"},
		{"[link](x)", "\\[link\\](x)"},
		{"`code`", "\\`code\\`"},
		{"_*[]`", "\\_\\*\\[\\]\\`"},
		{"back\\slash", "back\\slash"}, // the backslash itself is not escaped
	}
	for _, tc := range cases {
		if got := EscapeMarkdownV1(tc.in); got != tc.want {
			t.Errorf("EscapeMarkdownV1(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	got := buildMessage(Event{
		Type:  EventQuotaExceeded,
		Emoji: "🚨",
		Title: "Quota Exceeded",
		Lines: []string{"Team *Acme* hit its budget", "spent: 12.5 USD"},
	})
	// Only the five Markdown-v1 markup characters are escaped; a bare period
	// passes through untouched.
	want := "🚨 *Quota Exceeded*\nTeam \\*Acme\\* hit its budget\nspent: 12.5 USD"
	if got != want {
		t.Errorf("buildMessage = %q, want %q", got, want)
	}
	// A bare event renders as just the bold label.
	if got := buildMessage(Event{Title: "Ping"}); got != "*Ping*" {
		t.Errorf("bare buildMessage = %q, want *Ping*", got)
	}
}

func TestConfigSubscribedAndEnabled(t *testing.T) {
	all := Config{BotToken: testToken, ChatID: "42", SubscribedEvents: []string{"*"}}
	if !all.subscribed(EventQuotaExceeded) || !all.subscribed(EventTestPing) {
		t.Error("wildcard subscription missed an event")
	}
	picky := Config{SubscribedEvents: []string{EventRequestFailed}}
	if !picky.subscribed(EventRequestFailed) {
		t.Error("exact subscription missed its own event")
	}
	if picky.subscribed(EventQuotaExceeded) || picky.subscribed("") {
		t.Error("unsubscribed event passed the filter")
	}
	none := Config{SubscribedEvents: nil}
	if none.subscribed(EventTestPing) {
		t.Error("empty subscription list passed an event")
	}
	if !all.Enabled() {
		t.Error("token+chat config not Enabled")
	}
	if (Config{BotToken: testToken}).Enabled() || (Config{ChatID: "42"}).Enabled() {
		t.Error("half-finished config Enabled")
	}
}

func TestDispatchSuccess(t *testing.T) {
	var gotPath string
	var gotBody sendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	pointAtTestServer(t, srv)

	cfg := Config{BotToken: testToken, ChatID: "42", SubscribedEvents: []string{"*"}}
	event := Event{Type: EventTestPing, Emoji: "📡", Title: "Ping", Lines: []string{"ok"}}
	if err := Dispatch(context.Background(), srv.Client(), cfg, event); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if want := "/bot" + testToken + "/sendMessage"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotBody.ChatID != "42" || gotBody.ParseMode != "Markdown" {
		t.Errorf("payload = %+v, want chat 42 with Markdown parse mode", gotBody)
	}
	if !strings.Contains(gotBody.Text, "📡 *Ping*") {
		t.Errorf("text = %q, want the emoji and bold title", gotBody.Text)
	}
}

func TestDispatchNon2xx(t *testing.T) {
	srv, hits := countingServer(t, http.StatusBadRequest,
		`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	pointAtTestServer(t, srv)

	cfg := Config{BotToken: testToken, ChatID: "42", SubscribedEvents: []string{"*"}}
	err := Dispatch(context.Background(), srv.Client(), cfg,
		Event{Type: EventRequestFailed, Title: "Failure"})
	if err == nil {
		t.Fatal("Dispatch on 400 = nil, want an error")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("error = %v, want the status and the Bot API description", err)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}
}

func TestDispatchUnsubscribedShortCircuits(t *testing.T) {
	srv, hits := countingServer(t, http.StatusOK, `{"ok":true}`)
	pointAtTestServer(t, srv)

	cfg := Config{BotToken: testToken, ChatID: "42", SubscribedEvents: []string{EventQuotaExceeded}}
	if err := Dispatch(context.Background(), srv.Client(), cfg,
		Event{Type: EventKeyExpired, Title: "Expired"}); err != nil {
		t.Fatalf("Dispatch on unsubscribed event = %v, want nil", err)
	}
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 (filtered before any network call)", hits.Load())
	}
}

func TestDispatchInvalidTokenShortCircuits(t *testing.T) {
	srv, hits := countingServer(t, http.StatusOK, `{"ok":true}`)
	pointAtTestServer(t, srv)

	cfg := Config{BotToken: "not-a-token", ChatID: "42", SubscribedEvents: []string{"*"}}
	err := Dispatch(context.Background(), srv.Client(), cfg, Event{Type: EventTestPing, Title: "Ping"})
	if err == nil || !strings.Contains(err.Error(), "invalid bot token") {
		t.Fatalf("error = %v, want invalid bot token", err)
	}
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 (token rejected before any network call)", hits.Load())
	}
}

// TestDispatchSSRFBlocked proves the send URL must pass the public-host
// guard with the REAL handlerutil.AssertPublicURL in place: a loopback
// origin is refused without a request being attempted.
func TestDispatchSSRFBlocked(t *testing.T) {
	srv, hits := countingServer(t, http.StatusOK, `{"ok":true}`)
	// telegramAPIBase points at the loopback server; the guard is NOT swapped.
	origBase := telegramAPIBase
	telegramAPIBase = srv.URL
	t.Cleanup(func() { telegramAPIBase = origBase })

	cfg := Config{BotToken: testToken, ChatID: "42", SubscribedEvents: []string{"*"}}
	err := Dispatch(context.Background(), srv.Client(), cfg, Event{Type: EventTestPing, Title: "Ping"})
	if err == nil || !strings.Contains(err.Error(), "blocked URL") {
		t.Fatalf("error = %v, want the SSRF block", err)
	}
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 (blocked before any network call)", hits.Load())
	}
}

// TestDispatchAsyncDelivers proves the async path reaches the Bot API with
// the same payload Dispatch would send. That the call itself never blocks is
// by construction — DispatchAsync spawns a goroutine and returns — and is
// not asserted here, because an observation window would race with the
// goroutine's legitimate right to finish instantly.
func TestDispatchAsyncDelivers(t *testing.T) {
	delivered := make(chan sendMessageRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body sendMessageRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		delivered <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	pointAtTestServer(t, srv)

	cfg := Config{BotToken: testToken, ChatID: "42", SubscribedEvents: []string{"*"}}
	DispatchAsync(cfg, Event{Type: EventQuotaExceeded, Title: "Quota", Lines: []string{"over"}})

	select {
	case got := <-delivered:
		if got.ChatID != "42" || got.ParseMode != "Markdown" || !strings.Contains(got.Text, "*Quota*") {
			t.Errorf("async payload = %+v, want the chat 42 Markdown message", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async dispatch never delivered")
	}
}

// TestDispatchAsyncSkipsDisabledConfig proves an unconfigured install never
// spawns the goroutine at all.
func TestDispatchAsyncSkipsDisabledConfig(t *testing.T) {
	srv, hits := countingServer(t, http.StatusOK, `{"ok":true}`)
	pointAtTestServer(t, srv)

	DispatchAsync(Config{BotToken: testToken, SubscribedEvents: []string{"*"}},
		Event{Type: EventTestPing, Title: "Ping"}) // no ChatID
	DispatchAsync(Config{ChatID: "42", SubscribedEvents: []string{"*"}},
		Event{Type: EventTestPing, Title: "Ping"}) // no BotToken
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 for a disabled config", hits.Load())
	}
}

// TestDispatchTimeout proves the default client bounds the call: a server
// that never answers surfaces the timeout as an error, not a hang. The
// release channel is closed by a defer registered AFTER srv.Close's, so the
// stalled handler is freed before Close waits on it — the other order
// deadlocks Close against its own handler.
func TestDispatchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)
	pointAtTestServer(t, srv)

	cfg := Config{BotToken: testToken, ChatID: "42", SubscribedEvents: []string{"*"}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := Dispatch(ctx, nil, cfg, Event{Type: EventTestPing, Title: "Ping"})
	if err == nil {
		t.Fatal("Dispatch against a stalled server = nil, want the deadline error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", http.StatusRequestTimeout)) &&
		!strings.Contains(err.Error(), "context deadline exceeded") &&
		!strings.Contains(err.Error(), "Client.Timeout") {
		t.Errorf("error = %v, want a deadline/timeout failure", err)
	}
}
