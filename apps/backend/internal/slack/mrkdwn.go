package slack

import "regexp"

// Slack renders "mrkdwn", not Markdown. The agent writes Markdown, so the
// three constructs it uses most need translating before posting — anything
// left untranslated shows up as literal punctuation in the channel.
var (
	// [text](url) -> <url|text>
	mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	// **bold** -> *bold* (Slack's bold is a single asterisk)
	mdBoldRe = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	// ## Heading -> *Heading* (Slack has no heading syntax in mrkdwn text)
	mdHeadingRe = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.+)$`)
)

// ToMrkdwn rewrites the agent's Markdown into Slack mrkdwn. Link
// conversion runs first so bold markers wrapping a link still collapse
// correctly. Everything else (lists, inline code, fenced code) is already
// compatible.
func ToMrkdwn(s string) string {
	out := mdLinkRe.ReplaceAllString(s, "<$2|$1>")
	out = mdBoldRe.ReplaceAllString(out, "*$1*")
	out = mdHeadingRe.ReplaceAllString(out, "*$1*")
	return out
}
