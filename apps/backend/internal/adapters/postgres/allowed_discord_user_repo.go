package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

type AllowedDiscordUserRepo struct{ db *sql.DB }

func NewAllowedDiscordUserRepo(db *sql.DB) *AllowedDiscordUserRepo {
	return &AllowedDiscordUserRepo{db: db}
}

func (r *AllowedDiscordUserRepo) Add(ctx context.Context, u *domain.AllowedDiscordUser) error {
	const q = `
		INSERT INTO allowed_discord_users (company_id, discord_user_id, label)
		VALUES ($1, $2, $3)
		RETURNING added_at`
	err := r.db.QueryRowContext(ctx, q,
		u.CompanyID, strings.TrimSpace(u.DiscordUserID), u.Label,
	).Scan(&u.AddedAt)
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return domain.ErrAlreadyExists
	}
	return err
}

func (r *AllowedDiscordUserRepo) Remove(ctx context.Context, companyID, discordUserID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM allowed_discord_users WHERE company_id = $1 AND discord_user_id = $2`,
		companyID, strings.TrimSpace(discordUserID))
	return err
}

func (r *AllowedDiscordUserRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.AllowedDiscordUser, error) {
	const q = `SELECT company_id, discord_user_id, COALESCE(label, ''), added_at
		FROM allowed_discord_users WHERE company_id = $1 ORDER BY added_at`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.AllowedDiscordUser
	for rows.Next() {
		u := &domain.AllowedDiscordUser{}
		if err := rows.Scan(&u.CompanyID, &u.DiscordUserID, &u.Label, &u.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *AllowedDiscordUserRepo) IsAllowed(ctx context.Context, companyID, discordUserID string) (bool, error) {
	const q = `SELECT 1 FROM allowed_discord_users WHERE company_id = $1 AND discord_user_id = $2`
	var one int
	err := r.db.QueryRowContext(ctx, q, companyID, strings.TrimSpace(discordUserID)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

var _ domain.AllowedDiscordUserRepository = (*AllowedDiscordUserRepo)(nil)
