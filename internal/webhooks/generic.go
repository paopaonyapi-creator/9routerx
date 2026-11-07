// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import "fmt"

// genericTarget posts the event as a self-describing JSON object to any URL —
// a ntfy topic, a home-automation hook, an operator's own collector. The
// envelope is the contract: every field the other adapters fold into prose is
// carried as its own field instead.
type genericTarget struct {
	url string
}

func (t genericTarget) Type() string  { return TargetGeneric }
func (t genericTarget) Enabled() bool { return t.url != "" }

// genericPayload is the generic adapter's envelope.
type genericPayload struct {
	Event     string `json:"event"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Severity  string `json:"severity"`
	Timestamp string `json:"timestamp"`
}

func (t genericTarget) Send(event Event) error {
	if t.url == "" {
		return fmt.Errorf("webhooks: generic: url is empty")
	}
	return postMessage(t.url, genericPayload{
		Event:     event.Type,
		Title:     event.Title,
		Body:      event.Body,
		Severity:  severityOrDefault(event.Severity),
		Timestamp: nowRFC3339(),
	})
}

// severityOrDefault normalizes an empty severity to info so every envelope
// carries an explicit value a consumer can switch on.
func severityOrDefault(severity string) string {
	if severity == "" {
		return SeverityInfo
	}
	return severity
}
