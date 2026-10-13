package email

import (
	"fmt"
	"html"
	"strings"
)

// InviteData is everything the invite email says.
type InviteData struct {
	CompanyName string
	InviterName string
	AcceptURL   string
	// ExpiresIn is rendered as-is, e.g. "7 days". A date would be worse: the
	// reader is in a timezone this process does not know, and "expires 18
	// September" is ambiguous by a day where "expires in 7 days" is not.
	ExpiresIn string
}

// Invite renders the team invitation (T-F6, closing T-04's gap).
//
// **The link is the whole message.** Everything else is context for somebody
// deciding whether to click it, so the two facts that decide that — who invited
// them and to what — are in the first sentence and in the subject.
//
// Deliberately plain. This is not marketing mail: a transactional message that
// looks like a newsletter is one a spam filter treats like a newsletter, and
// the recipient is a colleague of the sender rather than a prospect.
func Invite(d InviteData) Message {
	company := d.CompanyName
	if strings.TrimSpace(company) == "" {
		company = "your team's workspace"
	}
	who := strings.TrimSpace(d.InviterName)
	intro := fmt.Sprintf("You have been invited to join %s on Argentum.", company)
	if who != "" {
		intro = fmt.Sprintf("%s invited you to join %s on Argentum.", who, company)
	}

	text := strings.Join([]string{
		intro,
		"",
		"Argentum answers questions about your company's data — you ask in plain language and it queries the database.",
		"",
		"Set your password and sign in:",
		d.AcceptURL,
		"",
		fmt.Sprintf("This link works once and expires in %s.", d.ExpiresIn),
		"If you were not expecting this, you can ignore it — nothing happens until somebody uses the link.",
	}, "\n")

	body := fmt.Sprintf(`<p>%s</p>
<p>Argentum answers questions about your company's data — you ask in plain language and it queries the database.</p>
<p><a href="%s" style="display:inline-block;padding:10px 18px;background:#F25C5C;color:#ffffff;border-radius:6px;text-decoration:none">Set your password</a></p>
<p style="color:#666;font-size:13px">This link works once and expires in %s. If you were not expecting this, you can ignore it — nothing happens until somebody uses the link.</p>
<p style="color:#666;font-size:12px">If the button does not work, paste this into your browser:<br>%s</p>`,
		html.EscapeString(intro),
		html.EscapeString(d.AcceptURL),
		html.EscapeString(d.ExpiresIn),
		html.EscapeString(d.AcceptURL))

	subject := fmt.Sprintf("Join %s on Argentum", company)
	if who != "" {
		subject = fmt.Sprintf("%s invited you to %s on Argentum", who, company)
	}
	return Message{Subject: subject, Text: text, HTML: wrap(body)}
}

// AlertData is a watcher breach on its way to an inbox (T-F7).
type AlertData struct {
	Title string
	// Body is the agent's own briefing about the breach, as markdown-ish plain
	// text. It is *not* re-rendered as markdown: this is model-authored content
	// and the one thing worse than an unformatted alert is an alert where a
	// stray character turned half of it into a heading.
	Body string
	// URL is where to see the whole thing. Optional — a deployment with no
	// APP_BASE_URL sends the alert without a link rather than not at all.
	URL string
}

// Alert renders a watcher breach.
func Alert(d AlertData) Message {
	text := d.Title + "\n\n" + d.Body
	if d.URL != "" {
		text += "\n\nOpen in Argentum: " + d.URL
	}
	body := fmt.Sprintf("<p style=\"font-weight:600\">%s</p><pre style=\"white-space:pre-wrap;font-family:inherit;margin:0\">%s</pre>",
		html.EscapeString(d.Title), html.EscapeString(d.Body))
	if d.URL != "" {
		body += fmt.Sprintf(`<p style="margin-top:16px"><a href="%s">Open in Argentum</a></p>`, html.EscapeString(d.URL))
	}
	return Message{Subject: d.Title, Text: text, HTML: wrap(body)}
}

// TopupReviewData is a proof of payment waiting for an operator (RIZ-83). Every
// amount arrives formatted: this package has no opinion on rupiah.
type TopupReviewData struct {
	CompanyName    string
	RequesterEmail string
	// Amount is the exact amount to match on the statement, "100.347".
	Amount     string
	PayeeLabel string
	// Credit is the frozen credit, "5.76".
	Credit    string
	ReviewURL string
}

