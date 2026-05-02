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
		INSERT INTO companies (name, slug)
		VALUES ($1, $2)
		RETURNING id, created_at
	`
	if err := r.db.QueryRowContext(ctx, q, c.Name, c.Slug).Scan(&c.ID, &c.CreatedAt); err != nil {
		return fmt.Errorf("insert company: %w", err)
	}
	return nil
}

func (r *CompanyRepo) GetByID(ctx context.Context, id string) (*domain.Company, error) {
	const q = `SELECT id, name, slug, created_at FROM companies WHERE id = $1`
	c := &domain.Company{}
	if err := r.db.QueryRowContext(ctx, q, id).Scan(&c.ID, &c.Name, &c.Slug, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (r *CompanyRepo) GetBySlug(ctx context.Context, slug string) (*domain.Company, error) {
	const q = `SELECT id, name, slug, created_at FROM companies WHERE slug = $1`
	c := &domain.Company{}
	if err := r.db.QueryRowContext(ctx, q, slug).Scan(&c.ID, &c.Name, &c.Slug, &c.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (r *CompanyRepo) Update(ctx context.Context, c *domain.Company) error {
	const q = `UPDATE companies SET name = $1, slug = $2 WHERE id = $3`
	_, err := r.db.ExecContext(ctx, q, c.Name, c.Slug, c.ID)
	return err
}
