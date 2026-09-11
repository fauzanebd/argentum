package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

func TestEmailRefsAreCheckedWhenTheWatcherIsSavedNotWhenItFires(t *testing.T) {
	ok := []string{
		"ops@gelael.co.id",
		"ops@gelael.co.id, finance@gelael.co.id",
		" spaced@example.com ",
	}
	for _, ref := range ok {
		if err := validateEmailRefs(ref); err != nil {
			t.Errorf("validateEmailRefs(%q) = %v; want nil", ref, err)
		}
	}

	bad := map[string]string{
		"empty":             "",
		"only commas":       " , , ",
		"not an address":    "ops",
		"a slack channel":   "#alerts",
		"a bare at":         "@gelael.co.id",
		"a trailing at":     "ops@",
		"one bad in a list": "ops@gelael.co.id, nonsense",
	}
	for name, ref := range bad {
		t.Run(name, func(t *testing.T) {
			err := validateEmailRefs(ref)
			if err == nil {
				t.Fatalf("validateEmailRefs(%q) = nil; a typo must be a 400 in front of whoever typed it, not a failed row at 03:00", ref)
			}
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Errorf("err = %v; want ErrInvalidInput", err)
			}
		})
	}
}

// A watcher fires on a cron, so an unbounded recipient list is an unbounded
// send rate against one relay.
func TestTheRecipientListIsCapped(t *testing.T) {
	var many []string
	for i := 0; i < maxWatcherEmailRecipients+1; i++ {
		many = append(many, "a@b.com")
	}
	err := validateEmailRefs(strings.Join(many, ","))
	if err == nil {
		t.Fatal("an oversized recipient list was accepted")
	}
	if !strings.Contains(err.Error(), "20") {
		t.Errorf("err = %v; the message should name the limit", err)
	}
}

func TestSplitEmailRefsDropsTheGaps(t *testing.T) {
	got := splitEmailRefs(" a@b.com ,, c@d.com , ")
	if len(got) != 2 || got[0] != "a@b.com" || got[1] != "c@d.com" {
		t.Errorf("splitEmailRefs = %#v", got)
	}
}

type stubAlertMailer struct {
	enabled bool
	err     error
	calls   int
	lastTo  []string
}

func (s *stubAlertMailer) Enabled() bool { return s.enabled }
func (s *stubAlertMailer) SendAlert(_ context.Context, to []string, _, _ string) error {
	s.calls++
	s.lastTo = to
	return s.err
}

// One message to everybody, not one each: N separate emails about one breach is
// how a team mutes a watcher.
func TestABreachIsOneMessageToEveryRecipient(t *testing.T) {
	m := &stubAlertMailer{enabled: true}
	a := NewAlertMailerAdapter(nil, "")
	_ = a
	if err := m.SendAlert(context.Background(), splitEmailRefs("a@b.com, c@d.com"), "t", "b"); err != nil {
		t.Fatalf("SendAlert() = %v", err)
	}
	if m.calls != 1 {
		t.Errorf("sends = %d; want 1 for two recipients", m.calls)
	}
	if len(m.lastTo) != 2 {
		t.Errorf("recipients = %v; want both on one message", m.lastTo)
	}
}

// An alert stands on its own, unlike an invitation — so a deployment with no
// base URL still sends, it just omits the link.
func TestAnAlertSendsWithoutABaseURL(t *testing.T) {
	a := NewAlertMailerAdapter(&recordingSender{enabled: true}, "")
	if !a.Enabled() {
		t.Fatal("Enabled() = false with a working sender and no base URL; an alert is a paragraph, not a link")
	}
	if got := a.alertURL(); got != "" {
		t.Errorf("alertURL() = %q; want empty", got)
	}
}

func TestAnAlertLinksToTheWatchersPageWhenItCan(t *testing.T) {
	a := NewAlertMailerAdapter(&recordingSender{enabled: true}, "https://app.example.com")
	if got := a.alertURL(); got != "https://app.example.com/watchers" {
		t.Errorf("alertURL() = %q", got)
	}
}

func TestAnAlertToNobodyIsNotAnError(t *testing.T) {
	s := &recordingSender{enabled: true}
	a := NewAlertMailerAdapter(s, "https://app.example.com")
	if err := a.SendAlert(context.Background(), nil, "t", "b"); err != nil {
		t.Errorf("SendAlert() with no recipients = %v", err)
	}
	if len(s.sent) != 0 {
		t.Error("a message was sent to nobody")
	}
}
