package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/doctable"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentTableRepo persists the tables found inside uploaded PDFs (T-P6).
type DocumentTableRepo struct{ db *sql.DB }

func NewDocumentTableRepo(db *sql.DB) *DocumentTableRepo { return &DocumentTableRepo{db: db} }

const documentTableColumns = `
	id, document_id, company_id, title, table_name, first_page, last_page,
	columns, status, verify_status, verify_detail, row_count, candidate_key,
	applied_by, applied_at, created_at, updated_at`

func scanDocumentTable(s interface{ Scan(...any) error }) (*domain.DocumentTable, error) {
	t := &domain.DocumentTable{}
	var (
		cols      []byte
		appliedBy sql.NullString
		appliedAt sql.NullTime
	)
	if err := s.Scan(
		&t.ID, &t.DocumentID, &t.CompanyID, &t.Title, &t.TableName, &t.FirstPage, &t.LastPage,
		&cols, &t.Status, &t.VerifyStatus, &t.VerifyDetail, &t.RowCount, &t.CandidateKey,
		&appliedBy, &appliedAt, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(cols) > 0 {
		if err := json.Unmarshal(cols, &t.Columns); err != nil {
			// A column list that will not decode is a row this product wrote and
			// can no longer read. Returned rather than swallowed: an empty
			// column list would make the table look like a draft of nothing, and
			// a reviewer would apply it.
			return nil, fmt.Errorf("decode document table columns: %w", err)
		}
	}
	if appliedBy.Valid {
		t.AppliedBy = appliedBy.String
	}
	if appliedAt.Valid {
		at := appliedAt.Time
		t.AppliedAt = &at
	}
	return t, nil
}

// Upsert writes a draft, keyed on the candidate it came from.
//
// The conflict clause is the interesting half. A re-parse updates the geometry,
// the row count and the verification — the facts about the extraction — and
// leaves `title`, `columns` and `status` alone when the row has already been
// applied. A reviewer's decisions survive a better parser; an applied table is
// not silently unpublished because the document was read again.
func (r *DocumentTableRepo) Upsert(ctx context.Context, t *domain.DocumentTable) error {
	cols, err := json.Marshal(t.Columns)
	if err != nil {
		return fmt.Errorf("encode document table columns: %w", err)
	}
	const q = `
		INSERT INTO document_tables
			(document_id, company_id, title, table_name, first_page, last_page,
			 columns, status, verify_status, verify_detail, row_count, candidate_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (document_id, candidate_key) DO UPDATE SET
			first_page    = EXCLUDED.first_page,
			last_page     = EXCLUDED.last_page,
			row_count     = EXCLUDED.row_count,
			verify_status = EXCLUDED.verify_status,
			verify_detail = EXCLUDED.verify_detail,
			-- A reviewer's title and typing win over a re-parse's inference.
			title   = CASE WHEN document_tables.status = 'draft' THEN EXCLUDED.title ELSE document_tables.title END,
			columns = CASE WHEN document_tables.status = 'draft' THEN EXCLUDED.columns ELSE document_tables.columns END,
			-- Quarantine is the one status a re-parse may impose on a draft: it
			-- is a fact about the numbers, not an opinion about them.
			status = CASE
				WHEN document_tables.status = 'applied' THEN 'applied'
				WHEN EXCLUDED.verify_status = 'quarantined' THEN 'quarantined'
				ELSE 'draft'
			END,
			updated_at = now()
		RETURNING ` + documentTableColumns
	row := r.db.QueryRowContext(ctx, q,
		t.DocumentID, t.CompanyID, t.Title, t.TableName, t.FirstPage, t.LastPage,
		cols, string(t.Status), string(t.VerifyStatus), t.VerifyDetail, t.RowCount, t.CandidateKey,
	)
	out, err := scanDocumentTable(row)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code.Name() == "unique_violation" {
		// A table name another document already claimed. Returned as a typed
		// error because the caller's answer is to pick a different name rather
		// than to fail: two documents both holding a table called "penjualan"
		// is the ordinary case, not a conflict anybody should have to resolve.
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("upsert document table: %w", err)
	}
	*t = *out
	return nil
}

func (r *DocumentTableRepo) GetForCompany(ctx context.Context, companyID, id string) (*domain.DocumentTable, error) {
	q := `SELECT ` + documentTableColumns + `
		FROM document_tables WHERE company_id = $1 AND id = $2`
	t, err := scanDocumentTable(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get document table: %w", err)
	}
	return t, nil
}

func (r *DocumentTableRepo) ListByDocument(ctx context.Context, companyID, documentID string) ([]*domain.DocumentTable, error) {
	q := `SELECT ` + documentTableColumns + `
		FROM document_tables
		WHERE company_id = $1 AND document_id = $2
		ORDER BY first_page, candidate_key`
	return r.list(ctx, q, companyID, documentID)
}

func (r *DocumentTableRepo) ListAppliedByCompany(ctx context.Context, companyID string) ([]*domain.DocumentTable, error) {
	q := `SELECT ` + documentTableColumns + `
		FROM document_tables
		WHERE company_id = $1 AND status = 'applied'
		ORDER BY applied_at DESC NULLS LAST`
	return r.list(ctx, q, companyID)
}

func (r *DocumentTableRepo) list(ctx context.Context, q string, args ...any) ([]*domain.DocumentTable, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list document tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.DocumentTable
	for rows.Next() {
		t, err := scanDocumentTable(rows)
		if err != nil {
			return nil, fmt.Errorf("scan document table: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateColumns saves the reviewer's typing decision and title.
//
// It cannot move the status, and that is the point: "save my column change" and
// "publish this table" are two different acts, and a save path that could
// publish is a path where a mis-click reaches the warehouse.
func (r *DocumentTableRepo) UpdateColumns(
	ctx context.Context, companyID, id, title string, cols []doctable.Column,
) error {
	blob, err := json.Marshal(cols)
	if err != nil {
		return fmt.Errorf("encode document table columns: %w", err)
	}
	const q = `
		UPDATE document_tables
		SET title = COALESCE(NULLIF($3, ''), title), columns = $4, updated_at = now()
		WHERE company_id = $1 AND id = $2`
	res, err := r.db.ExecContext(ctx, q, companyID, id, title, blob)
	if err != nil {
		return fmt.Errorf("update document table columns: %w", err)
	}
	return oneRowAffected(res)
}

// MarkApplied records the publish.
func (r *DocumentTableRepo) MarkApplied(ctx context.Context, companyID, id, userID string, rowCount int) error {
	const q = `
		UPDATE document_tables
		SET status = 'applied', applied_by = NULLIF($3, '')::uuid, applied_at = now(),
		    row_count = $4, updated_at = now()
		WHERE company_id = $1 AND id = $2 AND verify_status <> 'quarantined'`
	res, err := r.db.ExecContext(ctx, q, companyID, id, userID, rowCount)
	if err != nil {
		return fmt.Errorf("mark document table applied: %w", err)
	}
	// The `verify_status <> 'quarantined'` in the WHERE clause is the last of
	// the three places quarantine is enforced — the service checks it, the
	// handler checks it, and this makes the database refuse it too. Three
	// checks for one rule is right where the rule is "do not publish a table
	// whose figures are known to be wrong".
	return oneRowAffected(res)
}

func (r *DocumentTableRepo) SetVerification(
	ctx context.Context, companyID, id string, status doctable.VerifyStatus, detail string,
) error {
	const q = `
		UPDATE document_tables
		SET verify_status = $3, verify_detail = $4,
		    status = CASE
		        WHEN $3 = 'quarantined' THEN 'quarantined'
		        WHEN status = 'quarantined' THEN 'draft'
		        ELSE status
		    END,
		    updated_at = now()
		WHERE company_id = $1 AND id = $2`
	res, err := r.db.ExecContext(ctx, q, companyID, id, string(status), detail)
	if err != nil {
		return fmt.Errorf("set document table verification: %w", err)
	}
	return oneRowAffected(res)
}

func (r *DocumentTableRepo) Delete(ctx context.Context, companyID, id string) error {
	const q = `DELETE FROM document_tables WHERE company_id = $1 AND id = $2`
	res, err := r.db.ExecContext(ctx, q, companyID, id)
	if err != nil {
		return fmt.Errorf("delete document table: %w", err)
	}
	return oneRowAffected(res)
}

// oneRowAffected turns "the UPDATE matched nothing" into ErrNotFound, which is
// what every caller means by it: the id is another tenant's, or it is gone.
func oneRowAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
