package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type ReportShareRepo struct{ db *sql.DB }

func NewReportShareRepo(db *sql.DB) *ReportShareRepo { return &ReportShareRepo{db: db} }

// shareColumns is the SELECT list every read shares. created_by is coalesced
// because the column is ON DELETE SET NULL: an admin can leave the company and
// their links keep working, which is a property of the link rather than of
// them.
const shareColumns = `
	id, company_id, document_id, token_hash, COALESCE(created_by::text, ''),
	created_at, expires_at, revoked_at, view_count, last_viewed_at`

func scanShare(s interface{ Scan(...any) error }) (*domain.ReportShare, error) {
	sh := &domain.ReportShare{}
	var revoked, lastViewed sql.NullTime
	if err := s.Scan(
		&sh.ID, &sh.CompanyID, &sh.DocumentID, &sh.TokenHash, &sh.CreatedBy,
		&sh.CreatedAt, &sh.ExpiresAt, &revoked, &sh.ViewCount, &lastViewed,
	); err != nil {
		return nil, err
	}
	if revoked.Valid {
		t := revoked.Time
		sh.RevokedAt = &t
	}
	if lastViewed.Valid {
		t := lastViewed.Time
		sh.LastViewedAt = &t
	}
	return sh, nil
}

func (r *ReportShareRepo) Insert(ctx context.Context, s *domain.ReportShare) error {
	const q = `
		INSERT INTO report_shares (id, company_id, document_id, token_hash, created_by, expires_at)
		VALUES (
			COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()),
			$2, $3, $4, NULLIF($5, '')::uuid, $6
		)
		RETURNING id, created_at`
	return r.db.QueryRowContext(ctx, q,
		s.ID, s.CompanyID, s.DocumentID, s.TokenHash, s.CreatedBy, s.ExpiresAt,
	).Scan(&s.ID, &s.CreatedAt)
}

// ByTokenHash is the only unscoped read in this file, and it is the whole
// point of the table: the caller is logged out, so the token is the credential
// and the company comes back with the row rather than being asserted by the
// request.
//
// It returns expired and revoked rows too. The handler decides what those
// mean, because a not-found and an expired must answer identically to a
// stranger and differently in our own logs — a distinguishable "expired" tells
// somebody enumerating tokens that they guessed one correctly.
func (r *ReportShareRepo) ByTokenHash(ctx context.Context, hash string) (*domain.ReportShare, error) {
	q := `SELECT ` + shareColumns + ` FROM report_shares WHERE token_hash = $1`
	sh, err := scanShare(r.db.QueryRowContext(ctx, q, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return sh, err
}

func (r *ReportShareRepo) ListForDocument(ctx context.Context, companyID, documentID string) ([]*domain.ReportShare, error) {
	q := `SELECT ` + shareColumns + ` FROM report_shares
		WHERE company_id = $1 AND document_id = $2
		ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, companyID, documentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.ReportShare
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// Revoke stamps revoked_at, scoped by company in the WHERE clause.
//
// Idempotent by `revoked_at IS NULL`: revoking twice is what a nervous admin
// does, and the second call succeeding is the honest answer — the link is
// revoked either way. A row that does not exist, or belongs to somebody else,
// is a not-found rather than a silent success.
func (r *ReportShareRepo) Revoke(ctx context.Context, companyID, id string) error {
	const q = `
		UPDATE report_shares SET revoked_at = NOW()
		WHERE id = $1 AND company_id = $2 AND revoked_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, id, companyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Already revoked, or not ours. Tell them apart with one more read so
		// a double-click is a 200 and another tenant's id is a 404.
		var exists bool
		err := r.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM report_shares WHERE id = $1 AND company_id = $2)`,
			id, companyID).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return domain.ErrNotFound
		}
	}
	return nil
}

func (r *ReportShareRepo) RecordView(ctx context.Context, id string, at time.Time) error {
	const q = `
		UPDATE report_shares
		SET view_count = view_count + 1, last_viewed_at = $2
		WHERE id = $1`
	_, err := r.db.ExecContext(ctx, q, id, at)
	return err
}
