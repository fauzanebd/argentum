package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// TopupService is the manual credit top-up (RIZ-83): a workspace admin asks to
// add credit, transfers the exact amount, uploads proof, and an operator
// approves it against the bank statement.
//
// **The screenshot is evidence, not proof.** A receipt can be edited; the
// amount on the statement cannot. That is why every request carries a 3-digit
// code that makes its amount unique among open requests, why the operator page
// says to match on the amount, and why a byte-identical proof is flagged
// rather than trusted.
//
// **The server decides every number.** The customer sends an amount and
// nothing else is read from the request: the credit is computed here, from a
// rate this service fetched, and frozen on the row.
type TopupService struct {
	repo     domain.TopupRepository
	rates    *TopupRates
	settings TopupSettings
	// store is nil on a deployment with no object storage: requests can still
	// be made, and proof uploads answer 503.
	store  TopupProofStore
	budget BudgetInvalidator
	mailer *TopupMailer
	// The alert's context, looked up after an upload. Both optional.
	companies CompanyNameLookup
	users     InviterLookup

	code  func() int
	async func(func())
}

// TopupSettings is what config decides about top-ups.
type TopupSettings struct {
	MinIDR       int64
	MaxIDR       int64
	PresetsIDR   []int64
	Payees       []domain.TopupPayee
	ExpiresAfter time.Duration
}

// Enabled reports whether a request can be made: payees are configured. It is
// the kill switch — unset TOPUP_PAYEES and new requests stop, while requests
// already in review can still be decided.
func (s TopupSettings) Enabled() bool { return len(s.Payees) > 0 }

