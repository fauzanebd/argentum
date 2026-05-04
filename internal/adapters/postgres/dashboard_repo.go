package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/fauzanebd/argentum/internal/domain"
)

type DashboardRepo struct{ db *sql.DB }

func NewDashboardRepo(db *sql.DB) *DashboardRepo { return &DashboardRepo{db: db} }

func (r *DashboardRepo) Create(ctx context.Context, d *domain.SavedDashboard) error {
	const q = `
		INSERT INTO saved_dashboards (company_id, thread_id, metabase_dashboard_id, name, public_url)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`
	return r.db.QueryRowContext(ctx, q,
		d.CompanyID, d.ThreadID, d.MetabaseDashboardID, d.Name, d.PublicURL,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
}

func (r *DashboardRepo) GetByID(ctx context.Context, id string) (*domain.SavedDashboard, error) {
	const q = `
		SELECT id, company_id, thread_id, metabase_dashboard_id, name, public_url, created_at, updated_at
		FROM saved_dashboards WHERE id = $1
	`
	d := &domain.SavedDashboard{}
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&d.ID, &d.CompanyID, &d.ThreadID, &d.MetabaseDashboardID,
		&d.Name, &d.PublicURL, &d.CreatedAt, &d.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return d, err
}

func (r *DashboardRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.SavedDashboard, error) {
	const q = `
		SELECT id, company_id, thread_id, metabase_dashboard_id, name, public_url, created_at, updated_at
		FROM saved_dashboards WHERE company_id = $1 ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.SavedDashboard
	for rows.Next() {
		d := &domain.SavedDashboard{}
		if err := rows.Scan(
			&d.ID, &d.CompanyID, &d.ThreadID, &d.MetabaseDashboardID,
			&d.Name, &d.PublicURL, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *DashboardRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM saved_dashboards WHERE id = $1`, id)
	return err
}

func (r *DashboardRepo) DeleteByThread(ctx context.Context, threadID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM saved_dashboards WHERE thread_id = $1`, threadID)
	return err
}
