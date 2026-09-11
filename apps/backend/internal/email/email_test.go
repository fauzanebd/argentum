package email

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A deployment with nothing configured must boot, and must be honest about it.
func TestAnUnconfiguredDeploymentGetsASenderThatSaysSo(t *testing.T) {
	cases := map[string]Config{
		"disabled":  {Enabled: false, Host: "smtp.example.com", From: "a@example.com"},
		"no host":   {Enabled: true, From: "a@example.com"},
		"no from":   {Enabled: true, Host: "smtp.example.com"},
		"all empty": {},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := New(c)
			if s.Enabled() {
				t.Error("Enabled() = true on a deployment with nothing configured")
			}
			if err := s.Send(context.Background(), Message{To: []string{"b@example.com"}}); !errors.Is(err, ErrDisabled) {
				t.Errorf("Send() = %v; want ErrDisabled — a caller has to be able to tell 'sent' from 'nowhere to send'", err)
			}
		})
	}
}

func TestAConfiguredDeploymentGetsARealSender(t *testing.T) {
	s := New(Config{Enabled: true, Host: "smtp.example.com", From: "Argentum <no-reply@example.com>"})
	if !s.Enabled() {
		t.Fatal("Enabled() = false with host and from set")
	}
	if _, ok := s.(*SMTPSender); !ok {
		t.Errorf("New() = %T; want *SMTPSender", s)
	}
}

// The header value is what a human sees; the envelope is what SPF is checked
// against. Handing a relay the display name is how a deployment's whole mail
// output starts bouncing with a syntax error nobody reads.
func TestTheEnvelopeSenderIsTheBareAddress(t *testing.T) {
	cases := map[string]string{
		"Argentum <no-reply@example.com>": "no-reply@example.com",
		"no-reply@example.com":            "no-reply@example.com",
		"  spaced@example.com  ":          "spaced@example.com",
		"Weird <Name> <a@b.com>":          "a@b.com",
	}
	for in, want := range cases {
		if got := envelopeFrom(in); got != want {
			t.Errorf("envelopeFrom(%q) = %q; want %q", in, got, want)
		}
	}
}

// Indonesian subject lines are the normal case on this deployment, and a raw
// high byte in a header is mojibake in half the clients that see it.
func TestANonASCIISubjectIsEncoded(t *testing.T) {
	out := string(Render("a@example.com", Message{
		To: []string{"b@example.com"}, Subject: "Undangan — gabung ke tim", Text: "hi", HTML: "<p>hi</p>",
	}))
	subject := headerLine(t, out, "Subject: ")
	if strings.Contains(subject, "—") {
		t.Errorf("Subject = %q; a non-ASCII character reached the header raw", subject)
	}
	if !strings.Contains(subject, "=?utf-8?") {
		t.Errorf("Subject = %q; want an encoded-word", subject)
	}
}

// A client picks the LAST part it can display. Reversed, every modern client
// would show the plain text.
func TestThePlainTextPartComesBeforeTheHTML(t *testing.T) {
	out := string(Render("a@example.com", Message{
		To: []string{"b@example.com"}, Subject: "s", Text: "plain", HTML: "<p>rich</p>",
	}))
	textAt := strings.Index(out, "text/plain")
	htmlAt := strings.Index(out, "text/html")
	if textAt < 0 || htmlAt < 0 {
		t.Fatalf("both parts must be present:\n%s", out)
	}
	if textAt > htmlAt {
		t.Error("the HTML part comes first; every client would show the plain text")
	}
}

// In SMTP a line containing only "." ends the message. A body with one in it
// would be truncated there and the rest read as SMTP commands.
func TestABareDotLineCannotEndTheMessage(t *testing.T) {
	out := string(Render("a@example.com", Message{
		To: []string{"b@example.com"}, Subject: "s",
		Text: "first\n.\nsecond", HTML: "<p>x</p>",
	}))
	if strings.Contains(out, "\r\n.\r\n") {
		t.Errorf("a bare dot line survived into the message:\n%s", out)
	}
	if !strings.Contains(out, "\r\n..\r\n") {
		t.Errorf("the dot was not escaped:\n%s", out)
	}
}

