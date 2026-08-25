package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// itoa shortens the placeholder arithmetic in ListByCompany. The alternative
// is a fmt.Sprintf per clause, which reads as string-built SQL at a glance —
// and in this file that is exactly the thing a reader should be able to rule
// out immediately. Only ever called with a placeholder index.
func itoa(n int) string { return strconv.Itoa(n) }

type DocumentRepo struct{ db *sql.DB }

func NewDocumentRepo(db *sql.DB) *DocumentRepo { return &DocumentRepo{db: db} }

// documentColumns is the SELECT list every read below shares, so a column
// added to the table is added to the scan in one place. thread_id and
// api_key_id are coalesced to the empty string because both are legitimately
// null — the
// render door has no thread, and a deleted key leaves its documents behind —
// and a nullable string in the domain type would push a nil check onto every
// caller for a value that has one obvious empty form.
const documentColumns = `
	id, company_id, COALESCE(thread_id::text, ''), COALESCE(message_id::text, ''),
	format, filename, storage_key, size_bytes, source,
	COALESCE(api_key_id::text, ''), created_at`

// maxDocumentPage bounds a page. A caller asking for 10 000 rows gets 200,
// silently: the cursor is what makes the rest reachable, and refusing the
// request would only make them ask twice.
const maxDocumentPage = 200

// defaultDocumentPage is what an unspecified limit means.
const defaultDocumentPage = 25

func scanDocument(s interface{ Scan(...any) error }) (*domain.Document, error) {
	d := &domain.Document{}
	var format, source string
	if err := s.Scan(
		&d.ID, &d.CompanyID, &d.ThreadID, &d.MessageID,
		&format, &d.Filename, &d.StorageKey, &d.SizeBytes, &source,
		&d.APIKeyID, &d.CreatedAt,
	); err != nil {
		return nil, err
	}
	d.Format = domain.DocumentFormat(format)
	d.Source = domain.DocumentSource(source)
	return d, nil
}

// Insert persists a new document row. If d.ID is empty the DB generates
// a UUID; otherwise the provided id is used (lets callers upload to a
// deterministic key before persisting metadata).
func (r *DocumentRepo) Insert(ctx context.Context, d *domain.Document) error {
	const q = `
		INSERT INTO documents (id, company_id, thread_id, message_id, format, filename, storage_key, size_bytes, source, api_key_id)
		VALUES (
			COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()),
			$2, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5, $6, $7, $8,
			COALESCE(NULLIF($9, ''), 'agent'), NULLIF($10, '')::uuid
		)
		RETURNING id, created_at
	`
	return r.db.QueryRowContext(ctx, q,
		d.ID, d.CompanyID, d.ThreadID, d.MessageID,
		string(d.Format), d.Filename, d.StorageKey, d.SizeBytes,
		string(d.Source), d.APIKeyID,
	).Scan(&d.ID, &d.CreatedAt)
}

func (r *DocumentRepo) GetByID(ctx context.Context, id string) (*domain.Document, error) {
	q := `SELECT ` + documentColumns + ` FROM documents WHERE id = $1`
	d, err := scanDocument(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// GetForCompany is GetByID with the tenant boundary inside the query. `/v1`
// uses only this one: an id belonging to another tenant must be a not-found,
// and a handler that fetches first and compares afterwards is one forgotten
// comparison away from a cross-tenant read.
func (r *DocumentRepo) GetForCompany(ctx context.Context, companyID, id string) (*domain.Document, error) {
	q := `SELECT ` + documentColumns + ` FROM documents WHERE id = $1 AND company_id = $2`
	d, err := scanDocument(r.db.QueryRowContext(ctx, q, id, companyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// ListByCompany returns one page newest-first plus whether another exists.
//
// Keyset, never offset: rows arrive while a caller pages, and with an offset a
// document created during the walk shifts everything down one — the caller
// sees an item twice or misses one entirely. The predicate compares the
// (created_at, id) pair rather than created_at alone because two documents can
// share a microsecond, and comparing only the timestamp drops every row that
// ties with the last one on the previous page.
func (r *DocumentRepo) ListByCompany(ctx context.Context, companyID string, f domain.DocumentFilter) ([]*domain.Document, bool, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultDocumentPage
	}
	if limit > maxDocumentPage {
		limit = maxDocumentPage
	}

	var where strings.Builder
	where.WriteString(` WHERE company_id = $1`)
	args := []any{companyID}
	add := func(clause string, v any) {
		args = append(args, v)
		where.WriteString(clause)
	}
	if f.Format != "" {
		add(` AND format = $`+itoa(len(args)+1), string(f.Format))
	}
	if !f.From.IsZero() {
		add(` AND created_at >= $`+itoa(len(args)+1), f.From)
	}
	if !f.To.IsZero() {
		add(` AND created_at < $`+itoa(len(args)+1), f.To)
	}
	if f.CursorID != "" && !f.CursorTime.IsZero() {
		args = append(args, f.CursorTime, f.CursorID)
		where.WriteString(` AND (created_at, id) < ($` + itoa(len(args)-1) + `, $` + itoa(len(args)) + `::uuid)`)
	}

	// One more row than asked for, discarded before returning: it is what
	// makes has_more a fact rather than the guess `len(rows) == limit` gives,
	// and it costs one row instead of a second COUNT query.
	args = append(args, limit+1)
	q := `SELECT ` + documentColumns + ` FROM documents` + where.String() +
		` ORDER BY created_at DESC, id DESC LIMIT $` + itoa(len(args))

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]*domain.Document, 0, limit)
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

func (r *DocumentRepo) ListByThread(ctx context.Context, threadID string) ([]*domain.Document, error) {
	q := `SELECT ` + documentColumns + ` FROM documents WHERE thread_id = $1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
