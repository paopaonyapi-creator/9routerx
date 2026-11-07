// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
// Package telegram pushes gateway events to a Telegram bot chat (OmniRoute
// port). Everything here is fire-and-forget: a notification that fails must
// never fail, or even slow down, the request that produced it.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"9routerx/proxy/internal/handlerutil"
)

// botTokenPattern is Telegram's bot-token shape: a numeric bot id, a colon,
// and at least 35 alphanumeric, underscore or dash characters. The token is
// interpolated into the request URL, so it is validated before it ever
// touches a URL string.
var botTokenPattern = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{35,}$`)

// ValidateBotToken reports whether token has the shape the Bot API issues.
func ValidateBotToken(token string) bool {
	return botTokenPattern.MatchString(token)
}

// markdownEscaper escapes the Markdown-v1 special characters Telegram's
// legacy parse mode treats as markup. A chat_id owner controls neither the
// model names nor the error text that flow into a message, so every
// operator-supplied fragment goes through this before it is sent.
var markdownEscaper = strings.NewReplacer(
	"`", "\\`",
	"_", "\\_",
	"*", "\\*",
	"[", "\\[",
	"]", "\\]",
)

// EscapeMarkdownV1 prefixes every Markdown-v1 markup character with a
// backslash so the text renders literally.
func EscapeMarkdownV1(s string) string {
	return markdownEscaper.Replace(s)
}

// Event types this gateway pushes. A config subscribes by name; "*"
// subscribes to everything.
const (
	EventQuotaExceeded = "quota.exceeded"
	EventRequestFailed = "request.failed"
	EventKeyExpired    = "key.expired"
	EventTestPing      = "test.ping"
)

// Event is one notification to push.
type Event struct {
	Type  string   // one of the Event* constants
	Emoji string   // leading emoji, e.g. "🚨"
	Title string   // bold label line
	Lines []string // detail lines under the title
}

// Config is the stored push destination. It rides the settings row; the
// subscribed events are an allowlist, not a denylist, so an empty list means
// nothing is sent.
type Config struct {
	BotToken         string   `json:"botToken"`
	ChatID           string   `json:"chatId"`
	SubscribedEvents []string `json:"subscribedEvents"`
}

// Enabled reports whether the config names a usable destination. A config
// with only one of the two fields is a half-finished edit, not a destination.
func (c Config) Enabled() bool {
	return c.BotToken != "" && c.ChatID != ""
}

// subscribed reports whether eventType is one the config asked for.
func (c Config) subscribed(eventType string) bool {
	for _, want := range c.SubscribedEvents {
		if want == "*" || want == eventType {
			return true
		}
	}
	return false
}

// sendMessageRequest is the Bot API sendMessage body.
type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// sendTimeout bounds one dispatch. It applies to the default client and to
// DispatchAsync's context; a caller-supplied client owns its own timeout.
const sendTimeout = 10 * time.Second

var defaultClient = &http.Client{Timeout: sendTimeout}

// telegramAPIBase is the Bot API origin. A variable only so tests can point
// Dispatch at an httptest server; production never rewrites it.
var telegramAPIBase = "https://api.telegram.org"

// assertPublicURL is the SSRF guard the send URL must pass. A variable for
// the same reason as telegramAPIBase: the httptest server listens on the
// loopback the guard exists to refuse.
var assertPublicURL = handlerutil.AssertPublicURL

// buildMessage renders the event as Telegram Markdown-v1: the emoji, the
// bold label, then the detail lines. Every human-readable fragment is
// escaped; only the markup this builder emits is live.
func buildMessage(event Event) string {
	var b strings.Builder
	if event.Emoji != "" {
		b.WriteString(event.Emoji)
		b.WriteString(" ")
	}
	b.WriteString("*")
	b.WriteString(EscapeMarkdownV1(event.Title))
	b.WriteString("*")
	for _, line := range event.Lines {
		b.WriteString("\n")
		b.WriteString(EscapeMarkdownV1(line))
	}
	return b.String()
}

// Dispatch sends one event to the configured chat and returns the outcome.
// It filters on the subscribed events first, validates the bot token before
// any URL is built, refuses non-public destinations, and reports a non-2xx
// Bot API answer as an error. A nil client uses the package default with the
// 10s send timeout. Dispatch never retries: the caller decides whether a
// lost notification matters, and for the request path it does not.
func Dispatch(ctx context.Context, client *http.Client, cfg Config, event Event) error {
	if !cfg.subscribed(event.Type) {
		return nil
	}
	if !ValidateBotToken(cfg.BotToken) {
		return fmt.Errorf("telegram: invalid bot token")
	}
	sendURL := telegramAPIBase + "/bot" + cfg.BotToken + "/sendMessage"
	if err := assertPublicURL(sendURL); err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	payload, err := json.Marshal(sendMessageRequest{
		ChatID:    cfg.ChatID,
		Text:      buildMessage(event),
		ParseMode: "Markdown",
	})
	if err != nil {
		return fmt.Errorf("telegram: encode sendMessage: %w", err)
	}
	if client == nil {
		client = defaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("telegram: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: sendMessage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The body is only ever reported in an error line; cap what is read.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("telegram: sendMessage returned %s: %s",
			resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// DispatchAsync pushes event in a background goroutine and returns
// immediately: it never blocks the request path, never propagates a
// failure into it, and the goroutine's lifecycle is bounded by its own
// 10s timeout context rather than by any request. Used by the chat handler
// to raise quota.exceeded / request.failed / key.exceeded without paying a
// network round trip inside a streaming response.
func DispatchAsync(cfg Config, event Event) {
	if !cfg.Enabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		_ = Dispatch(ctx, nil, cfg, event)
	}()
}