func TestEveryLineEndingIsCRLF(t *testing.T) {
	out := string(Render("a@example.com", Message{
		To: []string{"b@example.com"}, Subject: "s", Text: "a\nb\r\nc\rd", HTML: "<p>x</p>",
	}))
	for i := 0; i < len(out); i++ {
		if out[i] == '\n' && (i == 0 || out[i-1] != '\r') {
			t.Fatalf("a bare LF at byte %d:\n%q", i, out)
		}
	}
}

func TestSendingToNobodyIsNotAnError(t *testing.T) {
	s := &SMTPSender{cfg: Config{Host: "nowhere.invalid", Port: 25, From: "a@example.com"}}
	// No To addresses, so it must return before it ever dials — which is also
	// what makes this test possible without a relay.
	if err := s.Send(context.Background(), Message{Subject: "s"}); err != nil {
		t.Errorf("Send() with no recipients = %v; a company with nobody who wants email is not a failure", err)
	}
}

// ---- templates ----

func TestTheInviteLeadsWithWhoAndWhat(t *testing.T) {
	m := Invite(InviteData{
		CompanyName: "Gelael", InviterName: "Sari",
		AcceptURL: "https://app.example.com/accept-invite?token=abc", ExpiresIn: "7 days",
	})
	if !strings.Contains(m.Subject, "Sari") || !strings.Contains(m.Subject, "Gelael") {
		t.Errorf("Subject = %q; the two facts that decide whether somebody clicks are missing", m.Subject)
	}
	for _, body := range []string{m.Text, m.HTML} {
		if !strings.Contains(body, "accept-invite?token=abc") {
			t.Error("the link is missing from a body")
		}
		if !strings.Contains(body, "7 days") {
			t.Error("the expiry is missing from a body")
		}
	}
	// Plain text must be a usable email on its own — the URL bare, not wrapped
	// in a tag a text client will print literally.
	if strings.Contains(m.Text, "<a ") {
		t.Errorf("the plain-text part contains markup:\n%s", m.Text)
	}
}

func TestTheInviteSurvivesAMissingInviterAndCompany(t *testing.T) {
	m := Invite(InviteData{AcceptURL: "https://x/y", ExpiresIn: "7 days"})
	if strings.Contains(m.Subject, "  ") || strings.HasPrefix(m.Subject, " ") {
		t.Errorf("Subject = %q; an empty name left a gap", m.Subject)
	}
	if !strings.Contains(m.Text, "your team's workspace") {
		t.Errorf("Text = %q; want a fallback for the company name", m.Text)
	}
}

// The invite URL is attacker-influenced only by an admin of the same company,
// but it lands in a page somebody reads: escaping it is the difference between
// a link and an injection into the one HTML this product mails out.
func TestAnglesInTemplateDataCannotReachTheHTML(t *testing.T) {
	m := Invite(InviteData{
		CompanyName: `Acme<script>alert(1)</script>`,
		AcceptURL:   "https://x/y",
		ExpiresIn:   "7 days",
	})
	if strings.Contains(m.HTML, "<script>") {
		t.Errorf("a script tag survived into the HTML body:\n%s", m.HTML)
	}
}

func TestAnAlertWithoutALinkStillSends(t *testing.T) {
	m := Alert(AlertData{Title: "Revenue down 18% week on week", Body: "Three drivers:\n- …"})
	if m.Subject == "" || m.Text == "" || m.HTML == "" {
		t.Fatal("an alert with no URL produced an incomplete message")
	}
	if strings.Contains(m.Text, "Open in Argentum") {
		t.Error("a link was offered with no URL behind it")
	}
}

func headerLine(t *testing.T, msg, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no %q header in:\n%s", prefix, msg)
	return ""
}
