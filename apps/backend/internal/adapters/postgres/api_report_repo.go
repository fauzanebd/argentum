package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// APIReportRepo persists the report jobs `/v1` hands back (T-A2).
type APIReportRepo struct{ db *sql.DB }

func NewAPIReportRepo(db *sql.DB) *APIReportRepo { return &APIReportRepo{db: db} }

const apiReportColumns = `
	id, company_id, COALESCE(api_key_id::text, ''), kind, status, format, prompt,
	COALESCE(thread_id::text, ''), COALESCE(document_id::text, ''),
	callback_url, error, request_id, created_at, completed_at`

func scanAPIReport(s interface{ Scan(...any) error }) (*domain.APIReport, error) {
	r := &domain.APIReport{}
	var kind, status, format string
	var completedAt sql.NullTime
	if err := s.Scan(
		&r.ID, &r.CompanyID, &r.APIKeyID, &kind, &status, &format, &r.Prompt,
		&r.ThreadID, &r.DocumentID, &r.CallbackURL, &r.Error, &r.RequestID,
		&r.CreatedAt, &completedAt,
	); err != nil {
		return nil, err
	}
	r.Kind = domain.APIReportKind(kind)
	r.Status = domain.APIReportStatus(status)
	r.Format = domain.DocumentFormat(format)
	if completedAt.Valid {
		t := completedAt.Time
		r.CompletedAt = &t
	}
	return r, nil
}

func (r *APIReportRepo) Create(ctx context.Context, rep *domain.APIReport) error {
	const q = `
		INSERT INTO api_reports (id, company_id, api_key_id, kind, status, format, prompt, thread_id, callback_url, request_id)
		VALUES (
			COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()),
			$2, NULLIF($3, '')::uuid, $4, $5, $6, $7, NULLIF($8, '')::uuid, $9, $10
		)
		RETURNING id, created_at
	`
	if rep.Status == "" {
		rep.Status = domain.APIReportQueued
	}
	return r.db.QueryRowContext(ctx, q,
		rep.ID, rep.CompanyID, rep.APIKeyID, string(rep.Kind), string(rep.Status),
		string(rep.Format), rep.Prompt, rep.ThreadID, rep.CallbackURL, rep.RequestID,
	).Scan(&rep.ID, &rep.CreatedAt)
}

// GetForCompany scopes by tenant in the query. A report id from another
// company is a not-found, not a row the handler is trusted to reject.
func (r *APIReportRepo) GetForCompany(ctx context.Context, companyID, id string) (*domain.APIReport, error) {
	q := `SELECT ` + apiReportColumns + ` FROM api_reports WHERE id = $1 AND company_id = $2`
	rep, err := scanAPIReport(r.db.QueryRowContext(ctx, q, id, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// Get is the worker's read. It holds a report id off a queue payload and has
// no company to scope by until it has the row — the scoping it needs is that
// the id came from a payload this system wrote, not from a caller.
func (r *APIReportRepo) Get(ctx context.Context, id string) (*domain.APIReport, error) {
	q := `SELECT ` + apiReportColumns + ` FROM api_reports WHERE id = $1`
	rep, err := scanAPIReport(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// AttachThread records the conversation the turn landed on.
//
// `thread_id IS NULL` in the WHERE clause, so an asynq retry that re-resolves
// to a *different* thread cannot move a job whose stream a caller is already
// attached to.
func (r *APIReportRepo) AttachThread(ctx context.Context, id, threadID string) error {
	const q = `UPDATE api_reports SET thread_id = $2::uuid WHERE id = $1 AND thread_id IS NULL`
	_, err := r.db.ExecContext(ctx, q, id, threadID)
	return err
}

// MarkRunning is best-effort progress reporting. The WHERE clause refuses to
// move a terminal job back: an asynq retry of a task whose first attempt
// already completed would otherwise reopen a finished report.
func (r *APIReportRepo) MarkRunning(ctx context.Context, id string) error {
	const q = `UPDATE api_reports SET status = 'running' WHERE id = $1 AND status = 'queued'`
	_, err := r.db.ExecContext(ctx, q, id)
	return err
}

// Complete writes the terminal state in one statement.
//
// The `NOT status IN (...)` guard makes this idempotent under an asynq retry,
// which matters because the callback fires off the transition: a second
// Complete that updated a row would send a tenant a second `report.completed`
// for one report.
func (r *APIReportRepo) Complete(ctx context.Context, id string, status domain.APIReportStatus, documentID, errMsg string, at time.Time) error {
	const q = `
		UPDATE api_reports
		SET status = $2, document_id = NULLIF($3, '')::uuid, error = $4, completed_at = $5
		WHERE id = $1 AND status NOT IN ('completed', 'failed')
	`
	_, err := r.db.ExecContext(ctx, q, id, string(status), documentID, errMsg, at.UTC())
	return err
}
