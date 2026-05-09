package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/fauzanebd/argentum/internal/domain"
)

type DocumentRepo struct{ db *sql.DB }

func NewDocumentRepo(db *sql.DB) *DocumentRepo { return &DocumentRepo{db: db} }

// Insert persists a new document row. If d.ID is empty the DB generates
// a UUID; otherwise the provided id is used (lets callers upload to a
// deterministic key before persisting metadata).
func (r *DocumentRepo) Insert(ctx context.Context, d *domain.Document) error {
	const q = `
		INSERT INTO documents (id, company_id, thread_id, message_id, format, filename, storage_key, size_bytes)
		VALUES (
			COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()),
			$2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8
		)
		RETURNING id, created_at
	`
	return r.db.QueryRowContext(ctx, q,
		d.ID, d.CompanyID, d.ThreadID, d.MessageID,
		string(d.Format), d.Filename, d.StorageKey, d.SizeBytes,
	).Scan(&d.ID, &d.CreatedAt)
}

func (r *DocumentRepo) GetByID(ctx context.Context, id string) (*domain.Document, error) {
	const q = `
		SELECT id, company_id, thread_id, COALESCE(message_id::text, ''),
		       format, filename, storage_key, size_bytes, created_at
		FROM documents WHERE id = $1
	`
	d := &domain.Document{}
	var format string
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&d.ID, &d.CompanyID, &d.ThreadID, &d.MessageID,
		&format, &d.Filename, &d.StorageKey, &d.SizeBytes, &d.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Format = domain.DocumentFormat(format)
	return d, nil
}

func (r *DocumentRepo) ListByThread(ctx context.Context, threadID string) ([]*domain.Document, error) {
	const q = `
		SELECT id, company_id, thread_id, COALESCE(message_id::text, ''),
		       format, filename, storage_key, size_bytes, created_at
		FROM documents WHERE thread_id = $1 ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Document
	for rows.Next() {
		d := &domain.Document{}
		var format string
		if err := rows.Scan(
			&d.ID, &d.CompanyID, &d.ThreadID, &d.MessageID,
			&format, &d.Filename, &d.StorageKey, &d.SizeBytes, &d.CreatedAt,
		); err != nil {
			return nil, err
		}
		d.Format = domain.DocumentFormat(format)
		out = append(out, d)
	}
	return out, rows.Err()
}