// TopupProofStore is the half of storage.StorageService this service needs,
// declared at the consumer like DocumentBlobStore.
type TopupProofStore interface {
	UploadKey(ctx context.Context, key string, r io.Reader, contentType string) (string, error)
	RemoveKey(ctx context.Context, key string) error
	PresignKey(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// BudgetInvalidator drops a company's cached budget verdict. UsageService
// satisfies it.
type BudgetInvalidator interface {
	InvalidateBudget(ctx context.Context, companyID string)
}

// The service's refusals. The handler owns the sentences; these only say
// which one.
var (
	// ErrTopupUnavailable: top-up is off, or no free matching code could be
	// drawn. 503.
	ErrTopupUnavailable = errors.New("top-up is not available")
	// ErrTopupProofUnavailable: no object storage, or it failed. 503.
	ErrTopupProofUnavailable = errors.New("proof upload is not available")
	// ErrTopupProofTooLarge: over TopupProofMaxBytes. 413.
	ErrTopupProofTooLarge = errors.New("proof file is too large")
	// ErrTopupProofType: not a PNG, JPEG, WebP or PDF by its bytes. 415.
	ErrTopupProofType = errors.New("proof file type is not accepted")
	// ErrTopupUnknownPayee: the payee id is not a configured account. 400.
	ErrTopupUnknownPayee = errors.New("unknown payee")
	// ErrTopupReasonRequired: a rejection with no reason. 400.
	ErrTopupReasonRequired = errors.New("a rejection needs a reason")
	// ErrTopupReasonTooLong: over TopupRejectReasonMax characters. 400.
	ErrTopupReasonTooLong = errors.New("the rejection reason is too long")
)

// TopupAmountError is a refused amount with the sentence the customer sees.
// It wraps ErrInvalidInput so a caller that only knows the domain errors still
// answers 400.
type TopupAmountError struct{ Message string }

func (e *TopupAmountError) Error() string { return e.Message }
func (e *TopupAmountError) Unwrap() error { return domain.ErrInvalidInput }

const (
	// TopupProofMaxBytes is the proof size cap. A phone screenshot is a few
	// hundred KB and a bank's PDF statement less than that.
	TopupProofMaxBytes = 5 << 20
	// TopupRejectReasonMax bounds the customer-facing sentence.
	TopupRejectReasonMax = 300
	// topupCodeTries bounds the code draw. With at most a few dozen open
	// requests among 999 codes, twenty misses in a row is a broken random
	// source or a table full of stale rows, not bad luck.
	topupCodeTries = 20
	// topupListLimit and topupReviewedLimit are R7's and R10's page sizes.
	topupListLimit     = 20
	topupReviewedLimit = 50
	// TopupProofLinkTTL is how long View proof's presigned URL works.
	TopupProofLinkTTL = 5 * time.Minute
)

// topupProofTypes are the content types a proof may be, by its bytes, and the
// extension its object is stored under.
var topupProofTypes = map[string]string{
	"image/png":       "png",
	"image/jpeg":      "jpg",
	"image/webp":      "webp",
	"application/pdf": "pdf",
}

// NewTopupService wires the required half. ExpiresAfter <= 0 falls back to 48h.
func NewTopupService(repo domain.TopupRepository, rates *TopupRates, settings TopupSettings) *TopupService {
	if settings.ExpiresAfter <= 0 {
		settings.ExpiresAfter = 48 * time.Hour
	}
	return &TopupService{
		repo:     repo,
		rates:    rates,
		settings: settings,
		code:     randomTopupCode,
		async:    func(f func()) { go f() },
	}
}

// WithProofStore enables proof uploads. Nil leaves them answering 503.
func (s *TopupService) WithProofStore(store TopupProofStore) *TopupService {
	s.store = store
	return s
}

// WithBudget makes an approval drop the company's cached budget verdict.
func (s *TopupService) WithBudget(b BudgetInvalidator) *TopupService {
	s.budget = b
	return s
}

// WithMailer turns on the operator alert. companies and users fill in its
// "who"; both may be nil.
func (s *TopupService) WithMailer(m *TopupMailer, companies CompanyNameLookup, users InviterLookup) *TopupService {
	s.mailer = m
	s.companies = companies
	s.users = users
	return s
}

// withCodeSource and withSyncAlerts are for tests: a code sequence that
// collides on purpose, and an alert that has been sent by the time the upload
// returns.
func (s *TopupService) withCodeSource(f func() int) *TopupService {
	s.code = f
	return s
}

func (s *TopupService) withSyncAlerts() *TopupService {
	s.async = func(f func()) { f() }
	return s
}

func randomTopupCode() int {
	n, err := rand.Int(rand.Reader, big.NewInt(999))
	if err != nil {
		// crypto/rand does not fail on any platform this runs on; if it
		// did, a fixed code still works — the unique index makes the retry
		// loop find a free one or refuse.
		return int(time.Now().UnixNano()%999) + 1
	}
	return int(n.Int64()) + 1
}

// TopupOptions is the choose screen's data (R3). The handler answers it as
// TopupOptionsResponse; the shape lives there because that file is what
// packages/api-types is generated from.
type TopupOptions struct {
	Enabled            bool
	MinIDR             int64
	MaxIDR             int64
	StepIDR            int64
	PresetsIDR         []int64
	PreviewIDRPerUSDE4 int64
	Payees             []domain.TopupPayee
	ExpiresAfterHours  int
}

// Options answers GET /api/topups/options. The preview rate is the effective
// rate a request made now would freeze — never the market rate or the spread,
// which a tenant does not see (AC28). Off, it fetches nothing.
func (s *TopupService) Options(ctx context.Context) TopupOptions {
	out := TopupOptions{
		Enabled:           s.settings.Enabled(),
		MinIDR:            s.settings.MinIDR,
		MaxIDR:            s.settings.MaxIDR,
		StepIDR:           1000,
		PresetsIDR:        append([]int64{}, s.settings.PresetsIDR...),
		Payees:            []domain.TopupPayee{},
		ExpiresAfterHours: int(s.settings.ExpiresAfter.Hours()),
	}
	if !out.Enabled {
		return out
	}
	out.Payees = append(out.Payees, s.settings.Payees...)
	out.PreviewIDRPerUSDE4 = s.rates.Current(ctx).FX.EffectiveIDRPerUSDE4
	return out
}

// jsonInteger is a JSON number with no fraction and no exponent.
var jsonInteger = regexp.MustCompile(`^-?[0-9]+$`)

// ParseTopupAmount reads the request's amount_idr. A string, a fraction or a
// missing field is the whole-thousands sentence (AC22): the field accepts an
// integer number of rupiah and nothing else, and that is what the sentence
// asks for.
func (s *TopupService) ParseTopupAmount(raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if !jsonInteger.MatchString(text) {
		return 0, s.wholeThousands()
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		// Too many digits for int64 is above any maximum.
		if strings.HasPrefix(text, "-") {
			return 0, s.belowMin()
		}
		return 0, s.aboveMax()
	}
	return n, nil
}

func (s *TopupService) wholeThousands() error {
	return &TopupAmountError{Message: "Enter an amount in whole thousands of rupiah."}
}

func (s *TopupService) belowMin() error {
	return &TopupAmountError{Message: fmt.Sprintf("The minimum top-up is Rp %s.", FormatRupiah(s.settings.MinIDR))}
}

func (s *TopupService) aboveMax() error {
	return &TopupAmountError{Message: fmt.Sprintf("The maximum top-up is Rp %s.", FormatRupiah(s.settings.MaxIDR))}
}

// validateAmount checks the range before the step, so 19.500 reads "the
// minimum is Rp 20.000" — the more useful of the two sentences for it.
func (s *TopupService) validateAmount(amount int64) error {
	switch {
	case amount < s.settings.MinIDR:
		return s.belowMin()
	case amount > s.settings.MaxIDR:
		return s.aboveMax()
	case amount%1000 != 0:
		return s.wholeThousands()
	}
	return nil
}

// Create makes an awaiting_payment request (R4).
func (s *TopupService) Create(ctx context.Context, companyID, userID string, amountIDR int64) (*domain.Topup, error) {
	if !s.settings.Enabled() {
		return nil, ErrTopupUnavailable
	}
	if err := s.validateAmount(amountIDR); err != nil {
		return nil, err
	}
	s.expireStale(ctx)

	rate := s.rates.Current(ctx)
	credit := TopupCreditMicroUSD(amountIDR, rate.FX.EffectiveIDRPerUSDE4)
	if credit <= 0 {
		return nil, fmt.Errorf("top-up of Rp %d prices to no credit at %d", amountIDR, rate.FX.EffectiveIDRPerUSDE4)
	}
	expires := time.Now().Add(s.settings.ExpiresAfter)

	for try := 0; try < topupCodeTries; try++ {
		code := s.code()
		t := &domain.Topup{
			CompanyID:         companyID,
			RequestedBy:       userID,
			AmountIDR:         amountIDR,
			UniqueCode:        code,
			ExpectedAmountIDR: amountIDR + int64(code),
			CreditMicroUSD:    credit,
			FX:                rate.FX,
			ExpiresAt:         expires,
		}
		err := s.repo.Create(ctx, t)
		if errors.Is(err, domain.ErrTopupCodeTaken) {
			continue
		}
		if err != nil {
			return nil, err
		}
		logrus.WithFields(logrus.Fields{
			"company_id": companyID,
			"topup_id":   t.ID,
			"amount_idr": amountIDR,
			"credit":     credit,
			"fx_source":  rate.FX.Source,
		}).Info("top-up requested")
		return t, nil
	}
	logrus.WithField("company_id", companyID).
		Warn("no free top-up matching code after twenty draws; refusing the request")
	return nil, ErrTopupUnavailable
}

// List is the company's recent top-ups, newest first (R7).
func (s *TopupService) List(ctx context.Context, companyID string) ([]*domain.Topup, error) {
	s.expireStale(ctx)
	return s.repo.ListForCompany(ctx, companyID, topupListLimit)
}

// Cancel withdraws a request that is still waiting for payment (R6).
func (s *TopupService) Cancel(ctx context.Context, companyID, id string) (*domain.Topup, error) {
	return s.repo.Cancel(ctx, companyID, id)
}

// expireStale is R8's lazy expiry. Its failure is not the caller's: a stale
// row the update missed is expired by the next request, and the guarded
// writes refuse it in the meantime.
func (s *TopupService) expireStale(ctx context.Context) {
	if n, err := s.repo.ExpireStale(ctx); err != nil {
		logrus.WithError(err).Warn("top-up expiry sweep failed; the next request retries it")
	} else if n > 0 {
		logrus.WithField("expired", n).Info("expired unpaid top-ups")
	}
}

// ProofInput is one proof upload.
type ProofInput struct {
	CompanyID string
	UserID    string
	TopupID   string
	PayeeID   string
	Body      io.Reader
}

// UploadProof stores a proof and moves the request to in_review (R5).
//
// Every refusal happens before a byte is stored — whose request, is it open,
// which account, how large, what type — so a refused upload leaves nothing in
// the bucket. After the store, a row that will not take the key removes the
// object it would have pointed at; a replaced proof's old object is kept,
// because it is evidence of what was sent first.
func (s *TopupService) UploadProof(ctx context.Context, in ProofInput) (*domain.Topup, error) {
	t, err := s.repo.GetForCompany(ctx, in.CompanyID, in.TopupID)
	if err != nil {
		return nil, err
	}
	if !proofAccepted(t, time.Now()) {
		return nil, domain.ErrTopupClosed
	}
	payee, ok := s.payee(in.PayeeID)
	if !ok {
		return nil, ErrTopupUnknownPayee
	}
	if in.Body == nil {
		return nil, ErrTopupProofType
	}
	// One byte over the cap, for DocumentIngestService.Upload's reason.
	data, err := io.ReadAll(io.LimitReader(in.Body, TopupProofMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read proof: %w", err)
	}
	if len(data) > TopupProofMaxBytes {
		return nil, ErrTopupProofTooLarge
	}
	// By the bytes, never the name or the declared type: both are the
	// caller's claim, and a text file renamed .png is still text.
	contentType := http.DetectContentType(data)
	ext, ok := topupProofTypes[contentType]
	if !ok || len(data) == 0 {
		return nil, ErrTopupProofType
	}
	if s.store == nil {
		return nil, ErrTopupProofUnavailable
	}

	sum := sha256.Sum256(data)
	key := fmt.Sprintf("topups/%s/%s/%s.%s", in.CompanyID, t.ID, uuid.NewString(), ext)
	if _, err := s.store.UploadKey(ctx, key, bytes.NewReader(data), contentType); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"company_id": in.CompanyID, "topup_id": t.ID}).
			Warn("top-up proof could not be stored")
		return nil, ErrTopupProofUnavailable
	}
	updated, err := s.repo.AttachProof(ctx, in.CompanyID, t.ID, domain.TopupProof{
		Key: key, ContentType: contentType, SHA256: hex.EncodeToString(sum[:]), PayeeID: payee.ID,
	})
	if err != nil {
		if rmErr := s.store.RemoveKey(ctx, key); rmErr != nil {
			logrus.WithError(rmErr).WithField("topup_id", t.ID).
				Warn("could not remove a proof whose row failed to write; it is orphaned")
		}
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": in.CompanyID,
		"topup_id":   t.ID,
		"bytes":      len(data),
		"type":       contentType,
	}).Info("top-up proof uploaded")

	if s.mailer.Enabled() {
		alert := TopupAlert{
			TopupID:           updated.ID,
			CompanyName:       s.companyName(ctx, in.CompanyID),
			RequesterEmail:    s.userEmail(ctx, in.UserID),
			ExpectedAmountIDR: updated.ExpectedAmountIDR,
			PayeeLabel:        payee.Label,
			CreditMicroUSD:    updated.CreditMicroUSD,
		}
		// Off the request path: SMTP can take its fifteen-second timeout, and
		// the customer is waiting on their upload, not on our inbox.
		actx := context.WithoutCancel(ctx)
		s.async(func() { s.mailer.NotifyProof(actx, alert) })
	}
	return updated, nil
}

