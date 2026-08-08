package slack

import (
	"regexp"
	"strings"
)

// mentionRe matches Slack's user-mention markup, e.g. `<@U012ABCDEF>` or
// `<@U012ABCDEF|alice>`. Slack always sends mentions in this escaped form,
// never as a plain "@name".
var mentionRe = regexp.MustCompile(`<@([A-Z0-9]+)(?:\|[^>]*)?>`)

// MentionsBot reports whether the bot's user id appears as an @mention in
// the message text. app_mention events are already mention-scoped, so this
// mainly guards messages arriving through other event types.
func MentionsBot(text, botUserID string) bool {
	if botUserID == "" {
		return false
	}
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		if m[1] == botUserID {
			return true
		}
	}
	return false
}

// StripMentions removes all `<@Uxxx>` tokens from the text and collapses
// the whitespace they leave behind, so the agent sees a clean prompt
// instead of "<@U0BOT> what were sales last week?".
func StripMentions(text string) string {
	out := mentionRe.ReplaceAllString(text, "")
	return strings.TrimSpace(strings.Join(strings.Fields(out), " "))
}
