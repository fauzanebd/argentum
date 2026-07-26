package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

type AllowedLarkUserRepo struct{ db *sql.DB }

func NewAllowedLarkUserRepo(db *sql.DB) *AllowedLarkUserRepo {
	return &AllowedLarkUserRepo{db: db}
}

func (r *AllowedLarkUserRepo) Add(ctx context.Context, u *domain.AllowedLarkUser) error {
	const q = `
		INSERT INTO allowed_lark_users (company_id, lark_open_id, label)
		VALUES ($1, $2, $3)
		RETURNING added_at`
	err := r.db.QueryRowContext(ctx, q,
		u.CompanyID, strings.TrimSpace(u.LarkOpenID), u.Label,
	).Scan(&u.AddedAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return domain.ErrAlreadyExists
	}
	return err
}

func (r *AllowedLarkUserRepo) Remove(ctx context.Context, companyID, larkOpenID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM allowed_lark_users WHERE company_id = $1 AND lark_open_id = $2`,
		companyID, strings.TrimSpace(larkOpenID))
	return err
}

func (r *AllowedLarkUserRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.AllowedLarkUser, error) {
	const q = `SELECT company_id, lark_open_id, COALESCE(label, ''), added_at
		FROM allowed_lark_users WHERE company_id = $1 ORDER BY added_at`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.AllowedLarkUser
	for rows.Next() {
		u := &domain.AllowedLarkUser{}
		if err := rows.Scan(&u.CompanyID, &u.LarkOpenID, &u.Label, &u.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *AllowedLarkUserRepo) IsAllowed(ctx context.Context, companyID, larkOpenID string) (bool, error) {
	const q = `SELECT 1 FROM allowed_lark_users WHERE company_id = $1 AND lark_open_id = $2`
	var one int
	err := r.db.QueryRowContext(ctx, q, companyID, strings.TrimSpace(larkOpenID)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

var _ domain.AllowedLarkUserRepository = (*AllowedLarkUserRepo)(nil)
