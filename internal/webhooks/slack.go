// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import "fmt"

// slackTarget posts {text} to a Slack incoming-webhook URL.
type slackTarget struct {
	url string
}

func (t slackTarget) Type() string  { return TargetSlack }
func (t slackTarget) Enabled() bool { return t.url != "" }

// slackMessage is the Slack incoming-webhook body. Only the text field is
// used: the destination is operator-configured and posts plain mrkdwn.
type slackMessage struct {
	Text string `json:"text"`
}

// slackText renders the event as Slack mrkdwn: a bold title line with the body
// under it, capped at the 40,000 characters one Slack message accepts (a cap
// so a pathological body cannot make the request unbounded).
func slackText(event Event) string {
	text := "*" + event.Title + "*"
	if event.Body != "" {
		text += "\n" + event.Body
	}
	if len(text) > maxSlackText {
		text = text[:maxSlackText]
	}
	return text
}

// maxSlackText mirrors Slack's documented per-message ceiling.
const maxSlackText = 40000

func (t slackTarget) Send(event Event) error {
	if t.url == "" {
		return fmt.Errorf("webhooks: slack: url is empty")
	}
	return postMessage(t.url, slackMessage{Text: slackText(event)})
}
