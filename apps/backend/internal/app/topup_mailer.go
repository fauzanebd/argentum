package app

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/email"
)

// TopupMailer tells the operators a proof of payment is waiting (RIZ-83 R14).
//
// Without it a paying customer stays refused until the owner happens to open
// the queue. It is the invite mailer's shape — the sender, a base URL, and
// nothing sent without both — for the invite mailer's reason: the message
// exists to carry one link, and a link to nowhere is worse than no message.
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
