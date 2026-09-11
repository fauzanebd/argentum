package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/email"
)

// InviteMailerAdapter turns TeamService's InviteMail into an actual message
// (T-F6).
//
// It exists because the three things an invitation needs are owned by three
// different places: TeamService has the token, the company repository has the
// name, and config has the base URL. Putting the assembly here keeps
// TeamService free of both — it still knows nothing about email beyond the fact
// that it can fail.
type InviteMailerAdapter struct {
	sender    email.Sender
	companies CompanyNameLookup
	users     InviterLookup
	baseURL   string
}

// CompanyNameLookup is the one question this adapter asks of the company
// repository. domain.CompanyRepository satisfies it.
type CompanyNameLookup interface {
	GetByID(ctx context.Context, id string) (*domain.Company, error)
}

// InviterLookup resolves who sent the invitation, so the email can say. Its
// absence is survivable — the message falls back to naming only the company —
// which is why it is a separate interface from the one above rather than a
// second method on it.
type InviterLookup interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// NewInviteMailerAdapter wires it. A blank baseURL disables sending entirely:
// see Enabled.
func NewInviteMailerAdapter(sender email.Sender, companies CompanyNameLookup, users InviterLookup, baseURL string) *InviteMailerAdapter {
	return &InviteMailerAdapter{sender: sender, companies: companies, users: users, baseURL: baseURL}
}

// Enabled reports whether an invitation can actually be sent.
//
// **It requires the base URL as well as the relay**, and that is the decision
// worth stating: an invite email whose entire purpose is one link, carrying a
// link to nowhere, is worse than no email — the recipient concludes the product
// is broken rather than that their admin has not finished setting it up. So a
// deployment with SMTP and no APP_BASE_URL sends nothing and the admin keeps
// copying links, which is the behaviour they already have.
func (a *InviteMailerAdapter) Enabled() bool {
	return a != nil && a.sender != nil && a.sender.Enabled() && a.baseURL != ""
}

// SendInvite composes and sends.
func (a *InviteMailerAdapter) SendInvite(ctx context.Context, to string, d InviteMail) error {
	if !a.Enabled() {
		return email.ErrDisabled
	}
	msg := email.Invite(email.InviteData{
		CompanyName: a.companyName(ctx, d.CompanyID),
		InviterName: a.inviterName(ctx, d.InvitedBy),
		AcceptURL:   a.acceptURL(d.Token),
		ExpiresIn:   humanDays(d.ExpiresIn),
	})
	msg.To = []string{to}
	return a.sender.Send(ctx, msg)
}

// acceptURL mirrors what the dashboard builds from window.location.origin
// (team-tab.tsx). The two have to agree, and the route is the dashboard's — so
// a change there is a change here, which is why the path is a constant with
// this comment on it rather than a string in the middle of a function.
func (a *InviteMailerAdapter) acceptURL(token string) string {
	return fmt.Sprintf("%s/accept-invite?token=%s", a.baseURL, url.QueryEscape(token))
}

// companyName is best-effort. A lookup that fails leaves it empty and the
// template falls back to "your team's workspace" — an invitation that arrives
// slightly less personal is better than one that does not arrive because a
// database was briefly busy.
func (a *InviteMailerAdapter) companyName(ctx context.Context, id string) string {
	if a.companies == nil || id == "" {
		return ""
	}
	c, err := a.companies.GetByID(ctx, id)
	if err != nil || c == nil {
		return ""
	}
	return c.Name
}

// inviterName is best-effort for the same reason, and returns the inviter's
// email — which is what this product knows people by, and what makes the
// recipient recognise the request.
func (a *InviteMailerAdapter) inviterName(ctx context.Context, id string) string {
	if a.users == nil || id == "" {
		return ""
	}
	u, err := a.users.GetByID(ctx, id)
	if err != nil || u == nil {
		return ""
	}
	return u.Email
}

// humanDays renders the TTL the way the email says it: "7 days", not a date.
// A date would be worse — the reader is in a timezone this process does not
// know, so "expires 18 September" is ambiguous by a day where "in 7 days" is
// not.
func humanDays(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 2:
		return fmt.Sprintf("%d days", days)
	case days == 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
}

// ErrNoMailer is what a caller gets if it somehow reaches a nil adapter.
var ErrNoMailer = errors.New("no invite mailer configured")

// AlertMailerAdapter delivers a watcher breach to an inbox (T-F7).
//
// Separate from the invite adapter, and the difference is the base URL: an
// invitation *is* a link and is not worth sending without one, whereas an alert
// is a paragraph the agent wrote that stands on its own. So this one sends with
// or without APP_BASE_URL and simply omits the "open in Argentum" line.
type AlertMailerAdapter struct {
	sender  email.Sender
	baseURL string
}

func NewAlertMailerAdapter(sender email.Sender, baseURL string) *AlertMailerAdapter {
	return &AlertMailerAdapter{sender: sender, baseURL: baseURL}
}

func (a *AlertMailerAdapter) Enabled() bool {
	return a != nil && a.sender != nil && a.sender.Enabled()
}

// SendAlert sends one message to every recipient rather than one message each.
// The recipients are colleagues watching the same number, and N separate emails
// about one breach is how a team mutes a watcher.
func (a *AlertMailerAdapter) SendAlert(ctx context.Context, to []string, title, body string) error {
	if !a.Enabled() {
		return email.ErrDisabled
	}
	if len(to) == 0 {
		return nil
	}
	m := email.Alert(email.AlertData{Title: title, Body: body, URL: a.alertURL()})
	m.To = to
	return a.sender.Send(ctx, m)
}

// alertURL points at the watchers page rather than at the specific event: the
// event id is not in scope here, and a link to the list is one click from the
// right place — where a link to a route that may not exist is a dead end in the
// one message a tenant reads at 03:00.
func (a *AlertMailerAdapter) alertURL() string {
	if a.baseURL == "" {
		return ""
	}
	return a.baseURL + "/watchers"
}
