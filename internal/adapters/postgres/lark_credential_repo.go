package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type CompanyLarkCredentialRepo struct{ db *sql.DB }

func NewCompanyLarkCredentialRepo(db *sql.DB) *CompanyLarkCredentialRepo {
	return &CompanyLarkCredentialRepo{db: db}
}

const larkCredColumns = `company_id, app_id, app_secret_encrypted, verification_token,
	COALESCE(encrypt_key, ''), COALESCE(bot_open_id, ''), enabled, created_at, updated_at`

type larkCredScanner interface {
	Scan(dest ...interface{}) error
}

func scanLarkCred(row larkCredScanner) (*domain.CompanyLarkCredential, error) {
	c := &domain.CompanyLarkCredential{}
	if err := row.Scan(
		&c.CompanyID, &c.AppID, &c.AppSecretEncrypted, &c.VerificationToken,
		&c.EncryptKey, &c.BotOpenID, &c.Enabled, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return c, nil
}

func (r *CompanyLarkCredentialRepo) Get(ctx context.Context, companyID string) (*domain.CompanyLarkCredential, error) {
	q := `SELECT ` + larkCredColumns + ` FROM company_lark_credentials WHERE company_id = $1`
	c, err := scanLarkCred(r.db.QueryRowContext(ctx, q, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CompanyLarkCredentialRepo) GetByAppID(ctx context.Context, appID string) (*domain.CompanyLarkCredential, error) {
	q := `SELECT ` + larkCredColumns + ` FROM company_lark_credentials WHERE app_id = $1`
	c, err := scanLarkCred(r.db.QueryRowContext(ctx, q, appID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *CompanyLarkCredentialRepo) Upsert(ctx context.Context, c *domain.CompanyLarkCredential) error {
	const q = `
		INSERT INTO company_lark_credentials
			(company_id, app_id, app_secret_encrypted, verification_token, encrypt_key, bot_open_id, enabled)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7)
		ON CONFLICT (company_id) DO UPDATE
		SET app_id                = EXCLUDED.app_id,
		    app_secret_encrypted  = EXCLUDED.app_secret_encrypted,
		    verification_token    = EXCLUDED.verification_token,
		    encrypt_key           = EXCLUDED.encrypt_key,
		    bot_open_id           = EXCLUDED.bot_open_id,
		    enabled               = EXCLUDED.enabled,
		    updated_at            = now()
		RETURNING created_at, updated_at`
	if err := r.db.QueryRowContext(ctx, q,
		c.CompanyID, c.AppID, c.AppSecretEncrypted, c.VerificationToken,
		c.EncryptKey, c.BotOpenID, c.Enabled,
	).Scan(&c.CreatedAt, &c.UpdatedAt); err != nil {
		return fmt.Errorf("upsert lark credential: %w", err)
	}
	return nil
}

func (r *CompanyLarkCredentialRepo) Delete(ctx context.Context, companyID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM company_lark_credentials WHERE company_id = $1`, companyID)
	return err
}

func (r *CompanyLarkCredentialRepo) ListEnabled(ctx context.Context) ([]*domain.CompanyLarkCredential, error) {
	q := `SELECT ` + larkCredColumns + ` FROM company_lark_credentials WHERE enabled = true`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.CompanyLarkCredential
	for rows.Next() {
		c, err := scanLarkCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var _ domain.CompanyLarkCredentialRepository = (*CompanyLarkCredentialRepo)(nil)
