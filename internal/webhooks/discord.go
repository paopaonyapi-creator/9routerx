// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package webhooks

import "fmt"

// discordTarget posts {content} to a Discord webhook URL.
type discordTarget struct {
	url string
}

func (t discordTarget) Type() string  { return TargetDiscord }
func (t discordTarget) Enabled() bool { return t.url != "" }

// discordMessage is the Discord webhook execute body. Only the content field
// is used: the destination is operator-configured and posts plain markdown.
type discordMessage struct {
	Content string `json:"content"`
}

// maxDiscordContent is the content length a Discord message accepts. A longer
// notification is truncated rather than refused: a webhook that errors on
// every long alert is a silently dead channel.
const maxDiscordContent = 2000

// discordContent renders the event as Discord markdown: a bold title line with
// the body under it, capped at what one message accepts.
func discordContent(event Event) string {
	content := "**" + event.Title + "**"
	if event.Body != "" {
		content += "\n" + event.Body
	}
	if len(content) > maxDiscordContent {
		content = content[:maxDiscordContent]
	}
	return content
}

func (t discordTarget) Send(event Event) error {
	if t.url == "" {
		return fmt.Errorf("webhooks: discord: url is empty")
	}
	return postMessage(t.url, discordMessage{Content: discordContent(event)})
}
