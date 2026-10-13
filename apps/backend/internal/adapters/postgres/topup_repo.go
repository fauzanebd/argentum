package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// TopupRepo persists credit top-up requests (RIZ-83).
//
// The tenant's methods scope every statement by company_id, like every other
// repository here. The operator's are cross-tenant by design and say so in
// their names — see domain.TopupRepository.
type TopupRepo struct{ db *sql.DB }

func NewTopupRepo(db *sql.DB) *TopupRepo { return &TopupRepo{db: db} }

// The two open-row partial unique indexes 090 creates. Create tells them apart
// by name, because they mean opposite things to the caller: one is the
// customer's (they already have a request open — a 409), the other is ours (a
// random code collided — draw another and try again).
const (
	topupOneOpenIndex        = "idx_credit_topups_one_open_per_company"
	topupExpectedAmountIndex = "idx_credit_topups_open_expected_amount"
)

const topupColumns = `t.id, t.company_id, COALESCE(t.requested_by::text, ''), t.amount_idr, t.unique_code,
	t.expected_amount_idr, t.credit_micro_usd, t.status, t.payee_id, t.proof_uploaded_at,
	t.expires_at, t.created_at, t.reviewed_at, t.reject_reason,
	t.fx_source, t.fx_market_idr_per_usd_e4, to_char(t.fx_rate_date, 'YYYY-MM-DD'), t.fx_spread_bp,
	t.fx_effective_idr_per_usd_e4,
	COALESCE(t.proof_key, ''), COALESCE(t.proof_content_type, ''), COALESCE(t.proof_sha256, ''),
	COALESCE(t.reviewed_by::text, '')`

// topupDest returns the scan targets for topupColumns and a finish func that
// copies the nullable ones onto t. Shared by the plain and the operator scans
// so the column list is written down once.
func topupDest(t *domain.Topup) ([]any, func()) {
	var (
		status, fxSource       string
		payee, reason, rateDay sql.NullString
		proofAt, reviewedAt    sql.NullTime
		market                 sql.NullInt64
		spread                 sql.NullInt32
	)
	dest := []any{
		&t.ID, &t.CompanyID, &t.RequestedBy, &t.AmountIDR, &t.UniqueCode,
		&t.ExpectedAmountIDR, &t.CreditMicroUSD, &status, &payee, &proofAt,
		&t.ExpiresAt, &t.CreatedAt, &reviewedAt, &reason,
		&fxSource, &market, &rateDay, &spread,
		&t.FX.EffectiveIDRPerUSDE4,
		&t.ProofKey, &t.ProofContentType, &t.ProofSHA256,
		&t.ReviewedBy,
	}
	return dest, func() {
		t.Status = domain.TopupStatus(status)
		t.FX.Source = domain.TopupFXSource(fxSource)
		if payee.Valid {
			t.PayeeID = &payee.String
		}
		if reason.Valid {
			t.RejectReason = &reason.String
		}
		if proofAt.Valid {
			at := proofAt.Time
			t.ProofUploadedAt = &at
		}
		if reviewedAt.Valid {
			at := reviewedAt.Time
			t.ReviewedAt = &at
		}
		if market.Valid {
			m := market.Int64
			t.FX.MarketIDRPerUSDE4 = &m
		}
		if rateDay.Valid {
			t.FX.RateDate = rateDay.String
		}
		if spread.Valid {
			s := int(spread.Int32)
			t.FX.SpreadBP = &s
		}
	}
}

func scanTopup(row rowScanner) (*domain.Topup, error) {
	t := &domain.Topup{}
	dest, finish := topupDest(t)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	finish()
	return t, nil
}

