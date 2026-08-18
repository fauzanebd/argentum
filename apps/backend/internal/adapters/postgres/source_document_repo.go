package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SourceDocumentRepo persists the PDFs a tenant uploaded (T-P1).
type SourceDocumentRepo struct{ db *sql.DB }

func NewSourceDocumentRepo(db *sql.DB) *SourceDocumentRepo { return &SourceDocumentRepo{db: db} }

const sourceDocumentColumns = `
	id, company_id, filename, content_sha256, byte_size, page_count,
	storage_key, status, status_detail, uploaded_by, created_at, updated_at`

func scanSourceDocument(s interface{ Scan(...any) error }) (*domain.SourceDocument, error) {
	d := &domain.SourceDocument{}
	var uploadedBy sql.NullString
	if err := s.Scan(
		&d.ID, &d.CompanyID, &d.Filename, &d.ContentSHA256, &d.ByteSize, &d.PageCount,
		&d.StorageKey, &d.Status, &d.StatusDetail, &uploadedBy, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if uploadedBy.Valid {
		d.UploadedBy = uploadedBy.String
	}
	return d, nil
}

// Create inserts the row and fills in the generated id and timestamps.
//
// A duplicate (company_id, content_sha256) comes back as ErrAlreadyExists rather
// than as a driver error, because the service treats it as "somebody uploaded
// this file twice at once" and answers with the row that won rather than with a
// failure.
func (r *SourceDocumentRepo) Create(ctx context.Context, d *domain.SourceDocument) error {
	const q = `
		INSERT INTO source_documents
			(company_id, filename, content_sha256, byte_size, page_count,
			 storage_key, status, status_detail, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::uuid)
		RETURNING id, created_at, updated_at`
	err := r.db.QueryRowContext(ctx, q,
		d.CompanyID, d.Filename, d.ContentSHA256, d.ByteSize, d.PageCount,
		d.StorageKey, string(d.Status), d.StatusDetail, d.UploadedBy,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code.Name() == "unique_violation" {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert source document: %w", err)
	}
	return nil
}

func (r *SourceDocumentRepo) GetForCompany(ctx context.Context, companyID, id string) (*domain.SourceDocument, error) {
	q := `SELECT ` + sourceDocumentColumns + `
		FROM source_documents WHERE company_id = $1 AND id = $2`
	d, err := scanSourceDocument(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read source document: %w", err)
	}
	return d, nil
}

func (r *SourceDocumentRepo) GetBySHA(ctx context.Context, companyID, sha256 string) (*domain.SourceDocument, error) {
	q := `SELECT ` + sourceDocumentColumns + `
		FROM source_documents WHERE company_id = $1 AND content_sha256 = $2`
	d, err := scanSourceDocument(r.db.QueryRowContext(ctx, q, companyID, sha256))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read source document by hash: %w", err)
	}
	return d, nil
}

func (r *SourceDocumentRepo) ListByCompany(ctx context.Context, companyID string, limit, offset int) ([]*domain.SourceDocument, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT ` + sourceDocumentColumns + `
		FROM source_documents WHERE company_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryContext(ctx, q, companyID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list source documents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]*domain.SourceDocument, 0, limit)
	for rows.Next() {
		d, err := scanSourceDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source document: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateStatus is the worker's write.
//
// The page count is applied through a GREATEST rather than assigned, so a later
// status change that carries no count cannot zero one an earlier pass
// established — a document that reaches 'failed' on page 40 of 40 should not
// forget that it has 40 pages.
func (r *SourceDocumentRepo) UpdateStatus(
	ctx context.Context, id string, status domain.SourceDocumentStatus, detail string, pageCount int,
) error {
	if pageCount < 0 {
		pageCount = 0
	}
	const q = `
		UPDATE source_documents
		   SET status = $2, status_detail = $3,
		       page_count = GREATEST(page_count, $4),
		       updated_at = now()
		 WHERE id = $1`
	res, err := r.db.ExecContext(ctx, q, id, string(status), detail, pageCount)
	if err != nil {
		return fmt.Errorf("update source document status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SourceDocumentRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM source_documents WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete source document: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
