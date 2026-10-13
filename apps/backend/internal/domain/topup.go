package domain

import (
	"context"
	"time"
)

// Manual credit top-up (RIZ-83): a workspace admin asks to add credit, pays
// the exact amount by bank transfer or GoPay, uploads proof, and an operator
// approves it against the statement.
//
// Three properties shape every type in this file:
//
//   - **Money is integers.** Rupiah in whole rupiah, credit in micro-USD (the
//     unit of every balance in the control plane), and a rate as rupiah per
//     dollar × 10 000 — the `_e4` suffix. 16 523,40 IDR per $1 is 165234000.
//   - **The credit is frozen at request time.** The rate it was computed from is
//     stored beside it, so the customer is credited what the page showed and the
//     operator can see why, whatever the market did in between.
//   - **A tenant never sees the rate.** Topup is what a workspace admin reads,
//     and its FX fields are `json:"-"`; OperatorTopup is the operator's view
//     and is the only shape that carries them.

// TopupStatus is where a top-up request is in its life.
type TopupStatus string

const (
	// TopupStatusAwaitingPayment is a request the customer has not proved
	// payment for yet. It expires at ExpiresAt and frees its code.
	TopupStatusAwaitingPayment TopupStatus = "awaiting_payment"
	// TopupStatusInReview has proof and waits for an operator. It never
	// expires: the customer may have paid.
	TopupStatusInReview  TopupStatus = "in_review"
	TopupStatusApproved  TopupStatus = "approved"
	TopupStatusRejected  TopupStatus = "rejected"
	TopupStatusExpired   TopupStatus = "expired"
	TopupStatusCancelled TopupStatus = "cancelled"
)

// Open reports whether the request still holds its matching code — the two
// statuses the open-row unique indexes in 090 cover.
func (s TopupStatus) Open() bool {
	return s == TopupStatusAwaitingPayment || s == TopupStatusInReview
}

// TopupFXSource says where a request's rate came from.
type TopupFXSource string

const (
	// TopupFXSourceMarket is the ECB reference rate plus the spread.
	TopupFXSourceMarket TopupFXSource = "market"
	// TopupFXSourceFallback is TOPUP_IDR_PER_USD, used as the effective rate
	// with no spread, because no market rate young enough was available.
	TopupFXSourceFallback TopupFXSource = "fallback"
)

// TopupFX is the rate a request was priced at, frozen when it was created.
type TopupFX struct {
	Source TopupFXSource
	// MarketIDRPerUSDE4 is nil on a fallback.
	MarketIDRPerUSDE4 *int64
	// RateDate is the ECB reference date, "2026-10-12". Empty on a fallback.
	RateDate string
	// SpreadBP is the spread in basis points (500 = 5%). Nil on a fallback.
	SpreadBP             *int
	EffectiveIDRPerUSDE4 int64
}

