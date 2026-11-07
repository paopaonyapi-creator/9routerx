// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"9routerx/proxy/internal/telegram"
)

// telegramTarget wraps the FASE 7 Telegram push (internal/telegram.Dispatch)
// as a webhook target. A webhookTargets row has one url column and a Bot API
// send needs a bot token plus a chat id, so the stored URL carries both:
//
//	https://api.telegram.org/bot<botToken>/sendMessage?chat_id=<chatID>
//
// The bare /bot<token> path and a chat id as the segment after the token are
// accepted too. The adapter parses that shape back into the telegram.Config
// the existing Dispatch sends; the subscription filter stays with this
// package, so the wrapped config subscribes to everything.
type telegramTarget struct {
	url string
	cfg telegram.Config
	// dispatch is the sender; the default is telegram.Dispatch. A field so the
	// offline test suite can record a send without reaching the network — the
	// real Dispatch pins its own API base and SSRF guard in package state this
	// package cannot reach.
	dispatch func(context.Context, *http.Client, telegram.Config, telegram.Event) error
}

func newTelegramTarget(rawURL string) *telegramTarget {
	t := &telegramTarget{url: rawURL, dispatch: telegram.Dispatch}
	cfg, err := parseTelegramURL(rawURL)
	if err != nil {
		return t // Enabled() stays false; ValidateTarget reports the reason
	}
	cfg.SubscribedEvents = []string{"*"} // the dispatcher filters already
	t.cfg = cfg
	return t
}

func (t *telegramTarget) Type() string  { return TargetTelegram }
func (t *telegramTarget) Enabled() bool { return t.cfg.Enabled() }

// Send delivers the event through the FASE 7 push. The stored URL is guarded
// here (not only at save time) so a row that was stored by a build without the
// guard, or edited by hand, still cannot aim the send inward.
func (t *telegramTarget) Send(event Event) error {
	if t.cfg.BotToken == "" || t.cfg.ChatID == "" {
		return fmt.Errorf("webhooks: telegram: url does not carry a bot token and chat id")
	}
	if err := assertPublicURL(t.url); err != nil {
		return fmt.Errorf("webhooks: telegram: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if err := t.dispatch(ctx, defaultClient, t.cfg, telegram.Event{
		Type:  event.Type,
		Emoji: severityEmoji(event.Severity),
		Title: event.Title,
		Lines: bodyLines(event.Body),
	}); err != nil {
		return fmt.Errorf("webhooks: telegram: %w", err)
	}
	return nil
}

// parseTelegramURL splits a stored Bot API URL into its config. The token must
// be the path segment starting with "bot"; the chat id comes from the
// chat_id query parameter, falling back to a trailing path segment that is not
// the sendMessage verb.
func parseTelegramURL(raw string) (telegram.Config, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return telegram.Config{}, fmt.Errorf("webhooks: telegram: parse url: %w", err)
	}
	if parsed.Host == "" {
		return telegram.Config{}, errors.New("webhooks: telegram: url has no host")
	}
	token := ""
	chatID := parsed.Query().Get("chat_id")
	for _, seg := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		switch {
		case seg == "":
			continue
		case strings.HasPrefix(seg, "bot") && token == "":
			token = strings.TrimPrefix(seg, "bot")
		case chatID == "" && seg != "sendMessage":
			chatID = seg
		}
	}
	if token == "" || chatID == "" {
		return telegram.Config{}, errors.New(
			"webhooks: telegram: url must be https://api.telegram.org/bot<token>/sendMessage?chat_id=<chatID>")
	}
	return telegram.Config{BotToken: token, ChatID: chatID}, nil
}

// severityEmoji maps an event severity to the leading emoji the Telegram
// message carries; an unknown severity gets the neutral satellite.
func severityEmoji(severity string) string {
	switch severity {
	case SeverityCritical:
		return "\U0001F6A8" // rotating light
	case SeverityWarn:
		return "\u26A0\uFE0F" // warning sign
	default:
		return "\U0001F4E1" // satellite antenna
	}
}

// bodyLines splits the detail body into the lines the Telegram message shows
// under its bold title.
func bodyLines(body string) []string {
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}
