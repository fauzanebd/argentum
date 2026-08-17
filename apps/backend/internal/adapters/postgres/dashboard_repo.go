package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/dashboard/spec"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DashboardRepo persists native dashboards (056).
//
// Every statement carries company_id in its WHERE clause. The repository this
// replaces took an id alone and left ownership to a comparison in the service,
// which is a check that works until somebody writes a second caller — so a
// dashboard belonging to another tenant is not "found and refused" here, it is
// simply not found, and there is no way to express the unscoped read.
type DashboardRepo struct{ db *sql.DB }

func NewDashboardRepo(db *sql.DB) *DashboardRepo { return &DashboardRepo{db: db} }

const dashboardColumns = `id, company_id, thread_id, source_id, title, description,
	spec, spec_version, refresh_secs, created_by, created_at, updated_at`

func (r *DashboardRepo) Create(ctx context.Context, d *domain.Dashboard) error {
	blob, err := json.Marshal(d.Spec)
	if err != nil {
		return fmt.Errorf("encode dashboard spec: %w", err)
	}
	const q = `
		INSERT INTO dashboards (company_id, thread_id, source_id, title, description, spec, spec_version, refresh_secs, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at
	`
	return r.db.QueryRowContext(ctx, q,
		d.CompanyID, d.ThreadID, d.SourceID, d.Title, d.Description,
		blob, d.SpecVersion, d.RefreshSecs, d.CreatedBy,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
}

// Update rewrites a dashboard in place. company_id is in the WHERE clause rather
// than checked beforehand, so a mis-scoped update writes nothing and says so.
func (r *DashboardRepo) Update(ctx context.Context, d *domain.Dashboard) error {
	blob, err := json.Marshal(d.Spec)
	if err != nil {
		return fmt.Errorf("encode dashboard spec: %w", err)
	}
	const q = `
		UPDATE dashboards
		SET source_id = $3, title = $4, description = $5, spec = $6, spec_version = $7,
		    refresh_secs = $8, updated_at = now()
		WHERE id = $1 AND company_id = $2
		RETURNING updated_at
	`
	err = r.db.QueryRowContext(ctx, q,
		d.ID, d.CompanyID, d.SourceID, d.Title, d.Description, blob, d.SpecVersion, d.RefreshSecs,
	).Scan(&d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func (r *DashboardRepo) GetByID(ctx context.Context, companyID, id string) (*domain.Dashboard, error) {
	q := `SELECT ` + dashboardColumns + ` FROM dashboards WHERE id = $1 AND company_id = $2`
	row := r.db.QueryRowContext(ctx, q, id, companyID)
	d, err := scanDashboard(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return d, err
}

func (r *DashboardRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.Dashboard, error) {
	q := `SELECT ` + dashboardColumns + ` FROM dashboards WHERE company_id = $1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Dashboard
	for rows.Next() {
		d, err := scanDashboard(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *DashboardRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM dashboards WHERE id = $1 AND company_id = $2`, id, companyID)
	if err != nil {
		return err
	}
	// A delete that matched nothing is reported rather than swallowed: the two
	// cases it hides are "already gone" and "belongs to somebody else", and a
	// silent success would answer 204 to both.
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanDashboard reads one row through whichever Scan the caller has — sql.Row's
// or sql.Rows' — so the column list and the spec decode live in one place.
func scanDashboard(scan func(...any) error) (*domain.Dashboard, error) {
	d := &domain.Dashboard{}
	var blob []byte
	if err := scan(
		&d.ID, &d.CompanyID, &d.ThreadID, &d.SourceID, &d.Title, &d.Description,
		&blob, &d.SpecVersion, &d.RefreshSecs, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	var s spec.Dashboard
	if err := json.Unmarshal(blob, &s); err != nil {
		return nil, fmt.Errorf("decode dashboard %s spec: %w", d.ID, err)
	}
	d.Spec = s
	return d, nil
}
