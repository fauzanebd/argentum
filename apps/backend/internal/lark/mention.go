package lark

import (
	"regexp"
	"strings"
)

// MentionsBot reports whether the bot's open_id appears in the message's
// mention list. Lark events carry mentions as `@_user_N` placeholders in
// the content string and a parallel `mentions` array that maps each
// placeholder to its open_id; we just check the array.
func MentionsBot(mentions []Mention, botOpenID string) bool {
	if botOpenID == "" {
		return false
	}
	for _, m := range mentions {
		if m.ID.OpenID == botOpenID {
			return true
		}
	}
	return false
}

// mentionPlaceholderRe matches the @_user_N tokens Lark inlines into the
// text content for each mention. We strip *all* of them so the agent sees a
// clean prompt — the bot's name, other users mentioned in the same turn,
// trailing whitespace etc.
var mentionPlaceholderRe = regexp.MustCompile(`@_user_\d+`)

// StripMentions removes Lark's @_user_N tokens from a text content and
// collapses surrounding whitespace.
func StripMentions(text string) string {
	out := mentionPlaceholderRe.ReplaceAllString(text, "")
	// Collapse runs of whitespace introduced by removed tokens.
	out = strings.Join(strings.Fields(out), " ")
	return strings.TrimSpace(out)
}
