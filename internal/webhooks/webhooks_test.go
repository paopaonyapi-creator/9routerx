// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9routerx/proxy/internal/telegram"
)

// pointAtTestServer disables the SSRF guard for the loopback httptest origins
// the delivery tests send to, restoring the real guard afterwards. Tests that
// need the REAL guard (the SSRF block cases) leave it alone.
func pointAtTestServer(t *testing.T) {
	t.Helper()
	orig := assertPublicURL
	assertPublicURL = func(string) error { return nil }
	t.Cleanup(func() { assertPublicURL = orig })
}

// captureServer is a JSON stand-in that records the bodies it receives.
func captureServer(t *testing.T) (*httptest.Server, *atomic.Int32, chan map[string]any) {
	t.Helper()
	var hits atomic.Int32
	bodies := make(chan map[string]any, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies <- body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, bodies
}

func TestMatchesFilter(t *testing.T) {
	cases := []struct {
		name         string
		filterEvents string
		eventType    string
		want         bool
	}{
		{"wildcard column default matches everything", "*", "quota.exceeded", true},
		{"empty value matches everything", "", "test.ping", true},
		{"array containing the event matches", `["quota.exceeded","key.expired"]`, "key.expired", true},
		{"array without the event does not match", `["quota.exceeded"]`, "key.expired", false},
		{"array with wildcard entry matches", `["*"]`, "request.failed", true},
		{"empty array subscribes to nothing", `[]`, "quota.exceeded", false},
		{"unparseable value fails open", `{oops`, "quota.exceeded", true},
		{"surrounding whitespace is trimmed", "  *  ", "test.ping", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchesFilter(tc.filterEvents, tc.eventType); got != tc.want {
				t.Errorf("MatchesFilter(%q, %q) = %v, want %v", tc.filterEvents, tc.eventType, got, tc.want)
			}
		})
	}
}

func TestIsTargetType(t *testing.T) {
	for _, typ := range TargetTypes {
		if !IsTargetType(typ) {
			t.Errorf("IsTargetType(%q) = false, want true", typ)
		}
	}
	if IsTargetType("pigeon") {
		t.Error(`IsTargetType("pigeon") = true, want false`)
	}
}

