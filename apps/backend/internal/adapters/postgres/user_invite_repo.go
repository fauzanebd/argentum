package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type UserInviteRepo struct{ db *sql.DB }

func NewUserInviteRepo(db *sql.DB) *UserInviteRepo { return &UserInviteRepo{db: db} }

const inviteColumns = `id, company_id, email, role, token_hash, expires_at, accepted_at, invited_by, created_at`

func (r *UserInviteRepo) Create(ctx context.Context, inv *domain.UserInvite) error {
	const q = `
		INSERT INTO user_invites (company_id, email, role, token_hash, expires_at, invited_by)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid)
		RETURNING id, created_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		inv.CompanyID, strings.ToLower(inv.Email), string(inv.Role),
		inv.TokenHash, inv.ExpiresAt, inv.InvitedBy,
	).Scan(&inv.ID, &inv.CreatedAt); err != nil {
		return fmt.Errorf("insert invite: %w", err)
	}
	return nil
}

func (r *UserInviteRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.UserInvite, error) {
	const q = `SELECT ` + inviteColumns + ` FROM user_invites WHERE token_hash = $1`
	inv, err := scanInvite(r.db.QueryRowContext(ctx, q, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// ListOpenByCompany returns invites nobody has accepted, expired ones
// included: an admin needs to see a stale invite in order to re-send it.
func (r *UserInviteRepo) ListOpenByCompany(ctx context.Context, companyID string) ([]*domain.UserInvite, error) {
	const q = `SELECT ` + inviteColumns + `
		FROM user_invites
		WHERE company_id = $1 AND accepted_at IS NULL
		ORDER BY created_at`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.UserInvite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// MarkAccepted only fires on a still-open invite, so a replayed token updates
// zero rows. It backs up the same guard in UserRepo.Activate; either alone
// would do, and having both means a future caller that reorders them is still
// single-use.
func (r *UserInviteRepo) MarkAccepted(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE user_invites SET accepted_at = $2 WHERE id = $1 AND accepted_at IS NULL`
	res, err := r.db.ExecContext(ctx, q, id, at)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *UserInviteRepo) DeleteOpenFor(ctx context.Context, companyID, email string) error {
	const q = `DELETE FROM user_invites
		WHERE company_id = $1 AND lower(email) = lower($2) AND accepted_at IS NULL`
	_, err := r.db.ExecContext(ctx, q, companyID, email)
	return err
}

func scanInvite(s rowScanner) (*domain.UserInvite, error) {
	inv := &domain.UserInvite{}
	var role string
	var accepted sql.NullTime
	var invitedBy sql.NullString
	if err := s.Scan(&inv.ID, &inv.CompanyID, &inv.Email, &role, &inv.TokenHash,
		&inv.ExpiresAt, &accepted, &invitedBy, &inv.CreatedAt); err != nil {
		return nil, err
	}
	inv.Role = domain.Role(role)
	if accepted.Valid {
		t := accepted.Time
		inv.AcceptedAt = &t
	}
	inv.InvitedBy = invitedBy.String
	return inv, nil
}
