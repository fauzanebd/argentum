package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// CompanyProfileRepo persists what business a workspace is (T-B1).
type CompanyProfileRepo struct{ db *sql.DB }

func NewCompanyProfileRepo(db *sql.DB) *CompanyProfileRepo { return &CompanyProfileRepo{db: db} }

// GetByCompany returns the company's profile, or domain.ErrNotFound when it
// has none. Absence is the ordinary case — it is what every tenant had before
// this table existed — so the caller reads it as "no block", never as an error.
func (r *CompanyProfileRepo) GetByCompany(ctx context.Context, companyID string) (*domain.CompanyProfile, error) {
	const q = `
		SELECT company_id, industry, description, context_notes,
		       fiscal_year_start_month, source, inferred_at, updated_by,
		       created_at, updated_at
		FROM company_profiles WHERE company_id = $1
	`
	p := &domain.CompanyProfile{}
	var inferredAt sql.NullTime
	var updatedBy sql.NullString
	err := r.db.QueryRowContext(ctx, q, companyID).Scan(
		&p.CompanyID, &p.Industry, &p.Description, &p.ContextNotes,
		&p.FiscalYearStartMonth, &p.Source, &inferredAt, &updatedBy,
		&p.CreatedAt, &p.UpdatedAt,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, domain.ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("get company profile: %w", err)
	}
	if inferredAt.Valid {
		t := inferredAt.Time
		p.InferredAt = &t
	}
	p.UpdatedBy = updatedBy.String
	return p, nil
}

// Upsert writes the profile, creating the row on first save.
//
// ON CONFLICT rather than a read-then-write: two admins saving the same form
// at once should produce one row and the later write, not a unique-violation
// one of them has to interpret. `created_at` is left alone by the update so it
// keeps meaning "when this company first described itself".
//
// The write is total, not a patch: every field the caller holds lands. The
// service is what decides which of them came from the tenant and which were
// carried over from the row already there, because that decision is where the
// provenance rule lives (locked decision 2) and it is not a SQL concern.
func (r *CompanyProfileRepo) Upsert(ctx context.Context, p *domain.CompanyProfile) error {
	const q = `
		INSERT INTO company_profiles (
			company_id, industry, description, context_notes,
			fiscal_year_start_month, source, inferred_at, updated_by, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid, now())
		ON CONFLICT (company_id) DO UPDATE SET
			industry = EXCLUDED.industry,
			description = EXCLUDED.description,
			context_notes = EXCLUDED.context_notes,
			fiscal_year_start_month = EXCLUDED.fiscal_year_start_month,
			source = EXCLUDED.source,
			inferred_at = EXCLUDED.inferred_at,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
		RETURNING created_at, updated_at
	`
	var inferredAt any
	if p.InferredAt != nil {
		inferredAt = *p.InferredAt
	}
	err := r.db.QueryRowContext(ctx, q,
		p.CompanyID, p.Industry, p.Description, p.ContextNotes,
		p.FiscalYearStartMonth, string(p.Source), inferredAt, p.UpdatedBy,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert company profile: %w", err)
	}
	return nil
}