// proofAccepted is AttachProof's guard, asked first so the common refusals
// cost no upload: waiting for payment and not yet expired, or in review (a
// replaced proof). The repository asks again in its WHERE clause, which is
// what holds under a race.
func proofAccepted(t *domain.Topup, now time.Time) bool {
	switch t.Status {
	case domain.TopupStatusInReview:
		return true
	case domain.TopupStatusAwaitingPayment:
		return now.Before(t.ExpiresAt)
	}
	return false
}

func (s *TopupService) payee(id string) (domain.TopupPayee, bool) {
	id = strings.TrimSpace(id)
	for _, p := range s.settings.Payees {
		if p.ID == id && id != "" {
			return p, true
		}
	}
	return domain.TopupPayee{}, false
}

func (s *TopupService) companyName(ctx context.Context, id string) string {
	if s.companies == nil {
		return ""
	}
	c, err := s.companies.GetByID(ctx, id)
	if err != nil || c == nil {
		return ""
	}
	return c.Name
}

func (s *TopupService) userEmail(ctx context.Context, id string) string {
	if s.users == nil || id == "" {
		return ""
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil || u == nil {
		return ""
	}
	return u.Email
}

// OperatorList is the review queue (view=review) or the last decisions
// (view=reviewed), across every workspace (R10).
func (s *TopupService) OperatorList(ctx context.Context, reviewed bool) ([]*domain.OperatorTopup, error) {
	s.expireStale(ctx)
	var (
		out []*domain.OperatorTopup
		err error
	)
	if reviewed {
		out, err = s.repo.ListReviewed(ctx, topupReviewedLimit)
	} else {
		out, err = s.repo.ListForReview(ctx)
	}
	if err != nil {
		return nil, err
	}
	// The row stores the id; the operator reads the label the customer chose
	// from. A payee removed from config since keeps its id, which is still
	// enough to find the account on a statement.
	for _, o := range out {
		if o == nil || o.PayeeID == nil {
			continue
		}
		o.PayeeLabel = *o.PayeeID
		if p, ok := s.payee(*o.PayeeID); ok {
			o.PayeeLabel = p.Label
		}
	}
	return out, nil
}

// TopupProofLink is View proof's answer: a presigned URL, never stored.
type TopupProofLink struct {
	URL         string
	ContentType string
	ExpiresIn   int
}

// ProofLink presigns the latest proof of any workspace's top-up for five
// minutes. The proof may carry the customer's account number, so the link is
// short-lived and nothing about it is logged.
func (s *TopupService) ProofLink(ctx context.Context, id string) (*TopupProofLink, error) {
	t, err := s.repo.GetAnyCompany(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.ProofKey == "" {
		return nil, domain.ErrNotFound
	}
	if s.store == nil {
		return nil, ErrTopupProofUnavailable
	}
	url, err := s.store.PresignKey(ctx, t.ProofKey, TopupProofLinkTTL)
	if err != nil {
		logrus.WithError(err).WithField("topup_id", id).Warn("top-up proof could not be presigned")
		return nil, ErrTopupProofUnavailable
	}
	return &TopupProofLink{URL: url, ContentType: t.ProofContentType, ExpiresIn: int(TopupProofLinkTTL.Seconds())}, nil
}

// Approve adds the frozen credit exactly once (R11) and drops the company's
// cached budget verdict, so the workspace can chat on its next message rather
// than a minute later.
func (s *TopupService) Approve(ctx context.Context, id, reviewerID string) (*domain.Topup, int64, error) {
	t, balance, err := s.repo.Approve(ctx, id, reviewerID)
	if err != nil {
		return nil, 0, err
	}
	if s.budget != nil {
		s.budget.InvalidateBudget(ctx, t.CompanyID)
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  t.CompanyID,
		"topup_id":    t.ID,
		"credit":      t.CreditMicroUSD,
		"balance":     balance,
		"reviewed_by": reviewerID,
	}).Info("top-up approved")
	return t, balance, nil
}

// Reject closes a request with the reason the customer will see (R12).
func (s *TopupService) Reject(ctx context.Context, id, reviewerID, reason string) (*domain.Topup, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, ErrTopupReasonRequired
	}
	if utf8.RuneCountInString(reason) > TopupRejectReasonMax {
		return nil, ErrTopupReasonTooLong
	}
	t, err := s.repo.Reject(ctx, id, reviewerID, reason)
	if err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  t.CompanyID,
		"topup_id":    t.ID,
		"reviewed_by": reviewerID,
	}).Info("top-up rejected")
	return t, nil
}

// FormatRupiah groups by thousands with a dot, the Indonesian way:
// 100347 → "100.347".
func FormatRupiah(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// FormatCreditUSD is a micro-USD credit in dollars, rounded down to the cent:
// 5763831 → "5.76". Down, like the credit itself: the page never promises a
// cent the balance will not show.
func FormatCreditUSD(micro int64) string {
	cents := micro / 10_000
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
