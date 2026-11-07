// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package webhooks fans gateway events out to operator-configured push
// destinations (FASE 10.4). It generalizes the FASE 7 Telegram-only push
// (internal/telegram, which stays untouched and is wrapped as one adapter) to
// a multi-target interface: one event, N destinations, each its own adapter.
//
// Everything here is fire-and-forget like the Telegram push it wraps: a
// notification that fails must never fail, or even slow down, the request that
// produced it. Every destination URL must pass the handlerutil SSRF guard —
// an operator-configured URL is still a URL the gateway will POST to, and a
// dashboard session must not be able to aim it at internal infrastructure.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"9routerx/proxy/internal/handlerutil"
)

// Event types this gateway pushes. The strings match internal/telegram's
// vocabulary so one event crosses every adapter unchanged.
const (
	EventQuotaExceeded = "quota.exceeded"
	EventRequestFailed = "request.failed"
	EventKeyExpired    = "key.expired"
	EventTestPing      = "test.ping"
)

// Event severities, mirroring the notification feed's vocabulary.
const (
	SeverityInfo     = "info"
	SeverityWarn     = "warn"
	SeverityCritical = "critical"
)

// Target types — the closed vocabulary of the webhookTargets.type column.
const (
	TargetTelegram = "telegram"
	TargetDiscord  = "discord"
	TargetSlack    = "slack"
	TargetGeneric  = "generic"
)

// TargetTypes is the closed set the dashboard API validates a type against.
var TargetTypes = []string{TargetTelegram, TargetDiscord, TargetSlack, TargetGeneric}

// IsTargetType reports whether typ names a known adapter.
func IsTargetType(typ string) bool {
	for _, known := range TargetTypes {
		if typ == known {
			return true
		}
	}
	return false
}

// Event is one notification to fan out.
type Event struct {
	Type     string // one of the Event* constants
	Title    string // short bold label line
	Body     string // detail text under the title
	Severity string // SeverityInfo | SeverityWarn | SeverityCritical
}

// Target is one destination adapter. Send never retries and owns its own
// 10s timeout; a failed notification is logged by the dispatcher and dropped.
type Target interface {
	Send(event Event) error
	Type() string
	// Enabled reports whether the destination carries enough configuration to
	// be worth a send. A target that is not enabled is skipped, not an error.
	Enabled() bool
}

// sendTimeout bounds one adapter send. It applies to the JSON adapters' client
// and to the context their Send builds; the telegram adapter's inner Dispatch
// applies the same value on its side.
const sendTimeout = 10 * time.Second

// defaultClient is shared by every JSON adapter: one pooled transport for all
// destinations instead of a client per send (§4.A HTTP transport hygiene).
//
// CheckRedirect re-runs the SSRF guard on every hop. postJSON validated only
// the first URL; without this, a public destination answering 302 Location:
// http://169.254.169.254/... (or any internal address) sent the client — with
// the payload still attached — straight at the internal host the guard exists
// to keep it away from (audit B-03). A redirect to a blocked address aborts
// the send; the returned error reads like any other refused destination.
var defaultClient = &http.Client{
	Timeout: sendTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if err := assertPublicURL(req.URL.String()); err != nil {
			return fmt.Errorf("webhooks: redirect refused: %w", err)
		}
		if len(via) >= 10 {
			return fmt.Errorf("webhooks: stopped after 10 redirects")
		}
		return nil
	},
}

// assertPublicURL is the SSRF guard every destination URL must pass before a
// byte is written. A variable for the same reason internal/telegram keeps one:
// the offline test suite sends to loopback httptest servers, which is exactly
// what the guard exists to refuse.
var assertPublicURL = handlerutil.AssertPublicURL

