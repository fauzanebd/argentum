package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// CreditGrantRepo persists free-credit requests (RIZ-89).
//
// InsertIfAbsent and GetForCompany scope by company_id. The operator's methods
// are cross-tenant by design and named like TopupRepo's — see
// domain.CreditGrantRequestRepository.
type CreditGrantRepo struct{ db *sql.DB }

func NewCreditGrantRepo(db *sql.DB) *CreditGrantRepo { return &CreditGrantRepo{db: db} }

// host() rather than ::text: an inet cast to text carries its netmask
// ("203.0.113.7/32"), and the address is all anything here wants.
const creditGrantColumns = `r.id, r.company_id, r.status, r.amount_micro_usd, r.created_at, r.reviewed_at,
	COALESCE(r.requested_by::text, ''), COALESCE(host(r.signup_ip), ''), COALESCE(r.reviewed_by::text, '')`

func creditGrantDest(g *domain.CreditGrantRequest) ([]any, func()) {
	var (
		status     string
		reviewedAt sql.NullTime
	)
	dest := []any{
		&g.ID, &g.CompanyID, &status, &g.AmountMicroUSD, &g.CreatedAt, &reviewedAt,
		&g.RequestedBy, &g.SignupIP, &g.ReviewedBy,
	}
	return dest, func() {
		g.Status = domain.CreditGrantStatus(status)
		if reviewedAt.Valid {
			at := reviewedAt.Time
			g.ReviewedAt = &at
		}
	}
}

func scanCreditGrant(row rowScanner) (*domain.CreditGrantRequest, error) {
	g := &domain.CreditGrantRequest{}
	dest, finish := creditGrantDest(g)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	finish()
	return g, nil
}

// InsertIfAbsent files a pending request. ON CONFLICT DO NOTHING on the
// one-per-company key, so a company whose sign-up already filed — or whose
// request was decided — is never filed again, however many budget checks or
// pods race to it. RETURNING yields no row on a conflict, which is how it
// reports that it did nothing.
func (r *CreditGrantRepo) InsertIfAbsent(ctx context.Context, g *domain.CreditGrantRequest) (bool, error) {
	const q = `
		INSERT INTO credit_grant_requests (company_id, amount_micro_usd, requested_by, signup_ip)
		VALUES ($1, $2, NULLIF($3, '')::uuid, NULLIF($4, '')::inet)
		ON CONFLICT (company_id) DO NOTHING
		RETURNING id, status, created_at
	`
	var status string
	err := r.db.QueryRowContext(ctx, q, g.CompanyID, g.AmountMicroUSD, g.RequestedBy, g.SignupIP).
		Scan(&g.ID, &status, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert free-credit request: %w", err)
	}
	g.Status = domain.CreditGrantStatus(status)
	return true, nil
}

