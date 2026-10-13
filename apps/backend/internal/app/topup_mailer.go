package app

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/email"
)

// TopupMailer tells the operators a proof of payment is waiting (RIZ-83 R14).
//
// Without it a paying customer stays refused until the owner happens to open
// the queue. It is the invite mailer's shape — the sender, a base URL, and
// nothing sent without both — for the invite mailer's reason: the message
// exists to carry one link, and a link to nowhere is worse than no message.
//
// Since RIZ-89 it also sends the two free-credit emails — the operators' alert
// about a sign-up, and the admin's "your free credit is ready" — because they
// are the same SMTP, the same base URL and the same operator list, and a
// second mailer would be a second place to configure them.
type TopupMailer struct {
	sender    email.Sender
	operators []string
	baseURL   string
}

// NewTopupMailer wires it. Unset operators, sender or base URL disable it.
func NewTopupMailer(sender email.Sender, operators []string, baseURL string) *TopupMailer {
	return &TopupMailer{sender: sender, operators: operators, baseURL: baseURL}
}

// Enabled reports whether an alert can actually be sent.
func (m *TopupMailer) Enabled() bool {
	return m != nil && m.sender != nil && m.sender.Enabled() && m.baseURL != "" && len(m.operators) > 0
}

// TopupAlert is what one alert says.
type TopupAlert struct {
	TopupID           string
	CompanyName       string
	RequesterEmail    string
	ExpectedAmountIDR int64
	PayeeLabel        string
	CreditMicroUSD    int64
}

// reviewURL is the operator page's route in the dashboard
// (routes/index.tsx). The two have to agree, which is why it is spelled once.
func (m *TopupMailer) reviewURL() string { return m.baseURL + "/operator/top-ups" }

// NotifyProof sends one message to each operator. One each rather than one to
// all: a bad address on the list must not stop the others' copy, and the
// operators are not a team that needs to see each other on a thread.
//
// Best-effort. A failure is logged with the top-up id and nothing else, and
// never reaches the upload that triggered it. Not even the error: an SMTP
// refusal quotes the address it refused ("550 5.1.1 <…>: user unknown"), and
// the operator list is not something to scatter through a log. The relay's own
// log has the reason.
func (m *TopupMailer) NotifyProof(ctx context.Context, a TopupAlert) {
	if !m.Enabled() {
		return
	}
	msg := email.TopupReview(email.TopupReviewData{
		CompanyName:    a.CompanyName,
		RequesterEmail: a.RequesterEmail,
		Amount:         FormatRupiah(a.ExpectedAmountIDR),
		PayeeLabel:     a.PayeeLabel,
		Credit:         FormatCreditUSD(a.CreditMicroUSD),
		ReviewURL:      m.reviewURL(),
	})
	for _, to := range m.operators {
		one := msg
		one.To = []string{to}
		if err := m.sender.Send(ctx, one); err != nil {
			logrus.WithField("topup_id", a.TopupID).
				Warn("top-up review email failed; the operator page still lists it")
		}
	}
}

// FreeCreditAlert is what the operator alert about a sign-up says (RIZ-89).
type FreeCreditAlert struct {
	CompanyName    string
	RequesterEmail string
	SignedUpAt     time.Time
	AmountMicroUSD int64
}

// operatorZone is the zone the operator alert's "signed up on" is written in.
// The operators are the owner, in Indonesia, and the review page beside the
// link shows the same moment in their browser's time; a UTC time in the email
// would be seven hours away from the row it points at. Embedded tzdata
// (scheduled_task_service.go) makes the load succeed on a bare image; UTC is
// the fallback anyway.
var operatorZone = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.UTC
}()

// freeCreditReviewURL is the operator's free-credit page (routes/index.tsx),
// spelled once for reviewURL's reason.
func (m *TopupMailer) freeCreditReviewURL() string { return m.baseURL + "/operator/free-credit" }

// NotifyFreeCredit emails each operator about a sign-up waiting for its free
// credit (R18): one message each, best-effort, logged without addresses —
// NotifyProof's rules, for NotifyProof's reasons. Silent when SMTP, the base
// URL or the operator list is unset.
func (m *TopupMailer) NotifyFreeCredit(ctx context.Context, a FreeCreditAlert) {
	if !m.Enabled() {
		return
	}
	at := a.SignedUpAt
	if at.IsZero() {
		at = time.Now()
	}
	msg := email.FreeCreditReview(email.FreeCreditReviewData{
		CompanyName:    a.CompanyName,
		RequesterEmail: a.RequesterEmail,
		SignedUpAt:     at.In(operatorZone).Format("2 Jan 2006, 15:04"),
		Amount:         FormatCreditUSD(a.AmountMicroUSD),
		ReviewURL:      m.freeCreditReviewURL(),
	})
	for _, to := range m.operators {
		one := msg
		one.To = []string{to}
		if err := m.sender.Send(ctx, one); err != nil {
			logrus.Warn("free-credit review email failed; the operator page still lists the request")
		}
	}
}

// FreeCreditReady is what the approval email says (RIZ-89 R19).
type FreeCreditReady struct {
	CompanyID     string
	CompanyName   string
	To            string
	AddedMicroUSD int64
}

// NotifyFreeCreditReady tells the admin who signed up that their free credit
// was added. It needs a sender and a base URL — the link is the point — but
// not the operator list, which it does not write to. Best-effort: a failure is
// a Warn with the company id, and the approval it follows has already
// happened.
func (m *TopupMailer) NotifyFreeCreditReady(ctx context.Context, r FreeCreditReady) {
	if m == nil || m.sender == nil || !m.sender.Enabled() || m.baseURL == "" || r.To == "" {
		return
	}
	msg := email.FreeCreditReady(email.FreeCreditReadyData{
		CompanyName: r.CompanyName,
		Amount:      FormatCreditUSD(r.AddedMicroUSD),
		ChatURL:     m.baseURL + "/chat",
	})
	msg.To = []string{r.To}
	if err := m.sender.Send(ctx, msg); err != nil {
		logrus.WithField("company_id", r.CompanyID).
			Warn("free-credit approval email failed; the credit is added and the chat notice is gone")
	}
}