// TopupReview renders the operator's alert (R14). The subject carries the two
// facts that decide whether to open it now — who, and how much to look for on
// the statement — so the inbox list is the queue.
func TopupReview(d TopupReviewData) Message {
	intro := fmt.Sprintf("%s (%s) uploaded proof for a top-up.", d.CompanyName, d.RequesterEmail)
	amount := fmt.Sprintf("Amount: Rp %s to %s", d.Amount, d.PayeeLabel)
	credit := fmt.Sprintf("Credit: $%s", d.Credit)
	review := "Review it: " + d.ReviewURL
	text := strings.Join([]string{intro, amount, credit, review}, "\n")
	body := fmt.Sprintf(`<p>%s</p>
<p>%s<br>%s</p>
<p><a href="%s" style="display:inline-block;padding:10px 18px;background:#F25C5C;color:#ffffff;border-radius:6px;text-decoration:none">Review it</a></p>
<p style="color:#666;font-size:12px">%s</p>`,
		html.EscapeString(intro),
		html.EscapeString(amount), html.EscapeString(credit),
		html.EscapeString(d.ReviewURL),
		html.EscapeString(review))
	return Message{
		Subject: fmt.Sprintf("Top-up to review: %s · Rp %s", d.CompanyName, d.Amount),
		Text:    text,
		HTML:    wrap(body),
	}
}

// FreeCreditReviewData is a new workspace waiting for its free credit
// (RIZ-89). Every value arrives formatted, for TopupReviewData's reason.
type FreeCreditReviewData struct {
	CompanyName    string
	RequesterEmail string
	// SignedUpAt is "13 Oct 2026, 14:32".
	SignedUpAt string
	// Amount is the free credit in dollars, "0.50".
	Amount    string
	ReviewURL string
}

// FreeCreditReview renders the operator's alert (R18). The subject names the
// workspace, because the operator decides from the name and the email whether
// it looks like a real business before opening anything.
func FreeCreditReview(d FreeCreditReviewData) Message {
	who := d.CompanyName
	if d.RequesterEmail != "" {
		who = fmt.Sprintf("%s (%s)", d.CompanyName, d.RequesterEmail)
	}
	intro := fmt.Sprintf("%s signed up on %s and is waiting for $%s of free credit.", who, d.SignedUpAt, d.Amount)
	review := "Review it: " + d.ReviewURL
	text := strings.Join([]string{intro, review}, "\n")
	body := fmt.Sprintf(`<p>%s</p>
<p><a href="%s" style="display:inline-block;padding:10px 18px;background:#F25C5C;color:#ffffff;border-radius:6px;text-decoration:none">Review it</a></p>
<p style="color:#666;font-size:12px">%s</p>`,
		html.EscapeString(intro),
		html.EscapeString(d.ReviewURL),
		html.EscapeString(review))
	return Message{
		Subject: "Free credit to approve: " + d.CompanyName,
		Text:    text,
		HTML:    wrap(body),
	}
}

// FreeCreditReadyData is an approval on its way to the admin who signed up.
type FreeCreditReadyData struct {
	CompanyName string
	// Amount is the credit added, in dollars, "0.50".
	Amount  string
	ChatURL string
}

// FreeCreditReady renders the approval email (R19): the one message that
// tells a waiting user to come back, so the link is to the chat itself.
func FreeCreditReady(d FreeCreditReadyData) Message {
	intro := fmt.Sprintf("We've added $%s of free credit to %s.", d.Amount, d.CompanyName)
	ask := "Ask your first question: " + d.ChatURL
	text := strings.Join([]string{intro, ask}, " ")
	body := fmt.Sprintf(`<p>%s</p>
<p><a href="%s" style="display:inline-block;padding:10px 18px;background:#F25C5C;color:#ffffff;border-radius:6px;text-decoration:none">Ask your first question</a></p>
<p style="color:#666;font-size:12px">%s</p>`,
		html.EscapeString(intro),
		html.EscapeString(d.ChatURL),
		html.EscapeString(ask))
	return Message{
		Subject: "Your Argentum free credit is ready",
		Text:    text,
		HTML:    wrap(body),
	}
}

// wrap puts a body in the one shared shell. Inline styles and a table-free
// layout, because email clients are not browsers and a <style> block is
// stripped by several of the ones that matter.
func wrap(body string) string {
	return `<!doctype html><html><body style="margin:0;padding:24px;background:#f6f6f6;font-family:system-ui,-apple-system,'Segoe UI',sans-serif;color:#1a1a1a">
<div style="max-width:520px;margin:0 auto;background:#ffffff;border-radius:10px;padding:28px;line-height:1.55">
` + body + `
<p style="margin-top:28px;color:#999;font-size:12px">Sent by Argentum. You are receiving this because somebody at your company uses it.</p>
</div></body></html>`
}