// Create inserts an awaiting_payment request with its frozen rate.
func (r *TopupRepo) Create(ctx context.Context, t *domain.Topup) error {
	const q = `
		INSERT INTO credit_topups (company_id, requested_by, amount_idr, unique_code, expected_amount_idr,
			fx_source, fx_market_idr_per_usd_e4, fx_rate_date, fx_spread_bp, fx_effective_idr_per_usd_e4,
			credit_micro_usd, status, expires_at)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, NULLIF($8, '')::date, $9, $10, $11, 'awaiting_payment', $12)
		RETURNING id, created_at
	`
	var spread sql.NullInt32
	if t.FX.SpreadBP != nil {
		spread = sql.NullInt32{Int32: int32(*t.FX.SpreadBP), Valid: true}
	}
	var market sql.NullInt64
	if t.FX.MarketIDRPerUSDE4 != nil {
		market = sql.NullInt64{Int64: *t.FX.MarketIDRPerUSDE4, Valid: true}
	}
	err := r.db.QueryRowContext(ctx, q,
		t.CompanyID, t.RequestedBy, t.AmountIDR, t.UniqueCode, t.ExpectedAmountIDR,
		string(t.FX.Source), market, t.FX.RateDate, spread, t.FX.EffectiveIDRPerUSDE4,
		t.CreditMicroUSD, t.ExpiresAt,
	).Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		var pgErr *pq.Error
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.Constraint {
			case topupOneOpenIndex:
				return domain.ErrTopupInProgress
			case topupExpectedAmountIndex:
				return domain.ErrTopupCodeTaken
			}
		}
		return fmt.Errorf("insert credit top-up: %w", err)
	}
	t.Status = domain.TopupStatusAwaitingPayment
	return nil
}

