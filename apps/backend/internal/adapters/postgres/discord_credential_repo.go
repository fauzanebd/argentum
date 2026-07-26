package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type CompanyDiscordCredentialRepo struct{ db *sql.DB }

func NewCompanyDiscordCredentialRepo(db *sql.DB) *CompanyDiscordCredentialRepo {
	return &CompanyDiscordCredentialRepo{db: db}
}

const discordCredColumns = `company_id, application_id, public_key, bot_token_encrypted,
	COALESCE(guild_id, ''), enabled, created_at, updated_at`

type discordCredScanner interface {
	Scan(dest ...interface{}) error
}

func scanDiscordCred(row discordCredScanner) (*domain.CompanyDiscordCredential, error) {
	c := &domain.CompanyDiscordCredential{}
	if err := row.Scan(
		&c.CompanyID, &c.ApplicationID, &c.PublicKey, &c.BotTokenEncrypted,
		&c.GuildID, &c.Enabled, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return c, nil
}

func (r *CompanyDiscordCredentialRepo) Get(ctx context.Context, companyID string) (*domain.CompanyDiscordCredential, error) {
	q := `SELECT ` + discordCredColumns + ` FROM company_discord_credentials WHERE company_id = $1`
	c, err := scanDiscordCred(r.db.QueryRowContext(ctx, q, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CompanyDiscordCredentialRepo) GetByApplicationID(ctx context.Context, applicationID string) (*domain.CompanyDiscordCredential, error) {
	q := `SELECT ` + discordCredColumns + ` FROM company_discord_credentials WHERE application_id = $1`
	c, err := scanDiscordCred(r.db.QueryRowContext(ctx, q, applicationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CompanyDiscordCredentialRepo) Upsert(ctx context.Context, c *domain.CompanyDiscordCredential) error {
	const q = `
		INSERT INTO company_discord_credentials
			(company_id, application_id, public_key, bot_token_encrypted, guild_id, enabled)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)
		ON CONFLICT (company_id) DO UPDATE
		SET application_id       = EXCLUDED.application_id,
		    public_key           = EXCLUDED.public_key,
		    bot_token_encrypted  = EXCLUDED.bot_token_encrypted,
		    guild_id             = EXCLUDED.guild_id,
		    enabled              = EXCLUDED.enabled,
		    updated_at           = now()
		RETURNING created_at, updated_at`
	if err := r.db.QueryRowContext(ctx, q,
		c.CompanyID, c.ApplicationID, c.PublicKey, c.BotTokenEncrypted, c.GuildID, c.Enabled,
	).Scan(&c.CreatedAt, &c.UpdatedAt); err != nil {
		return fmt.Errorf("upsert discord credential: %w", err)
	}
	return nil
}

func (r *CompanyDiscordCredentialRepo) Delete(ctx context.Context, companyID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM company_discord_credentials WHERE company_id = $1`, companyID)
	return err
}

func (r *CompanyDiscordCredentialRepo) ListEnabled(ctx context.Context) ([]*domain.CompanyDiscordCredential, error) {
	q := `SELECT ` + discordCredColumns + ` FROM company_discord_credentials WHERE enabled = true`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CompanyDiscordCredential
	for rows.Next() {
		c, err := scanDiscordCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var _ domain.CompanyDiscordCredentialRepository = (*CompanyDiscordCredentialRepo)(nil)
