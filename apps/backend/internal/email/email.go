// Package email is how this product reaches a person who is not looking at it.
//
// It did not exist until T-F6, and the absence was load-bearing in three
// places: T-04's team invite produced a link and handed it to the *inviting
// admin* to forward by hand; T-H6's data export and erasure record had no way
// to reach the person who requested them; and a tenant with no Slack, Discord
// or Lark could not receive a watcher's push at all, because every delivery
// channel this product had was a team-chat product.
//
// **This package sends. Nothing here receives.** Inbound email is a new
// untrusted-input surface with its own threat model — T-H8's argument, and the
// same one that keeps the roster's channel bindings outbound-shaped — and it is
// deliberately not in this track.
package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// Message is one email. Both bodies are required for anything user-facing:
// Text is what a plain-text client and most spam filters read, HTML is what a
// person sees. A message with only HTML is the shape that lands in a junk
// folder.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Sender is the one thing every caller needs. Narrow on purpose: a caller that
// could also list, retry or inspect delivery would grow a dependency on which
// provider is behind it.
type Sender interface {
	Send(ctx context.Context, m Message) error
	// Enabled reports whether this sender will actually deliver. Callers use it
	// to decide whether to *say* an email was sent, never to decide whether to
	// do their own job — an invite is created whether or not it can be mailed.
	Enabled() bool
}

// Config is what a deployment sets.
type Config struct {
	Enabled  bool
	Host     string
	Port     int
	Username string
	Password string
	// From is the envelope and header sender, e.g. "Argentum <no-reply@example.com>".
	From string
	// Timeout bounds the whole conversation with the relay.
	Timeout time.Duration
}

// nopSender is what a deployment with nothing configured gets.
//
// It is not an error type. A build with no SMTP configuration is a valid
// deployment — it is what every existing one is — and the callers all have a
// fallback (the invite still returns its link). What it must not do is look
// like it worked: Enabled() is false, so a caller can tell the difference
// between "sent" and "there was nowhere to send it", which is exactly the
// distinction T-P8's embedder resolver got wrong when it returned no client and
// no error and the boot log printed "enabled".
type nopSender struct{}

func (nopSender) Send(context.Context, Message) error { return ErrDisabled }
func (nopSender) Enabled() bool                       { return false }

// ErrDisabled is returned by the no-op sender. Callers should treat it as "not
// configured" rather than as a failure — it is the one error here that says
// nothing went wrong.
var ErrDisabled = errors.New("email is not configured on this deployment")

// New builds a Sender from config. It never returns an error: a deployment with
// no relay gets the no-op sender and one log line, because failing to boot over
// an optional outbound channel would take the whole product down for a feature
// most tenants never touch.
func New(c Config) Sender {
	if !c.Enabled || strings.TrimSpace(c.Host) == "" || strings.TrimSpace(c.From) == "" {
		// Said once, at startup, at Info. The alternative — silence — is how a
		// deployment ends up believing its invites are arriving.
		logrus.WithFields(logrus.Fields{
			"enabled":  c.Enabled,
			"has_host": strings.TrimSpace(c.Host) != "",
			"has_from": strings.TrimSpace(c.From) != "",
		}).Info("email is not configured; invites will return a link instead of sending one")
		return nopSender{}
	}
	if c.Port == 0 {
		c.Port = 587
	}
	if c.Timeout <= 0 {
		c.Timeout = 15 * time.Second
	}
	logrus.WithFields(logrus.Fields{
		"host": c.Host, "port": c.Port, "from": c.From,
	}).Info("email enabled")
	return &SMTPSender{cfg: c}
}

// SMTPSender delivers over SMTP with STARTTLS.
//
// SMTP rather than a provider SDK, for two reasons that are both about who can
// run this: every operator can point it at something they already have, and a
// test deployment can point it at a local catcher without an account anywhere.
// A provider implementation goes behind the same interface later without
// touching a caller — the arrangement whatsapp.Provider already established for
// the Meta and Twilio backends.
type SMTPSender struct{ cfg Config }

func (s *SMTPSender) Enabled() bool { return true }

func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	if len(m.To) == 0 {
		// Not an error worth failing a caller over: "send this to nobody" is
		// what a company with no members who want email looks like.
		return nil
	}
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	d := net.Dialer{Timeout: s.cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))
	}

	cl, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = cl.Quit() }()

	// STARTTLS whenever the relay offers it, which every relay worth using
	// does. Not required outright: the common development arrangement is a
	// catcher on loopback with no certificate, and refusing that would mean
	// nobody could see what these emails look like before a customer does.
	if ok, _ := cl.Extension("STARTTLS"); ok {
		if err := cl.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			// The error is wrapped without the credential. smtp's own messages
			// quote the server's reply, which does not contain the password —
			// but the username is an address, and T-H7's rule is that the
			// operational log carries shapes rather than identities.
			return fmt.Errorf("smtp auth failed for %s", s.cfg.Host)
		}
	}
	if err := cl.Mail(envelopeFrom(s.cfg.From)); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	for _, to := range m.To {
		if err := cl.Rcpt(to); err != nil {
			return fmt.Errorf("smtp rcpt: %w", err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(Render(s.cfg.From, m)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return nil
}
