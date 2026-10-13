package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// FreeCreditService is the operator's half of free credit by approval
// (RIZ-89): the queue of new workspaces waiting for their free credit, and the
// two decisions an operator can make about each.
//
// **Approving is the only thing that writes free credit.** Sign-up files a
// request (UsageService.StartSignupCredit), a budget check files one for a
// company that has none (UsageService.CheckBudget), and nothing else in the
// codebase may credit a company for free. The write itself, and the guard that
// makes it exactly-once, are CreditGrantRequestRepository.Approve's.
//
// It is TopupService's operator half in shape — the same allowlist in front
// of it, the same guarded transition, the same InvalidateBudget after — so an
// operator who has learnt one queue has learnt the other.
type FreeCreditService struct {
	repo   domain.CreditGrantRequestRepository
	budget BudgetInvalidator
	mailer FreeCreditReadyNotifier
	// The approval email's "who" and "to". Both optional.
	companies CompanyNameLookup
	users     InviterLookup

	async func(func())
}

// FreeCreditReadyNotifier sends the approval email. TopupMailer satisfies it.
type FreeCreditReadyNotifier interface {
	NotifyFreeCreditReady(ctx context.Context, r FreeCreditReady)
}

// freeCreditReviewedLimit is R7's "the last 50 decisions".
const freeCreditReviewedLimit = 50

func NewFreeCreditService(repo domain.CreditGrantRequestRepository) *FreeCreditService {
	return &FreeCreditService{repo: repo, async: func(f func()) { go f() }}
}

// WithBudget makes a decision drop the company's cached budget verdict, so an
// approval runs the company's next message rather than one a minute later, and
// a rejection says "not approved" on it rather than "waiting".
func (s *FreeCreditService) WithBudget(b BudgetInvalidator) *FreeCreditService {
	s.budget = b
	return s
}

// WithMailer turns on the approval email. companies and users fill in its
// company name and recipient; both may be nil, and without users there is
// nobody to send it to.
func (s *FreeCreditService) WithMailer(m FreeCreditReadyNotifier, companies CompanyNameLookup, users InviterLookup) *FreeCreditService {
	s.mailer = m
	s.companies = companies
	s.users = users
	return s
}

// withSyncMail is for tests: an email that has been sent by the time the
// approval returns.
func (s *FreeCreditService) withSyncMail() *FreeCreditService {
	s.async = func(f func()) { f() }
	return s
}

// OperatorList is the review queue (pending, oldest first) or the last
// decisions, across every workspace (R7).
func (s *FreeCreditService) OperatorList(ctx context.Context, reviewed bool) ([]*domain.OperatorFreeCredit, error) {
	if reviewed {
		return s.repo.ListReviewed(ctx, freeCreditReviewedLimit)
	}
	return s.repo.ListPending(ctx)
}

// Approve writes the request's free credit exactly once (R8), drops the
// company's cached verdict, and emails the admin who signed up when credit was
// actually added.
func (s *FreeCreditService) Approve(ctx context.Context, id, reviewerID string) (*domain.CreditGrantApproval, error) {
	if !validRequestID(id) {
		return nil, domain.ErrNotFound
	}
	res, err := s.repo.Approve(ctx, id, reviewerID)
	if err != nil {
		return nil, err
	}
	g := res.Request
	if s.budget != nil {
		s.budget.InvalidateBudget(ctx, g.CompanyID)
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  g.CompanyID,
		"request_id":  g.ID,
		"added":       res.AddedMicroUSD,
		"balance":     res.BalanceMicroUSD,
		"reviewed_by": reviewerID,
	}).Info("free credit approved")

	// Nothing added means the workspace already had its free credit, so
	// there is no news to send.
	if res.AddedMicroUSD > 0 && s.mailer != nil && g.RequestedBy != "" {
		ready := FreeCreditReady{
			CompanyID:     g.CompanyID,
			CompanyName:   s.companyName(ctx, g.CompanyID),
			To:            s.userEmail(ctx, g.RequestedBy),
			AddedMicroUSD: res.AddedMicroUSD,
		}
		// Off the request path: the operator is waiting on their click, not
		// on the customer's inbox, and the approval has already happened.
		actx := context.WithoutCancel(ctx)
		s.async(func() { s.mailer.NotifyFreeCreditReady(actx, ready) })
	}
	return res, nil
}

// Reject closes a pending request without touching the balance (R9). Final in
// v1, with no reason: the user is told one fixed sentence, and topping up
// still works.
func (s *FreeCreditService) Reject(ctx context.Context, id, reviewerID string) (*domain.CreditGrantRequest, error) {
	if !validRequestID(id) {
		return nil, domain.ErrNotFound
	}
	g, err := s.repo.Reject(ctx, id, reviewerID)
	if err != nil {
		return nil, err
	}
	// A pending refusal cached a minute ago would go on saying "waiting for
	// approval" after the operator said no.
	if s.budget != nil {
		s.budget.InvalidateBudget(ctx, g.CompanyID)
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  g.CompanyID,
		"request_id":  g.ID,
		"reviewed_by": reviewerID,
	}).Info("free credit rejected")
	return g, nil
}

// validID answers an id that cannot be a request as "no such request" before
// it reaches a uuid column, where Postgres would refuse it as a 500.
func validRequestID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

func (s *FreeCreditService) companyName(ctx context.Context, id string) string {
	if s.companies == nil {
		return ""
	}
	c, err := s.companies.GetByID(ctx, id)
	if err != nil || c == nil {
		return ""
	}
	return c.Name
}

func (s *FreeCreditService) userEmail(ctx context.Context, id string) string {
	if s.users == nil || id == "" {
		return ""
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil || u == nil {
		return ""
	}
	return u.Email
}
