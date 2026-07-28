package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type CompanyRepo struct{ db *sql.DB }

func NewCompanyRepo(db *sql.DB) *CompanyRepo { return &CompanyRepo{db: db} }

func (r *CompanyRepo) Create(ctx context.Context, c *domain.Company) error {
	const q = `
		INSERT INTO companies (name, slug, default_currency)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`
	currency := c.DefaultCurrency
	if currency == "" {
		currency = "USD"
	}
	if err := r.db.QueryRowContext(ctx, q, c.Name, c.Slug, currency).Scan(&c.ID, &c.CreatedAt); err != nil {
		return fmt.Errorf("insert company: %w", err)
	}
	c.DefaultCurrency = currency
	return nil
}

func (r *CompanyRepo) GetByID(ctx context.Context, id string) (*domain.Company, error) {
	const q = `SELECT id, name, slug, default_currency, created_at FROM companies WHERE id = $1`
	c := &domain.Company{}
	if err := r.db.QueryRowContext(ctx, q, id).Scan(&c.ID, &c.Name, &c.Slug, &c.DefaultCurrency, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (r *CompanyRepo) GetBySlug(ctx context.Context, slug string) (*domain.Company, error) {
	const q = `SELECT id, name, slug, default_currency, created_at FROM companies WHERE slug = $1`
	c := &domain.Company{}
	if err := r.db.QueryRowContext(ctx, q, slug).Scan(&c.ID, &c.Name, &c.Slug, &c.DefaultCurrency, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (r *CompanyRepo) Update(ctx context.Context, c *domain.Company) error {
	const q = `UPDATE companies SET name = $1, slug = $2, default_currency = $3 WHERE id = $4`
	_, err := r.db.ExecContext(ctx, q, c.Name, c.Slug, c.DefaultCurrency, c.ID)
	return err
}

// GetBranding reads the report branding record. A company that has never
// configured one has `{}` in the column (migration 022's default), which
// unmarshals into the zero value — so this returns a usable branding and never
// a nil pointer for a company that exists.
func (r *CompanyRepo) GetBranding(ctx context.Context, companyID string) (*domain.ReportBranding, error) {
	const q = `SELECT report_branding FROM companies WHERE id = $1`
	var raw []byte
	if err := r.db.QueryRowContext(ctx, q, companyID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	b := &domain.ReportBranding{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, b); err != nil {
			// A branding row that will not parse must not take the document
			// down with it: the renderer's whole contract is that branding is
			// optional. Report it so it can be fixed, and render Argentum's
			// defaults meanwhile.
			return nil, fmt.Errorf("decode report_branding for company %s: %w", companyID, err)
		}
	}
	return b, nil
}

// SaveBranding writes the whole record. It touches one column, so it does not
// race the name/slug/currency writes going through Update.
func (r *CompanyRepo) SaveBranding(ctx context.Context, companyID string, b *domain.ReportBranding) error {
	if b == nil {
		b = &domain.ReportBranding{}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("encode report_branding: %w", err)
	}
	const q = `UPDATE companies SET report_branding = $1 WHERE id = $2`
	res, err := r.db.ExecContext(ctx, q, raw, companyID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
