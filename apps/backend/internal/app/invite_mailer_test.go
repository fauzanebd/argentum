package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/email"
)

type recordingSender struct {
	sent    []email.Message
	err     error
	enabled bool
}

func (r *recordingSender) Send(_ context.Context, m email.Message) error {
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, m)
	return nil
}
func (r *recordingSender) Enabled() bool { return r.enabled }

type fakeCompanyName struct {
	name string
	err  error
}

func (f fakeCompanyName) GetByID(context.Context, string) (*domain.Company, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.Company{Name: f.name}, nil
}

type fakeInviter struct {
	emailAddr string
	err       error
}

func (f fakeInviter) GetByID(context.Context, string) (*domain.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.User{Email: f.emailAddr}, nil
}

func adapter(s email.Sender, base string) *InviteMailerAdapter {
	return NewInviteMailerAdapter(s, fakeCompanyName{name: "Gelael"}, fakeInviter{emailAddr: "sari@gelael.co.id"}, base)
}

// The decision this ticket turns on: SMTP without a base URL sends nothing.
// An invite whose entire purpose is one link, carrying a link to nowhere, makes
// the recipient conclude the product is broken.
func TestAMailerWithNoBaseURLIsNotEnabled(t *testing.T) {
	s := &recordingSender{enabled: true}
	a := adapter(s, "")
	if a.Enabled() {
		t.Fatal("Enabled() = true with no APP_BASE_URL; the email would carry a link to nowhere")
	}
	if err := a.SendInvite(context.Background(), "b@example.com", InviteMail{Token: "tok"}); !errors.Is(err, email.ErrDisabled) {
		t.Errorf("SendInvite() = %v; want ErrDisabled", err)
	}
	if len(s.sent) != 0 {
		t.Error("a message was sent with no base URL to link to")
	}
}

func TestAMailerWithNoRelayIsNotEnabled(t *testing.T) {
	a := adapter(&recordingSender{enabled: false}, "https://app.example.com")
	if a.Enabled() {
		t.Error("Enabled() = true with a disabled sender")
	}
}

func TestTheLinkMatchesWhatTheDashboardBuilds(t *testing.T) {
	s := &recordingSender{enabled: true}
	a := adapter(s, "https://app.example.com")
	if err := a.SendInvite(context.Background(), "new@gelael.co.id", InviteMail{
		CompanyID: "co-1", InvitedBy: "u-1", Token: "tok/en+with=chars", ExpiresIn: 7 * 24 * time.Hour,
	}); err != nil {
		t.Fatalf("SendInvite() = %v", err)
	}
	if len(s.sent) != 1 {
		t.Fatalf("sent %d messages; want 1", len(s.sent))
	}
	m := s.sent[0]
	// The dashboard builds `${origin}/accept-invite?token=${encodeURIComponent(token)}`
	// (team-tab.tsx). These two have to agree or half the invitations in a
	// workspace land on a different route from the other half.
	if !strings.Contains(m.Text, "https://app.example.com/accept-invite?token=") {
		t.Errorf("link does not match the dashboard's shape:\n%s", m.Text)
	}
	// And the token has to survive the URL. A raw `+` in a query value reads as
	// a space, which would turn a valid single-use token into a 404 that looks
	// like an expired invite.
	if strings.Contains(m.Text, "tok/en+with=chars") {
		t.Errorf("the token reached the URL unescaped:\n%s", m.Text)
	}
	if len(m.To) != 1 || m.To[0] != "new@gelael.co.id" {
		t.Errorf("To = %v", m.To)
	}
}

// A busy database must not stop an invitation. Both lookups are decoration.
func TestAFailedLookupStillSendsTheInvitation(t *testing.T) {
	s := &recordingSender{enabled: true}
	a := NewInviteMailerAdapter(s,
		fakeCompanyName{err: errors.New("db down")},
		fakeInviter{err: errors.New("db down")},
		"https://app.example.com")
	if err := a.SendInvite(context.Background(), "b@example.com", InviteMail{Token: "t", ExpiresIn: 7 * 24 * time.Hour}); err != nil {
		t.Fatalf("SendInvite() = %v; a failed name lookup must not stop the invitation", err)
	}
	if len(s.sent) != 1 {
		t.Fatal("nothing was sent")
	}
	if !strings.Contains(s.sent[0].Text, "your team's workspace") {
		t.Errorf("no fallback for the company name:\n%s", s.sent[0].Text)
	}
}

func TestHumanDaysReadsLikeTheSentenceItIsIn(t *testing.T) {
	cases := map[time.Duration]string{
		7 * 24 * time.Hour: "7 days",
		48 * time.Hour:     "2 days",
		24 * time.Hour:     "1 day",
		6 * time.Hour:      "6 hours",
	}
	for d, want := range cases {
		if got := humanDays(d); got != want {
			t.Errorf("humanDays(%s) = %q; want %q", d, got, want)
		}
	}
}