// Topup is one top-up request as the workspace that made it sees it. Nothing
// here is a storage key or a URL, and nothing is a rate: GET /api/topups
// answers with exactly these fields (R7, AC28).
type Topup struct {
	ID          string `json:"id"`
	CompanyID   string `json:"-"`
	RequestedBy string `json:"-"`
	// AmountIDR is what the customer chose, in whole thousands. The credit is
	// computed from this alone; the code adds nothing to it.
	AmountIDR  int64 `json:"amount_idr"`
	UniqueCode int   `json:"unique_code"`
	// ExpectedAmountIDR is AmountIDR + UniqueCode: the exact amount to
	// transfer, and what the operator looks for on the statement.
	ExpectedAmountIDR int64       `json:"expected_amount_idr"`
	CreditMicroUSD    int64       `json:"credit_micro_usd"`
	Status            TopupStatus `json:"status"`
	// PayeeID is the configured account the customer says they paid.
	PayeeID         *string    `json:"payee_id"`
	ProofUploadedAt *time.Time `json:"proof_uploaded_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	ReviewedAt      *time.Time `json:"reviewed_at"`
	// RejectReason is the operator's sentence to the customer.
	RejectReason *string `json:"reject_reason"`

	// Server-side only. FX is the operator's (OperatorTopup), and the proof
	// fields are how the operator's View proof finds the object — a tenant
	// never receives a key it could not use and should not hold.
	FX               TopupFX `json:"-"`
	ProofKey         string  `json:"-"`
	ProofContentType string  `json:"-"`
	ProofSHA256      string  `json:"-"`
	ReviewedBy       string  `json:"-"`
}

// TopupDuplicate names an earlier top-up whose proof is byte-identical to
// this one's (R15). A flag for the operator, never a block: the same receipt
// uploaded twice is suspicious, not proof of anything.
type TopupDuplicate struct {
	CompanyName       string    `json:"company_name"`
	ExpectedAmountIDR int64     `json:"expected_amount_idr"`
	CreatedAt         time.Time `json:"created_at"`
}

// OperatorTopup is a top-up as the operator reviews it: across every
// workspace, with who asked, which account, and the frozen rate.
type OperatorTopup struct {
	// Embedded, so its fields sit at the top level of the JSON beside the
	// operator's; the tstype tag makes the TypeScript say the same thing
	// (`extends Topup`) rather than a nested object that is never sent.
	Topup            `tstype:",extends"`
	CompanyName      string  `json:"company_name"`
	RequestedByEmail string  `json:"requested_by_email"`
	ReviewedByEmail  *string `json:"reviewed_by_email"`
	// PayeeLabel is the configured label for PayeeID ("GoPay" for "gopay"),
	// resolved from TOPUP_PAYEES when the list is read; the id itself when that
	// payee has since been removed from config.
	PayeeLabel string `json:"payee_label"`
	// The frozen rate, flattened for the table's Rate column.
	FXSource               TopupFXSource   `json:"fx_source"`
	FXMarketIDRPerUSDE4    *int64          `json:"fx_market_idr_per_usd_e4"`
	FXRateDate             *string         `json:"fx_rate_date"`
	FXSpreadBP             *int            `json:"fx_spread_bp"`
	FXEffectiveIDRPerUSDE4 int64           `json:"fx_effective_idr_per_usd_e4"`
	DuplicateOf            *TopupDuplicate `json:"duplicate_of"`
}

// TopupPayee is one account a customer may pay into. Deploy config
// (TOPUP_PAYEES), never a row and never in git: the owner asked for the
// numbers to stay out of the repository.
type TopupPayee struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

// TopupProof is what an upload records on the request.
type TopupProof struct {
	Key         string
	ContentType string
	SHA256      string
	PayeeID     string
}

// TopupRepository persists top-up requests.
//
// Two kinds of method, and the names say which. The tenant's take a company
// id and scope every statement by it. The operator's — ListForReview,
// ListReviewed, GetAnyCompany, Approve, Reject — are deliberately
// cross-tenant: an operator reviews every workspace's payments, and the
// OperatorOnly middleware is what stands in front of them.
type TopupRepository interface {
	// Create inserts an awaiting_payment request. ErrTopupInProgress when the
	// company already has an open one; ErrTopupCodeTaken when another open
	// request uses the same expected amount.
	Create(ctx context.Context, t *Topup) error
	GetForCompany(ctx context.Context, companyID, id string) (*Topup, error)
	ListForCompany(ctx context.Context, companyID string, limit int) ([]*Topup, error)
	// AttachProof records a proof on an open, unexpired request of this
	// company and moves it to in_review. ErrNotFound for another company's id;
	// ErrTopupClosed for a request that is no longer open.
	AttachProof(ctx context.Context, companyID, id string, p TopupProof) (*Topup, error)
	// Cancel moves awaiting_payment to cancelled; ErrTopupNotCancellable for
	// any other status.
	Cancel(ctx context.Context, companyID, id string) (*Topup, error)
	// ExpireStale marks every awaiting_payment request past its expiry as
	// expired, across all companies. Lazy expiry: there is no cron.
	ExpireStale(ctx context.Context) (int64, error)

	ListForReview(ctx context.Context) ([]*OperatorTopup, error)
	ListReviewed(ctx context.Context, limit int) ([]*OperatorTopup, error)
	GetAnyCompany(ctx context.Context, id string) (*Topup, error)
	// Approve marks an in_review request approved and adds its credit to the
	// company's balance in one transaction, returning the new balance.
	// ErrTopupAlreadyReviewed when it is not in review; ErrNotFound when the
	// id does not exist.
	Approve(ctx context.Context, id, reviewerID string) (*Topup, int64, error)
	Reject(ctx context.Context, id, reviewerID, reason string) (*Topup, error)
}
