package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// MetricRepo stores the metric registry (T-06).
//
// Every method that names a metric takes the company id beside it, like
// MCPServerRepo: the id is a bare uuid on an admin-only surface, and a
// repository that will answer for any company is one forgotten check from a
// cross-tenant read of the tenant's own SQL.
type MetricRepo struct{ db *sql.DB }

func NewMetricRepo(db *sql.DB) *MetricRepo { return &MetricRepo{db: db} }

const metricColumns = `id, company_id, source_id, key, label, description, sql_template,
	value_column, grain, unit, COALESCE(currency, ''), higher_is_better, enabled,
	COALESCE(created_by::text, ''), created_at, updated_at`

func scanMetric(row interface {
	Scan(dest ...interface{}) error
}) (*domain.MetricDefinition, error) {
	m := &domain.MetricDefinition{}
	var grain, unit string
	if err := row.Scan(
		&m.ID, &m.CompanyID, &m.SourceID, &m.Key, &m.Label, &m.Description, &m.SQLTemplate,
		&m.ValueColumn, &grain, &unit, &m.Currency, &m.HigherIsBetter, &m.Enabled,
		&m.CreatedBy, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	m.Grain = domain.MetricGrain(grain)
	m.Unit = domain.MetricUnit(unit)
	return m, nil
}

func (r *MetricRepo) Create(ctx context.Context, m *domain.MetricDefinition) error {
	const q = `
		INSERT INTO metric_definitions (
			company_id, source_id, key, label, description, sql_template, value_column,
			grain, unit, currency, higher_is_better, enabled, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11, $12, NULLIF($13, '')::uuid
		)
		RETURNING id, created_at, updated_at`
	err := r.db.QueryRowContext(ctx, q,
		m.CompanyID, m.SourceID, m.Key, m.Label, m.Description, m.SQLTemplate, m.ValueColumn,
		string(m.Grain), string(m.Unit), m.Currency, m.HigherIsBetter, m.Enabled, m.CreatedBy,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
	if err != nil && uniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert metric: %w", err)
	}
	return nil
}

func (r *MetricRepo) GetByID(ctx context.Context, companyID, id string) (*domain.MetricDefinition, error) {
	q := `SELECT ` + metricColumns + ` FROM metric_definitions WHERE company_id = $1 AND id = $2`
	m, err := scanMetric(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return m, err
}

func (r *MetricRepo) GetByKey(ctx context.Context, companyID, key string) (*domain.MetricDefinition, error) {
	q := `SELECT ` + metricColumns + ` FROM metric_definitions WHERE company_id = $1 AND key = $2`
	m, err := scanMetric(r.db.QueryRowContext(ctx, q, companyID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return m, err
}

func (r *MetricRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.MetricDefinition, error) {
	return r.list(ctx, `WHERE company_id = $1`, companyID)
}

func (r *MetricRepo) ListEnabled(ctx context.Context, companyID string) ([]*domain.MetricDefinition, error) {
	return r.list(ctx, `WHERE company_id = $1 AND enabled`, companyID)
}

func (r *MetricRepo) list(ctx context.Context, where string, args ...interface{}) ([]*domain.MetricDefinition, error) {
	q := `SELECT ` + metricColumns + ` FROM metric_definitions ` + where + ` ORDER BY lower(label)`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list metrics: %w", err)
	}
	defer rows.Close()

	out := []*domain.MetricDefinition{}
	for rows.Next() {
		m, err := scanMetric(rows)
		if err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *MetricRepo) Update(ctx context.Context, m *domain.MetricDefinition) error {
	const q = `
		UPDATE metric_definitions
		   SET source_id = $3, key = $4, label = $5, description = $6, sql_template = $7,
		       value_column = $8, grain = $9, unit = $10, currency = NULLIF($11, ''),
		       higher_is_better = $12, enabled = $13, updated_at = now()
		 WHERE company_id = $1 AND id = $2
		RETURNING updated_at`
	err := r.db.QueryRowContext(ctx, q,
		m.CompanyID, m.ID, m.SourceID, m.Key, m.Label, m.Description, m.SQLTemplate,
		m.ValueColumn, string(m.Grain), string(m.Unit), m.Currency, m.HigherIsBetter, m.Enabled,
	).Scan(&m.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.ErrNotFound
	case err != nil && uniqueViolation(err):
		return domain.ErrAlreadyExists
	case err != nil:
		return fmt.Errorf("update metric: %w", err)
	}
	return nil
}

func (r *MetricRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM metric_definitions WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete metric: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