func (r *TopupRepo) GetForCompany(ctx context.Context, companyID, id string) (*domain.Topup, error) {
	q := `SELECT ` + topupColumns + ` FROM credit_topups t WHERE t.id = $1 AND t.company_id = $2`
	t, err := scanTopup(r.db.QueryRowContext(ctx, q, id, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		// Also the answer for another company's id: a 403 would confirm the
		// row is real to a caller holding a bare uuid.
		return nil, domain.ErrNotFound
	}
	return t, err
}

func (r *TopupRepo) ListForCompany(ctx context.Context, companyID string, limit int) ([]*domain.Topup, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT ` + topupColumns + ` FROM credit_topups t
		WHERE t.company_id = $1 ORDER BY t.created_at DESC LIMIT $2`
	rows, err := r.db.QueryContext(ctx, q, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.Topup{}
	for rows.Next() {
		t, err := scanTopup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AttachProof is guarded in its WHERE clause, so a request that expired or was
// decided between the caller reading it and this write cannot be reopened:
// awaiting_payment only while unexpired, and in_review (a replaced proof).
func (r *TopupRepo) AttachProof(ctx context.Context, companyID, id string, p domain.TopupProof) (*domain.Topup, error) {
	q := `
		UPDATE credit_topups t
		SET proof_key = $3, proof_content_type = $4, proof_sha256 = $5, payee_id = $6,
			proof_uploaded_at = now(), status = 'in_review', updated_at = now()
		WHERE t.id = $1 AND t.company_id = $2
		  AND ((t.status = 'awaiting_payment' AND t.expires_at > now()) OR t.status = 'in_review')
		RETURNING ` + topupColumns
	t, err := scanTopup(r.db.QueryRowContext(ctx, q, id, companyID, p.Key, p.ContentType, p.SHA256, p.PayeeID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, r.closedOrMissing(ctx, companyID, id, domain.ErrTopupClosed)
	}
	return t, err
}

func (r *TopupRepo) Cancel(ctx context.Context, companyID, id string) (*domain.Topup, error) {
	q := `
		UPDATE credit_topups t SET status = 'cancelled', updated_at = now()
		WHERE t.id = $1 AND t.company_id = $2 AND t.status = 'awaiting_payment'
		RETURNING ` + topupColumns
	t, err := scanTopup(r.db.QueryRowContext(ctx, q, id, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, r.closedOrMissing(ctx, companyID, id, domain.ErrTopupNotCancellable)
	}
	return t, err
}

// closedOrMissing turns a guarded write that matched no row into the right
// refusal: the row is this company's and in the wrong state, or it is not
// this company's at all.
func (r *TopupRepo) closedOrMissing(ctx context.Context, companyID, id string, closed error) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM credit_topups WHERE id = $1 AND company_id = $2)`, id, companyID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return closed
}

// ExpireStale is the lazy expiry (R8). Cross-tenant on purpose: it frees the
// matching codes every company's next request draws from, so expiring only
// the caller's own rows would leave a stale code blocking somebody else.
func (r *TopupRepo) ExpireStale(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE credit_topups SET status = 'expired', updated_at = now()
		WHERE status = 'awaiting_payment' AND expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// operatorTopupFrom adds the operator's joins to topupColumns. The duplicate is
// the earliest *other* top-up with the same proof bytes uploaded before this
// one, so the original is not flagged as a copy of its own copy.
const operatorTopupFrom = `
	FROM credit_topups t
	JOIN companies c ON c.id = t.company_id
	LEFT JOIN users ru ON ru.id = t.requested_by
	LEFT JOIN users vu ON vu.id = t.reviewed_by
	LEFT JOIN LATERAL (
		SELECT dc.name, d.expected_amount_idr, d.created_at
		FROM credit_topups d JOIN companies dc ON dc.id = d.company_id
		WHERE t.proof_sha256 IS NOT NULL AND d.proof_sha256 = t.proof_sha256 AND d.id <> t.id
		  AND (d.proof_uploaded_at, d.id) < (t.proof_uploaded_at, t.id)
		ORDER BY d.proof_uploaded_at ASC, d.id ASC
		LIMIT 1
	) dup ON true`

const operatorTopupColumns = topupColumns + `, c.name, COALESCE(ru.email, ''), vu.email,
	dup.name, dup.expected_amount_idr, dup.created_at`

func scanOperatorTopup(row rowScanner) (*domain.OperatorTopup, error) {
	o := &domain.OperatorTopup{}
	dest, finish := topupDest(&o.Topup)
	var (
		reviewer, dupName sql.NullString
		dupAmount         sql.NullInt64
		dupAt             sql.NullTime
	)
	dest = append(dest, &o.CompanyName, &o.RequestedByEmail, &reviewer, &dupName, &dupAmount, &dupAt)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	finish()
	if reviewer.Valid {
		o.ReviewedByEmail = &reviewer.String
	}
	if dupName.Valid {
		o.DuplicateOf = &domain.TopupDuplicate{
			CompanyName: dupName.String, ExpectedAmountIDR: dupAmount.Int64, CreatedAt: dupAt.Time,
		}
	}
	o.FXSource = o.FX.Source
	o.FXMarketIDRPerUSDE4 = o.FX.MarketIDRPerUSDE4
	if o.FX.RateDate != "" {
		d := o.FX.RateDate
		o.FXRateDate = &d
	}
	o.FXSpreadBP = o.FX.SpreadBP
	o.FXEffectiveIDRPerUSDE4 = o.FX.EffectiveIDRPerUSDE4
	return o, nil
}

func (r *TopupRepo) listOperator(ctx context.Context, q string, args ...any) ([]*domain.OperatorTopup, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.OperatorTopup{}
	for rows.Next() {
		o, err := scanOperatorTopup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListForReview is every workspace's in_review top-ups, oldest proof first:
// the customer who has waited longest is first in the queue.
func (r *TopupRepo) ListForReview(ctx context.Context) ([]*domain.OperatorTopup, error) {
	return r.listOperator(ctx, `SELECT `+operatorTopupColumns+operatorTopupFrom+`
		WHERE t.status = 'in_review' ORDER BY t.proof_uploaded_at ASC, t.id ASC`)
}

// ListReviewed is the last decisions, newest first.
func (r *TopupRepo) ListReviewed(ctx context.Context, limit int) ([]*domain.OperatorTopup, error) {
	if limit <= 0 {
		limit = 50
	}
	return r.listOperator(ctx, `SELECT `+operatorTopupColumns+operatorTopupFrom+`
		WHERE t.status IN ('approved', 'rejected') ORDER BY t.reviewed_at DESC, t.id DESC LIMIT $1`, limit)
}

// GetAnyCompany reads one top-up whoever owns it. The operator's View proof is
// the only caller.
func (r *TopupRepo) GetAnyCompany(ctx context.Context, id string) (*domain.Topup, error) {
	q := `SELECT ` + topupColumns + ` FROM credit_topups t WHERE t.id = $1`
	t, err := scanTopup(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

// Approve credits a top-up exactly once (R11).
//
// The guard is the UPDATE's own WHERE status = 'in_review', inside the same
// transaction as the balance write. A second approve — a double click, two
// operators — blocks on the first's row lock, then re-reads the row as
// approved and updates nothing, so it adds nothing. No ledger table and no
// idempotency key are needed for that, and there is no window between
// "checked it was in review" and "marked it approved" for anything to land in.
//
// The balance write is an upsert: a legacy workspace with no company_credits
// row gets balance = grant = credit. Otherwise the credit is added — paying
// off a negative balance first, which is what addition does — and the grant
// becomes the balance this leaves, never below the credit itself, so the
// Usage bar and the 20% warning measure from the level right after the top-up.
// The casts are Decrement's (RIZ-79 QA): an untyped parameter beside `+` is a
// statement Postgres refuses.
func (r *TopupRepo) Approve(ctx context.Context, id, reviewerID string) (*domain.Topup, int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	q := `
		UPDATE credit_topups t
		SET status = 'approved', reviewed_by = NULLIF($2, '')::uuid, reviewed_at = now(), updated_at = now()
		WHERE t.id = $1 AND t.status = 'in_review'
		RETURNING ` + topupColumns
	t, err := scanTopup(tx.QueryRowContext(ctx, q, id, reviewerID))
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return nil, 0, r.reviewedOrMissing(ctx, id)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("mark top-up approved: %w", err)
	}

	const credit = `
		INSERT INTO company_credits (company_id, balance_micro_usd, monthly_grant_micro_usd, last_topup_at, updated_at)
		VALUES ($1, $2::bigint, $2::bigint, now(), now())
		ON CONFLICT (company_id) DO UPDATE
			SET balance_micro_usd = company_credits.balance_micro_usd + $2::bigint,
				monthly_grant_micro_usd = GREATEST(company_credits.balance_micro_usd + $2::bigint, $2::bigint),
				last_topup_at = now(),
				updated_at = now()
		RETURNING balance_micro_usd
	`
	var balance int64
	if err := tx.QueryRowContext(ctx, credit, t.CompanyID, t.CreditMicroUSD).Scan(&balance); err != nil {
		return nil, 0, fmt.Errorf("add top-up credit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("commit top-up approval: %w", err)
	}
	return t, balance, nil
}

// Reject closes an in_review top-up without touching the balance, under the
// same guard as Approve.
func (r *TopupRepo) Reject(ctx context.Context, id, reviewerID, reason string) (*domain.Topup, error) {
	q := `
		UPDATE credit_topups t
		SET status = 'rejected', reject_reason = $3, reviewed_by = NULLIF($2, '')::uuid,
			reviewed_at = now(), updated_at = now()
		WHERE t.id = $1 AND t.status = 'in_review'
		RETURNING ` + topupColumns
	t, err := scanTopup(r.db.QueryRowContext(ctx, q, id, reviewerID, reason))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, r.reviewedOrMissing(ctx, id)
	}
	return t, err
}

func (r *TopupRepo) reviewedOrMissing(ctx context.Context, id string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM credit_topups WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return domain.ErrTopupAlreadyReviewed
}
