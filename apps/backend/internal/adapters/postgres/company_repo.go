package postgres

import (
	"context"
	"database/sql"
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
