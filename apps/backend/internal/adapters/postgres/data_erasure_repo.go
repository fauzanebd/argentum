package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// DataErasureRepo persists the written completion record (T-H6).
//
// There is no Delete and there will not be one. The table's whole value is
// that it outlives what it describes, and a route that erases the evidence of
// an erasure answers a regulator's question with silence.
type DataErasureRepo struct {
	db *sql.DB
}

// NewDataErasureRepo builds the repository over the control database.
func NewDataErasureRepo(db *sql.DB) *DataErasureRepo { return &DataErasureRepo{db: db} }

// Begin writes the `running` row and fills in e.ID.
//
// Before the delete, not after. A process that dies mid-erasure then leaves a
// row that says an erasure was attempted and did not finish — which is the
// state somebody needs to know about — rather than leaving nothing and letting
// a half-deleted tenant look untouched.
func (r *DataErasureRepo) Begin(ctx context.Context, e *domain.DataErasure) error {
	const q = `
		INSERT INTO data_erasures (company_id, requested_by, scope, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id, requested_at`
	var requestedBy any
	if e.RequestedBy != "" {
		requestedBy = e.RequestedBy
	}
	if e.Status == "" {
		e.Status = domain.ErasureStatusRunning
	}
	if err := r.db.QueryRowContext(ctx, q, e.CompanyID, requestedBy, string(e.Scope), e.Status).
		Scan(&e.ID, &e.RequestedAt); err != nil {
		return fmt.Errorf("begin erasure record: %w", err)
	}
	return nil
}

// Complete closes a row with what actually went.
func (r *DataErasureRepo) Complete(ctx context.Context, id string, threads, messages int) error {
	const q = `
		UPDATE data_erasures
		SET status = $1, threads_deleted = $2, messages_deleted = $3, completed_at = now()
		WHERE id = $4`
	_, err := r.db.ExecContext(ctx, q, domain.ErasureStatusCompleted, threads, messages, id)
	if err != nil {
		return fmt.Errorf("complete erasure record: %w", err)
	}
	return nil
}

// Fail closes a row with the reason it did not finish.
func (r *DataErasureRepo) Fail(ctx context.Context, id, reason string) error {
	const q = `
		UPDATE data_erasures
		SET status = $1, error_text = $2, completed_at = now()
		WHERE id = $3`
	_, err := r.db.ExecContext(ctx, q, domain.ErasureStatusFailed, reason, id)
	if err != nil {
		return fmt.Errorf("fail erasure record: %w", err)
	}
	return nil
}

// ListByCompany returns a company's history newest-first.
func (r *DataErasureRepo) ListByCompany(ctx context.Context, companyID string, limit int) ([]*domain.DataErasure, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT id, company_id, COALESCE(requested_by::text, ''), scope, status,
		       threads_deleted, messages_deleted, COALESCE(error_text, ''),
		       requested_at, completed_at
		FROM data_erasures
		WHERE company_id = $1
		ORDER BY requested_at DESC
		LIMIT $2`
	rows, err := r.db.QueryContext(ctx, q, companyID, limit)
	if err != nil {
		return nil, fmt.Errorf("list erasures: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.DataErasure, 0, limit)
	for rows.Next() {
		e := &domain.DataErasure{}
		if err := rows.Scan(
			&e.ID, &e.CompanyID, &e.RequestedBy, &e.Scope, &e.Status,
			&e.ThreadsDeleted, &e.MessagesDeleted, &e.ErrorText,
			&e.RequestedAt, &e.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan erasure: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
