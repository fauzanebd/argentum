package email

import (
	"fmt"
	"mime"
	"strings"
	"time"
)

// boundary is fixed rather than random. A multipart boundary only has to not
// appear in the payload, and both parts here are generated from templates whose
// content this package controls — a random one would make the rendered output
// untestable for the sake of a property nothing depends on.
const boundary = "argentum-boundary-3f9c1a"

// Render builds the RFC 5322 message: headers, then a multipart/alternative
// body with the plain text first and the HTML second.
//
// **Order matters and is not cosmetic.** A client picks the *last* part it can
// display, so text-then-HTML shows HTML where it can and falls back where it
// cannot. Reversed, every modern client would show the plain text.
//
// Split out of Send so the bytes on the wire are reachable in a test without an
// SMTP server — which is the only way to check the one property that actually
// bites, that a subject with a non-ASCII character is encoded rather than sent
// raw.
func Render(from string, m Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(m.To, ", ") + "\r\n")
	// Encoded-word for anything outside ASCII. Indonesian subject lines are the
	// normal case on this deployment, and a raw high byte in a header is what
	// turns "Undangan bergabung" into mojibake in half of the clients that see
	// it.
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	// This is transactional mail to somebody who was invited by a colleague, not
	// a mailing list — but the header is what tells an automatic responder not
	// to reply and a bulk filter that nobody signed up for a newsletter.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n", boundary)
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(normalizeCRLF(m.Text))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	b.WriteString(normalizeCRLF(m.HTML))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// normalizeCRLF makes every line ending CRLF, and escapes a line that is a bare
// dot.
//
// The bare dot is not a nicety: in SMTP a line containing only "." ends the
// message, so an email body with one in it would be *truncated at that point*
// and the rest interpreted as SMTP commands. net/smtp's data writer does this
// too, but doing it here means Render's output is a valid message on its own
// rather than only when it goes through that one writer.
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "." {
			lines[i] = ".."
		}
	}
	return strings.Join(lines, "\r\n")
}

// envelopeFrom pulls the bare address out of a "Name <addr>" header value. The
// envelope sender is what a relay checks against SPF, and handing it the
// display name is the mistake that gets a whole deployment's mail rejected with
// a syntax error nobody reads.
func envelopeFrom(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.Index(from[i:], ">"); j > 0 {
			return strings.TrimSpace(from[i+1 : i+j])
		}
	}
	return strings.TrimSpace(from)
}
