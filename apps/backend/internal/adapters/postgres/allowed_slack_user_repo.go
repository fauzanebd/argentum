package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

type AllowedSlackUserRepo struct{ db *sql.DB }

func NewAllowedSlackUserRepo(db *sql.DB) *AllowedSlackUserRepo {
	return &AllowedSlackUserRepo{db: db}
}

func (r *AllowedSlackUserRepo) Add(ctx context.Context, u *domain.AllowedSlackUser) error {
	const q = `
		INSERT INTO allowed_slack_users (company_id, slack_user_id, label)
		VALUES ($1, $2, $3)
		RETURNING added_at`
	err := r.db.QueryRowContext(ctx, q,
		u.CompanyID, strings.TrimSpace(u.SlackUserID), u.Label,
	).Scan(&u.AddedAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return domain.ErrAlreadyExists
	}
	return err
}

func (r *AllowedSlackUserRepo) Remove(ctx context.Context, companyID, slackUserID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM allowed_slack_users WHERE company_id = $1 AND slack_user_id = $2`,
		companyID, strings.TrimSpace(slackUserID))
	return err
}

func (r *AllowedSlackUserRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.AllowedSlackUser, error) {
	const q = `SELECT company_id, slack_user_id, COALESCE(label, ''), added_at
		FROM allowed_slack_users WHERE company_id = $1 ORDER BY added_at`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.AllowedSlackUser
	for rows.Next() {
		u := &domain.AllowedSlackUser{}
		if err := rows.Scan(&u.CompanyID, &u.SlackUserID, &u.Label, &u.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *AllowedSlackUserRepo) IsAllowed(ctx context.Context, companyID, slackUserID string) (bool, error) {
	const q = `SELECT 1 FROM allowed_slack_users WHERE company_id = $1 AND slack_user_id = $2`
	var one int
	err := r.db.QueryRowContext(ctx, q, companyID, strings.TrimSpace(slackUserID)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

var _ domain.AllowedSlackUserRepository = (*AllowedSlackUserRepo)(nil)