func TestDiscordAdapterDelivers(t *testing.T) {
	srv, hits, bodies := captureServer(t)
	pointAtTestServer(t)

	target := NewTarget(TargetDiscord, srv.URL)
	if !target.Enabled() {
		t.Fatal("discord target with a url is not Enabled")
	}
	event := Event{Type: EventQuotaExceeded, Title: "Quota Exceeded", Body: "team acme hit its budget", Severity: SeverityWarn}
	if err := target.Send(event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
	body := <-bodies
	content, _ := body["content"].(string)
	if content != "**Quota Exceeded**\nteam acme hit its budget" {
		t.Errorf("content = %q, want the bold title and body", content)
	}
}

func TestDiscordAdapterTruncatesLongContent(t *testing.T) {
	event := Event{Type: EventRequestFailed, Title: "T", Body: string(make([]byte, maxDiscordContent))}
	if got := len(discordContent(event)); got != maxDiscordContent {
		t.Errorf("content length = %d, want capped at %d", got, maxDiscordContent)
	}
}

func TestSlackAdapterDelivers(t *testing.T) {
	srv, hits, bodies := captureServer(t)
	pointAtTestServer(t)

	target := NewTarget(TargetSlack, srv.URL)
	event := Event{Type: EventKeyExpired, Title: "Key Expired", Body: "claude/1 rotated out", Severity: SeverityCritical}
	if err := target.Send(event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
	body := <-bodies
	text, _ := body["text"].(string)
	if text != "*Key Expired*\nclaude/1 rotated out" {
		t.Errorf("text = %q, want the mrkdwn title and body", text)
	}
}

func TestGenericAdapterDeliversEnvelope(t *testing.T) {
	srv, hits, bodies := captureServer(t)
	pointAtTestServer(t)

	target := NewTarget(TargetGeneric, srv.URL)
	event := Event{Type: EventRequestFailed, Title: "Request Failed", Body: "codex upstream 502", Severity: SeverityCritical}
	if err := target.Send(event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
	body := <-bodies
	if body["event"] != EventRequestFailed || body["title"] != "Request Failed" ||
		body["body"] != "codex upstream 502" || body["severity"] != SeverityCritical {
		t.Errorf("envelope = %+v, want the event fields carried as fields", body)
	}
	ts, _ := body["timestamp"].(string)
	if ts == "" {
		t.Error("envelope carries no timestamp")
	}
}

func TestGenericAdapterDefaultsSeverity(t *testing.T) {
	if got := severityOrDefault(""); got != SeverityInfo {
		t.Errorf("severityOrDefault(\"\") = %q, want %q", got, SeverityInfo)
	}
}

func TestJSONAdapterReportsNon2xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"invalid webhook"}`))
	}))
	defer srv.Close()
	pointAtTestServer(t)

	target := NewTarget(TargetSlack, srv.URL)
	err := target.Send(Event{Type: EventTestPing, Title: "Ping"})
	if err == nil {
		t.Fatal("Send on 403 = nil, want an error")
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}
}

// TestAdaptersSSRFBlocked proves every destination URL must pass the REAL
// handlerutil guard: a loopback origin is refused before any request is made.
func TestAdaptersSSRFBlocked(t *testing.T) {
	srv, hits, _ := captureServer(t) // guard NOT swapped

	cases := []struct {
		name       string
		targetType string
	}{
		{"discord", TargetDiscord},
		{"slack", TargetSlack},
		{"generic", TargetGeneric},
		{"telegram", TargetTelegram},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := NewTarget(tc.targetType, srv.URL)
			if target == nil {
				t.Fatalf("NewTarget(%s) = nil", tc.targetType)
			}
			err := target.Send(Event{Type: EventTestPing, Title: "Ping"})
			if err == nil {
				t.Fatal("Send to a loopback URL = nil error, want the SSRF block")
			}
		})
	}
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 (every adapter must refuse before dialing)", hits.Load())
	}
}

// TestValidateTargetSSRF proves the dashboard write path refuses the same
// addresses the send path does.
func TestValidateTargetSSRF(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"loopback literal is blocked", "http://127.0.0.1:8080/hook", true},
		{"localhost name is blocked", "http://localhost:8080/hook", true},
		{"RFC1918 literal is blocked", "http://192.168.1.10/hook", true},
		{"public https url passes", "https://hooks.slack.com/services/a/b/c", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTarget(TargetGeneric, tc.url)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateTarget(generic, %q) error = %v, wantErr %v", tc.url, err, tc.wantErr)
			}
		})
	}
}

func TestValidateTargetRejectsUnknownTypeAndEmptyURL(t *testing.T) {
	if err := ValidateTarget("pigeon", "https://example.com/hook"); err == nil {
		t.Error("unknown type accepted")
	}
	if err := ValidateTarget(TargetDiscord, "  "); err == nil {
		t.Error("empty url accepted")
	}
}

func TestParseTelegramURL(t *testing.T) {
	const token = "1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	cases := []struct {
		name       string
		raw        string
		wantToken  string
		wantChatID string
		wantErr    bool
	}{
		{
			name:      "full sendMessage url with chat_id query",
			raw:       "https://api.telegram.org/bot" + token + "/sendMessage?chat_id=42",
			wantToken: token, wantChatID: "42",
		},
		{
			name:      "bare bot path with chat_id query",
			raw:       "https://api.telegram.org/bot" + token + "?chat_id=42",
			wantToken: token, wantChatID: "42",
		},
		{
			name:      "chat id as trailing path segment",
			raw:       "https://api.telegram.org/bot" + token + "/-100123",
			wantToken: token, wantChatID: "-100123",
		},
		{name: "no bot segment", raw: "https://api.telegram.org/x?chat_id=42", wantErr: true},
		{name: "no chat id", raw: "https://api.telegram.org/bot" + token, wantErr: true},
		{name: "no host", raw: "not a url at all", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseTelegramURL(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseTelegramURL(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if cfg.BotToken != tc.wantToken || cfg.ChatID != tc.wantChatID {
				t.Errorf("cfg = %+v, want token %q chat %q", cfg, tc.wantToken, tc.wantChatID)
			}
		})
	}
}

func TestTelegramAdapterWrapsDispatch(t *testing.T) {
	srv, _, _ := captureServer(t) // loopback url stored on the target
	pointAtTestServer(t)

	const token = "1234567890:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	raw := srv.URL + "/bot" + token + "/sendMessage?chat_id=42"
	target := NewTarget(TargetTelegram, raw)
	tg, ok := target.(*telegramTarget)
	if !ok {
		t.Fatalf("NewTarget(telegram) returned %T", target)
	}
	if !tg.Enabled() {
		t.Fatal("parsed telegram target is not Enabled")
	}
	if tg.cfg.SubscribedEvents == nil || tg.cfg.SubscribedEvents[0] != "*" {
		t.Errorf("wrapped config subscribes to %v, want [\"*\"] (this package filters)", tg.cfg.SubscribedEvents)
	}

	var gotCfg telegram.Config
	var gotEvent telegram.Event
	tg.dispatch = func(_ context.Context, _ *http.Client, cfg telegram.Config, event telegram.Event) error {
		gotCfg, gotEvent = cfg, event
		return nil
	}
	event := Event{Type: EventTestPing, Title: "Ping", Body: "line one\nline two", Severity: SeverityCritical}
	if err := tg.Send(event); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotCfg.BotToken != token || gotCfg.ChatID != "42" {
		t.Errorf("dispatched config = %+v, want the parsed token and chat", gotCfg)
	}
	if gotEvent.Type != EventTestPing || gotEvent.Title != "Ping" ||
		gotEvent.Emoji != severityEmoji(SeverityCritical) ||
		len(gotEvent.Lines) != 2 || gotEvent.Lines[0] != "line one" {
		t.Errorf("dispatched event = %+v, want the mapped telegram event", gotEvent)
	}
}

func TestTelegramTargetWithoutChatIDIsDisabled(t *testing.T) {
	target := NewTarget(TargetTelegram, "https://api.telegram.org/bot123:abc")
	if target == nil {
		t.Fatal("NewTarget = nil")
	}
	if target.Enabled() {
		t.Error("target without a chat id reports Enabled")
	}
	if err := target.Send(Event{Type: EventTestPing, Title: "Ping"}); err == nil {
		t.Error("Send on an unconfigured telegram target = nil, want an error")
	}
}

func TestNewTargetUnknownType(t *testing.T) {
	if NewTarget("pigeon", "https://example.com") != nil {
		t.Error("NewTarget(pigeon) != nil, want nil")
	}
}

// TestRedirectToBlockedHostRefused proves the send-path SSRF guard covers
// redirect hops too (audit B-03): a destination that passes the guard and
// answers 302 toward an internal address must not have its payload — or a
// connection — delivered there. Before the CheckRedirect hook the client
// followed the hop and the guard saw only the first URL.
func TestRedirectToBlockedHostRefused(t *testing.T) {
	var internalHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/internal", http.StatusFound)
		case "/internal":
			internalHits.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	orig := assertPublicURL
	// A public destination ("/start") the guard allows, and the internal
	// address the redirect points at (".../internal") the guard blocks.
	assertPublicURL = func(raw string) error {
		if strings.HasSuffix(raw, "/internal") {
			return errors.New("blocked URL: internal host")
		}
		return nil
	}
	t.Cleanup(func() { assertPublicURL = orig })

	target := NewTarget(TargetGeneric, srv.URL+"/start")
	err := target.Send(Event{Type: EventTestPing, Title: "Ping"})
	if err == nil {
		t.Fatal("Send through a 302 to a blocked host = nil error, want the redirect refusal")
	}
	if !strings.Contains(err.Error(), "redirect refused") {
		t.Errorf("Send error = %v, want it to name the refused redirect", err)
	}
	if internalHits.Load() != 0 {
		t.Errorf("internal host hits = %d, want 0 (the redirect hop must not be followed)", internalHits.Load())
	}
}

func TestValidateTargetRequiresHTTPSApartFromLoopback(t *testing.T) {
	cases := []struct {
		name   string
		target string
		url    string
		wantOK bool
	}{
		{"public https", TargetGeneric, "https://hooks.example.net/abc", true},
		{"public http", TargetGeneric, "http://hooks.example.net/abc", false},
		{"discord token over http", TargetDiscord, "http://discord.com/api/webhooks/1/token", false},
		{"loopback http dev allowance", TargetGeneric, "http://127.0.0.1:9090/hook", true},
		{"localhost http dev allowance", TargetGeneric, "http://localhost:9090/hook", true},
		{"telegram https", TargetTelegram, "https://api.telegram.org/botT:ok/sendMessage?chat_id=1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pointAtTestServer(t) // isolate from the SSRF guard; this test judges the scheme only
			err := ValidateTarget(tc.target, tc.url)
			if tc.wantOK && err != nil {
				t.Fatalf("expected %q to validate, got %v", tc.url, err)
			}
			if !tc.wantOK {
				if err == nil {
					t.Fatalf("expected %q to be rejected", tc.url)
				}
				if !strings.Contains(err.Error(), "https://") {
					t.Errorf("expected the error to require https, got %q", err.Error())
				}
			}
		})
	}
}