func (r *CreditGrantRepo) GetForCompany(ctx context.Context, companyID string) (*domain.CreditGrantRequest, error) {
	q := `SELECT ` + creditGrantColumns + ` FROM credit_grant_requests r WHERE r.company_id = $1`
	g, err := scanCreditGrant(r.db.QueryRowContext(ctx, q, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return g, err
}

// operatorCreditGrantSelect adds the operator's columns. The same-IP count is
// the other sign-ups from this request's address within seven days either
// side of it — either side, so the first of a scripted batch is flagged as
// loudly as the last — and the partial index on (signup_ip, created_at) is
// what it reads.
const operatorCreditGrantSelect = `SELECT ` + creditGrantColumns + `,
		c.name, COALESCE(ru.email, ''), COALESCE(vu.email, ''),
		EXISTS (SELECT 1 FROM db_connections dc WHERE dc.company_id = r.company_id),
		CASE WHEN r.signup_ip IS NULL THEN 0 ELSE (
			SELECT count(*) FROM credit_grant_requests o
			WHERE o.signup_ip = r.signup_ip AND o.id <> r.id
			  AND o.created_at BETWEEN r.created_at - interval '7 days' AND r.created_at + interval '7 days'
		) END
	FROM credit_grant_requests r
	JOIN companies c ON c.id = r.company_id
	LEFT JOIN users ru ON ru.id = r.requested_by
	LEFT JOIN users vu ON vu.id = r.reviewed_by`

func scanOperatorCreditGrant(row rowScanner) (*domain.OperatorFreeCredit, error) {
	o := &domain.OperatorFreeCredit{}
	dest, finish := creditGrantDest(&o.CreditGrantRequest)
	dest = append(dest, &o.CompanyName, &o.RequestedByEmail, &o.ReviewedByEmail, &o.HasDataSource, &o.SameIPSignups7d)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	finish()
	return o, nil
}

func (r *CreditGrantRepo) listOperator(ctx context.Context, q string, args ...any) ([]*domain.OperatorFreeCredit, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.OperatorFreeCredit{}
	for rows.Next() {
		o, err := scanOperatorCreditGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListPending is every workspace's pending request, oldest first: the sign-up
// that has waited longest is first in the queue.
func (r *CreditGrantRepo) ListPending(ctx context.Context) ([]*domain.OperatorFreeCredit, error) {
	return r.listOperator(ctx, operatorCreditGrantSelect+`
		WHERE r.status = 'pending' ORDER BY r.created_at ASC, r.id ASC`)
}

// ListReviewed is the last decisions, newest first.
func (r *CreditGrantRepo) ListReviewed(ctx context.Context, limit int) ([]*domain.OperatorFreeCredit, error) {
	if limit <= 0 {
		limit = 50
	}
	return r.listOperator(ctx, operatorCreditGrantSelect+`
		WHERE r.status IN ('approved', 'rejected') ORDER BY r.reviewed_at DESC, r.id DESC LIMIT $1`, limit)
}

// Approve writes a company's free credit exactly once (R8).
//
// The guard is TopupRepo.Approve's: the UPDATE's own WHERE status = 'pending',
// in the same transaction as the balance write, so a second approve blocks on
// the first's row lock, re-reads the row as approved and adds nothing.
//
// What it writes depends on the company_credits row, which is locked first so
// nothing changes it between the read and the write:
//
//   - **No row, or a row whose grant is 0** — a new sign-up, or a company from
//     before RIZ-79 that never chatted, or one whose only row is the negative
//     balance Decrement minted. Balance and grant both become the amount. A
//     negative balance with no grant was never a debt the tenant agreed to;
//     the lazy grant forgave it the same way (credits.go, decision 1).
//   - **A row that was topped up** — the workspace paid while it waited (AC11).
//     The amount is added on top, and the grant becomes the balance it leaves,
//     never below the amount, exactly as a top-up approval writes it.
//   - **A row that already holds free credit and was never topped up** — a
//     grant written while CREDITS_GRANT_REQUIRES_APPROVAL was off, or by an
//     older pod's lazy grant during the rolling deploy. Nothing is added: that
//     workspace already had its $0.50, and approving must not make it $1.00.
//
// The no-row case inserts with DO NOTHING rather than DO UPDATE: if a row
// appeared since the locked read found none (a Decrement, a top-up, an older
// pod's lazy grant), the insert does nothing, the row is read again — locked —
// and the branches above decide, rather than an upsert's arm guessing which
// of them it is.
func (r *CreditGrantRepo) Approve(ctx context.Context, id, reviewerID string) (*domain.CreditGrantApproval, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	q := `
		UPDATE credit_grant_requests r
		SET status = 'approved', reviewed_by = NULLIF($2, '')::uuid, reviewed_at = now(), updated_at = now()
		WHERE r.id = $1 AND r.status = 'pending'
		RETURNING ` + creditGrantColumns
	g, err := scanCreditGrant(tx.QueryRowContext(ctx, q, id, reviewerID))
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return nil, r.reviewedOrMissing(ctx, id)
	}
	if err != nil {
		return nil, fmt.Errorf("mark free-credit request approved: %w", err)
	}

	added, balance, err := writeFreeCredit(ctx, tx, g.CompanyID, g.AmountMicroUSD)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit free-credit approval: %w", err)
	}
	return &domain.CreditGrantApproval{Request: g, AddedMicroUSD: added, BalanceMicroUSD: balance}, nil
}

// writeFreeCredit is Approve's balance write. The casts are Decrement's
// (RIZ-79 QA): an untyped parameter beside `+` is a statement Postgres
// refuses.
func writeFreeCredit(ctx context.Context, tx *sql.Tx, companyID string, amount int64) (added, balance int64, err error) {
	const lock = `
		SELECT balance_micro_usd, monthly_grant_micro_usd, last_topup_at IS NOT NULL
		FROM company_credits WHERE company_id = $1 FOR UPDATE`
	var (
		grant    int64
		toppedUp bool
	)
	err = tx.QueryRowContext(ctx, lock, companyID).Scan(&balance, &grant, &toppedUp)
	if errors.Is(err, sql.ErrNoRows) {
		const insert = `
			INSERT INTO company_credits (company_id, balance_micro_usd, monthly_grant_micro_usd, updated_at)
			VALUES ($1, $2::bigint, $2::bigint, now())
			ON CONFLICT (company_id) DO NOTHING
			RETURNING balance_micro_usd`
		err = tx.QueryRowContext(ctx, insert, companyID, amount).Scan(&balance)
		if err == nil {
			return amount, balance, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, 0, fmt.Errorf("insert free credit: %w", err)
		}
		// A row appeared since the read. Read it again, locked, and decide.
		err = tx.QueryRowContext(ctx, lock, companyID).Scan(&balance, &grant, &toppedUp)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read balance for free credit: %w", err)
	}

	switch {
	case grant == 0:
		const forgive = `
			UPDATE company_credits
			SET balance_micro_usd = $2::bigint, monthly_grant_micro_usd = $2::bigint, updated_at = now()
			WHERE company_id = $1
			RETURNING balance_micro_usd`
		if err := tx.QueryRowContext(ctx, forgive, companyID, amount).Scan(&balance); err != nil {
			return 0, 0, fmt.Errorf("write free credit: %w", err)
		}
		return amount, balance, nil
	case toppedUp:
		const add = `
			UPDATE company_credits
			SET balance_micro_usd = company_credits.balance_micro_usd + $2::bigint,
				monthly_grant_micro_usd = GREATEST(company_credits.balance_micro_usd + $2::bigint, $2::bigint),
				updated_at = now()
			WHERE company_id = $1
			RETURNING balance_micro_usd`
		if err := tx.QueryRowContext(ctx, add, companyID, amount).Scan(&balance); err != nil {
			return 0, 0, fmt.Errorf("add free credit: %w", err)
		}
		return amount, balance, nil
	default:
		return 0, balance, nil
	}
}

// Reject closes a pending request without touching the balance, under the
// same guard as Approve.
func (r *CreditGrantRepo) Reject(ctx context.Context, id, reviewerID string) (*domain.CreditGrantRequest, error) {
	q := `
		UPDATE credit_grant_requests r
		SET status = 'rejected', reviewed_by = NULLIF($2, '')::uuid, reviewed_at = now(), updated_at = now()
		WHERE r.id = $1 AND r.status = 'pending'
		RETURNING ` + creditGrantColumns
	g, err := scanCreditGrant(r.db.QueryRowContext(ctx, q, id, reviewerID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, r.reviewedOrMissing(ctx, id)
	}
	return g, err
}

func (r *CreditGrantRepo) reviewedOrMissing(ctx context.Context, id string) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM credit_grant_requests WHERE id = $1)`, id,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return domain.ErrCreditGrantAlreadyReviewed
}