// NewTarget builds the adapter a webhookTargets row describes, or nil when the
// row's type names no adapter. A telegram URL that does not parse comes back
// as a disabled target: the dispatcher skips it silently, and the dashboard
// API surfaces the parse error through ValidateTarget instead.
func NewTarget(targetType, url string) Target {
	switch targetType {
	case TargetTelegram:
		return newTelegramTarget(url)
	case TargetDiscord:
		return discordTarget{url: url}
	case TargetSlack:
		return slackTarget{url: url}
	case TargetGeneric:
		return genericTarget{url: url}
	default:
		return nil
	}
}

// ValidateTarget runs every check a dashboard write must pass before the row
// is stored: the SSRF guard on the destination, the outbound scheme, and, for
// a telegram target, the Bot API URL shape. The returned error is shown to the
// operator verbatim.
func ValidateTarget(targetType, url string) error {
	if !IsTargetType(targetType) {
		return fmt.Errorf("webhooks: unknown target type %q (want one of %s)",
			targetType, strings.Join(TargetTypes, ", "))
	}
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("webhooks: url is required")
	}
	if err := assertPublicURL(url); err != nil {
		return fmt.Errorf("webhooks: %w", err)
	}
	if err := assertOutboundScheme(url); err != nil {
		return fmt.Errorf("webhooks: %w", err)
	}
	if targetType == TargetTelegram {
		if _, err := parseTelegramURL(url); err != nil {
			return err
		}
	}
	return nil
}

// assertOutboundScheme requires https on every webhook destination. The SSRF
// guard has already rejected loopback and private hosts by the time this runs,
// so any URL that reaches here points at a public host — and a public
// destination reached over http:// would carry the event payload, plus the
// destination's own credential (a discord/slack webhook URL embeds its token
// in the path) in plaintext. Loopback http:// stays legal for local testing;
// the guard only lets such a URL through when the test suite swaps it out.
func assertOutboundScheme(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// The SSRF guard already parsed and passed this URL; failing to parse
		// here cannot happen in practice, and a scheme check must not turn into
		// the error that masks the guard's clearer one.
		return nil
	}
	if parsed.Scheme == "https" {
		return nil
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return nil // local-dev allowance
	}
	return fmt.Errorf("url must use https:// — an http:// destination sends the payload (and, for discord/slack, the URL's own token) in plaintext")
}

// MatchesFilter reports whether eventType passes one filterEvents column
// value. "*", an empty value and a JSON array containing "*" subscribe to
// everything; any other JSON array lists the event types exactly. A value that
// parses as neither is treated as "*": the column is written by the dashboard
// API which validates it, so an unparseable value is a hand edit — and a
// webhook that over-notifies is recoverable, while a silently dead alert
// channel is not noticed until an incident.
func MatchesFilter(filterEvents, eventType string) bool {
	raw := strings.TrimSpace(filterEvents)
	if raw == "" || raw == "*" {
		return true
	}
	var filters []string
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return true
	}
	if len(filters) == 0 {
		return false
	}
	for _, want := range filters {
		if want == "*" || want == eventType {
			return true
		}
	}
	return false
}

// plainMessage renders Title + Body the way the text-field adapters (Discord,
// Slack) post it.
func plainMessage(event Event) string {
	if event.Body == "" {
		return event.Title
	}
	return event.Title + "\n" + event.Body
}

// postMessage is Send's body for the JSON adapters: guard the destination,
// encode the payload, POST it, all under the 10s send timeout.
func postMessage(dest string, payload any) error {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	return postJSON(ctx, defaultClient, dest, payload)
}

// postJSON sends payload to dest and reports the outcome. The SSRF guard runs
// here so an adapter cannot forget it; a non-2xx answer is an error carrying
// the status and a capped slice of the body.
func postJSON(ctx context.Context, client *http.Client, dest string, payload any) error {
	if err := assertPublicURL(dest); err != nil {
		return fmt.Errorf("webhooks: %w", err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhooks: encode payload: %w", err)
	}
	if client == nil {
		client = defaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhooks: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhooks: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The body is only ever reported in an error line; cap what is read.
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhooks: destination returned %s: %s",
			resp.Status, strings.TrimSpace(string(respBody)))
	}
	return nil
}
