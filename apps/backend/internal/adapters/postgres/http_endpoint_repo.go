package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// HTTPEndpointRepo stores a company's registered http_action endpoints (T-12b).
//
// Like MCPServerRepo, every method carries the company id beside the row id: the
// ids are bare uuids in an admin-only URL, and a repository that answers for any
// company is one forgotten check from a cross-tenant read. It stores and returns
// the header template sealed — the bytes are what an admin submitted encrypted,
// and this layer never opens them. Decryption belongs to the turn-time resolver.
type HTTPEndpointRepo struct{ db *sql.DB }

func NewHTTPEndpointRepo(db *sql.DB) *HTTPEndpointRepo { return &HTTPEndpointRepo{db: db} }

const httpEndpointColumns = `id, company_id, name, method, url_template,
		header_encrypted, body_template, created_by, created_at, updated_at`

func scanHTTPEndpoint(row interface {
	Scan(dest ...interface{}) error
}) (*domain.HTTPEndpoint, error) {
	e := &domain.HTTPEndpoint{}
	var header []byte
	var createdBy sql.NullString
	if err := row.Scan(
		&e.ID, &e.CompanyID, &e.Name, &e.Method, &e.URLTemplate,
		&header, &e.BodyTemplate, &createdBy, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	e.HeaderEncrypted = header
	// Derived on read, never stored: the column is the fact, and a boolean beside
	// it that could disagree is a partial-write bug in waiting.
	e.HasHeader = len(header) > 0
	if createdBy.Valid {
		e.CreatedBy = createdBy.String
	}
	return e, nil
}

func (r *HTTPEndpointRepo) Create(ctx context.Context, e *domain.HTTPEndpoint) error {
	const q = `
		INSERT INTO http_endpoints (company_id, name, method, url_template, header_encrypted, body_template, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`
	var createdBy interface{}
	if e.CreatedBy != "" {
		createdBy = e.CreatedBy
	}
	err := r.db.QueryRowContext(ctx, q,
		e.CompanyID, e.Name, e.Method, e.URLTemplate, e.HeaderEncrypted, e.BodyTemplate, createdBy,
	).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil && uniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert http endpoint: %w", err)
	}
	e.HasHeader = len(e.HeaderEncrypted) > 0
	return nil
}

func (r *HTTPEndpointRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.HTTPEndpoint, error) {
	q := `SELECT ` + httpEndpointColumns + ` FROM http_endpoints WHERE company_id = $1 ORDER BY lower(name)`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("list http endpoints: %w", err)
	}
	defer rows.Close()

	out := []*domain.HTTPEndpoint{}
	for rows.Next() {
		e, err := scanHTTPEndpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("scan http endpoint: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *HTTPEndpointRepo) GetByID(ctx context.Context, companyID, id string) (*domain.HTTPEndpoint, error) {
	q := `SELECT ` + httpEndpointColumns + ` FROM http_endpoints WHERE company_id = $1 AND id = $2`
	e, err := scanHTTPEndpoint(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return e, err
}

// GetByName is the turn-time lookup: http_action resolves the name the agent
// proposed to the row an admin registered. Company-scoped like the rest.
func (r *HTTPEndpointRepo) GetByName(ctx context.Context, companyID, name string) (*domain.HTTPEndpoint, error) {
	q := `SELECT ` + httpEndpointColumns + ` FROM http_endpoints WHERE company_id = $1 AND name = $2`
	e, err := scanHTTPEndpoint(r.db.QueryRowContext(ctx, q, companyID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return e, err
}

func (r *HTTPEndpointRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM http_endpoints WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete http endpoint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
