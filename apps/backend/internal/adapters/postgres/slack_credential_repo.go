package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type CompanySlackCredentialRepo struct{ db *sql.DB }

func NewCompanySlackCredentialRepo(db *sql.DB) *CompanySlackCredentialRepo {
	return &CompanySlackCredentialRepo{db: db}
}

const slackCredColumns = `company_id, app_id, COALESCE(team_id, ''), bot_token_encrypted,
	signing_secret, COALESCE(bot_user_id, ''), enabled, created_at, updated_at`

type slackCredScanner interface {
	Scan(dest ...interface{}) error
}

func scanSlackCred(row slackCredScanner) (*domain.CompanySlackCredential, error) {
	c := &domain.CompanySlackCredential{}
	if err := row.Scan(
		&c.CompanyID, &c.AppID, &c.TeamID, &c.BotTokenEncrypted,
		&c.SigningSecret, &c.BotUserID, &c.Enabled, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return c, nil
}

func (r *CompanySlackCredentialRepo) Get(ctx context.Context, companyID string) (*domain.CompanySlackCredential, error) {
	q := `SELECT ` + slackCredColumns + ` FROM company_slack_credentials WHERE company_id = $1`
	c, err := scanSlackCred(r.db.QueryRowContext(ctx, q, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CompanySlackCredentialRepo) GetByAppID(ctx context.Context, appID string) (*domain.CompanySlackCredential, error) {
	q := `SELECT ` + slackCredColumns + ` FROM company_slack_credentials WHERE app_id = $1`
	c, err := scanSlackCred(r.db.QueryRowContext(ctx, q, appID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CompanySlackCredentialRepo) Upsert(ctx context.Context, c *domain.CompanySlackCredential) error {
	const q = `
		INSERT INTO company_slack_credentials
			(company_id, app_id, team_id, bot_token_encrypted, signing_secret, bot_user_id, enabled)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''), $7)
		ON CONFLICT (company_id) DO UPDATE
		SET app_id              = EXCLUDED.app_id,
		    team_id             = EXCLUDED.team_id,
		    bot_token_encrypted = EXCLUDED.bot_token_encrypted,
		    signing_secret      = EXCLUDED.signing_secret,
		    bot_user_id         = EXCLUDED.bot_user_id,
		    enabled             = EXCLUDED.enabled,
		    updated_at          = now()
		RETURNING created_at, updated_at`
	if err := r.db.QueryRowContext(ctx, q,
		c.CompanyID, c.AppID, c.TeamID, c.BotTokenEncrypted,
		c.SigningSecret, c.BotUserID, c.Enabled,
	).Scan(&c.CreatedAt, &c.UpdatedAt); err != nil {
		return fmt.Errorf("upsert slack credential: %w", err)
	}
	return nil
}

func (r *CompanySlackCredentialRepo) Delete(ctx context.Context, companyID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM company_slack_credentials WHERE company_id = $1`, companyID)
	return err
}

func (r *CompanySlackCredentialRepo) ListEnabled(ctx context.Context) ([]*domain.CompanySlackCredential, error) {
	q := `SELECT ` + slackCredColumns + ` FROM company_slack_credentials WHERE enabled = true`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CompanySlackCredential
	for rows.Next() {
		c, err := scanSlackCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var _ domain.CompanySlackCredentialRepository = (*CompanySlackCredentialRepo)(nil)
