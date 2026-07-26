package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type CompanyLLMCredentialRepo struct{ db *sql.DB }

func NewCompanyLLMCredentialRepo(db *sql.DB) *CompanyLLMCredentialRepo {
	return &CompanyLLMCredentialRepo{db: db}
}

const llmCredColumns = `id, company_id, tier, interface, model, base_url,
    api_key_encrypted, created_at, updated_at`

func scanLLMCred(row interface {
	Scan(dest ...interface{}) error
}) (*domain.CompanyLLMCredential, error) {
	c := &domain.CompanyLLMCredential{}
	var iface, model, base sql.NullString
	var key []byte
	if err := row.Scan(
		&c.ID, &c.CompanyID, &c.Tier, &iface, &model, &base, &key,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	c.Interface = iface.String
	c.Model = model.String
	c.BaseURL = base.String
	c.APIKeyEncrypted = key
	return c, nil
}

func (r *CompanyLLMCredentialRepo) GetByCompany(ctx context.Context, companyID string) ([]*domain.CompanyLLMCredential, error) {
	q := `SELECT ` + llmCredColumns + ` FROM company_llm_credentials WHERE company_id = $1`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("query llm credentials: %w", err)
	}
	defer rows.Close()
	var out []*domain.CompanyLLMCredential
	for rows.Next() {
		c, err := scanLLMCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Upsert writes the row for (company_id, tier). Empty-string Interface /
// Model / BaseURL are stored as NULL so the resolver treats them as
// "fall back to env" per field. APIKeyEncrypted is stored as-is (nil =>
// fall back to env API key).
func (r *CompanyLLMCredentialRepo) Upsert(ctx context.Context, c *domain.CompanyLLMCredential) error {
	const q = `
		INSERT INTO company_llm_credentials
			(company_id, tier, interface, model, base_url, api_key_encrypted)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6)
		ON CONFLICT (company_id, tier) DO UPDATE
		SET interface         = EXCLUDED.interface,
			model             = EXCLUDED.model,
			base_url          = EXCLUDED.base_url,
			api_key_encrypted = EXCLUDED.api_key_encrypted,
			updated_at        = now()
		RETURNING id, created_at, updated_at`
	if err := r.db.QueryRowContext(ctx, q,
		c.CompanyID, string(c.Tier), c.Interface, c.Model, c.BaseURL, c.APIKeyEncrypted,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return fmt.Errorf("upsert llm credential: %w", err)
	}
	return nil
}

func (r *CompanyLLMCredentialRepo) Delete(ctx context.Context, companyID string, tier domain.LLMTier) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM company_llm_credentials WHERE company_id = $1 AND tier = $2`,
		companyID, string(tier))
	return err
}

var _ domain.CompanyLLMCredentialRepository = (*CompanyLLMCredentialRepo)(nil)
