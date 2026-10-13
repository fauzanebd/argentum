package domain

import (
	"context"
	"time"
)

// Free credit by approval (RIZ-89): a new workspace's free credit is a request
// an operator approves, not a balance sign-up writes.
//
// Sign-up had no email verification, CAPTCHA or domain check, so a script
// could farm $0.50 per sign-up. Now sign-up files a CreditGrantRequest, a
// budget check refuses a company that has no grant until one is approved, and
// approval is the only thing that writes free credit.

// CreditGrantStatus is where a free-credit request is. There is no way back
// from approved or rejected in v1: a rejected workspace can still top up.
type CreditGrantStatus string

const (
	CreditGrantStatusPending  CreditGrantStatus = "pending"
	CreditGrantStatusApproved CreditGrantStatus = "approved"
	CreditGrantStatusRejected CreditGrantStatus = "rejected"
)

// CreditGrantRequest is one company's free-credit request. There is at most
// one per company, ever.
type CreditGrantRequest struct {
	ID        string            `json:"id"`
	CompanyID string            `json:"company_id"`
	Status    CreditGrantStatus `json:"status"`
	// AmountMicroUSD is CREDITS_DEFAULT_GRANT_USD when the request was filed,
	// frozen: the operator approves what the user was told is waiting.
	AmountMicroUSD int64      `json:"amount_micro_usd"`
	CreatedAt      time.Time  `json:"created_at"`
	ReviewedAt     *time.Time `json:"reviewed_at"`

	// Server-side only. The sign-up IP in particular never leaves the
	// server: the operator sees how many other sign-ups shared it, which is
	// the signal, and not the address, which is personal data.
	RequestedBy string `json:"-"`
	SignupIP    string `json:"-"`
	ReviewedBy  string `json:"-"`
}

// OperatorFreeCredit is a request as the operator reviews it: across every
// workspace, with the two signals that tell a real business from a farmed
// sign-up.
type OperatorFreeCredit struct {
	// Embedded for OperatorTopup's reason: the fields sit at the top level of
	// the JSON, and the TypeScript says `extends`.
	CreditGrantRequest `tstype:",extends"`
	CompanyName        string `json:"company_name"`
	// RequestedByEmail is the sign-up admin's email; empty for a request a
	// budget check filed, and for an admin since deleted.
	RequestedByEmail string `json:"requested_by_email"`
	ReviewedByEmail  string `json:"reviewed_by_email"`
	// HasDataSource is whether the workspace has connected a database:
	// the clearest sign of a real business.
	HasDataSource bool `json:"has_data_source"`
	// SameIPSignups7d counts the *other* sign-ups whose IP matched this one's
	// within seven days either side of it: the clearest sign of farming. Zero
	// for a request with no IP.
	SameIPSignups7d int `json:"same_ip_signups_7d"`
}

// CreditGrantApproval is what an approval did to the balance.
type CreditGrantApproval struct {
	Request *CreditGrantRequest
	// AddedMicroUSD is zero when the company already held free credit — a
	// grant written while CREDITS_GRANT_REQUIRES_APPROVAL was off, or by an
	// older pod's lazy grant during a rolling deploy. Approving then marks
	// the request approved and adds nothing.
	AddedMicroUSD   int64
	BalanceMicroUSD int64
}

// CreditGrantRequestRepository persists free-credit requests.
//
// Two kinds of method, named like TopupRepository's. InsertIfAbsent and
// GetForCompany are a tenant's and scope by company. The operator's —
// ListPending, ListReviewed, Approve, Reject — are deliberately cross-tenant:
// an operator reviews every workspace, and OperatorOnly stands in front of
// them.
type CreditGrantRequestRepository interface {
	// InsertIfAbsent files r as pending unless the company already has a
	// request, in which case it is a silent no-op. It reports whether it
	// inserted, which is what decides whether the operators are emailed.
	InsertIfAbsent(ctx context.Context, r *CreditGrantRequest) (bool, error)
	// GetForCompany returns the company's request, or ErrNotFound.
	GetForCompany(ctx context.Context, companyID string) (*CreditGrantRequest, error)

	// ListPending is every pending request, oldest first.
	ListPending(ctx context.Context) ([]*OperatorFreeCredit, error)
	// ListReviewed is the last decisions, newest first.
	ListReviewed(ctx context.Context, limit int) ([]*OperatorFreeCredit, error)
	// Approve marks a pending request approved and writes the free credit in
	// one transaction (RIZ-89 R8). ErrCreditGrantAlreadyReviewed when it is
	// not pending; ErrNotFound when the id does not exist.
	Approve(ctx context.Context, id, reviewerID string) (*CreditGrantApproval, error)
	// Reject marks a pending request rejected and touches no balance, under
	// the same guard as Approve.
	Reject(ctx context.Context, id, reviewerID string) (*CreditGrantRequest, error)
}
