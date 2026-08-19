package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentChunkRepo persists what a document says, and retrieves it two ways
// (T-P8).
type DocumentChunkRepo struct{ db *sql.DB }

func NewDocumentChunkRepo(db *sql.DB) *DocumentChunkRepo { return &DocumentChunkRepo{db: db} }

// ReplaceForDocument writes a document's chunks in one transaction.
//
// Delete-then-insert inside a transaction rather than an upsert per ordinal:
// a re-ingest can produce a *different number* of chunks — a better parser
// finds a heading the old one missed — and an upsert keyed on ordinal would
// leave the tail of the previous run behind, quietly, as chunks that belong to
// no section of the current document.
func (r *DocumentChunkRepo) ReplaceForDocument(
	ctx context.Context, companyID, documentID string, chunks []*domain.DocumentChunk,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin chunk replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM document_chunks WHERE company_id = $1 AND document_id = $2`,
		companyID, documentID,
	); err != nil {
		return fmt.Errorf("clear previous chunks: %w", err)
	}

	const q = `
		INSERT INTO document_chunks
			(document_id, company_id, ordinal, page_from, page_to, heading_path,
			 content, context_prefix, embedding, model)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at`
	for _, c := range chunks {
		var vec any
		if len(c.Embedding) > 0 {
			// NULL rather than a zero vector where the deployment has no
			// embeddings. A zero vector is equidistant from everything and
			// would return the same three chunks for every question, which
			// looks like retrieval working.
			vec = pgvector.NewVector(c.Embedding)
		}
		if err := tx.QueryRowContext(ctx, q,
			documentID, companyID, c.Ordinal, c.PageFrom, c.PageTo, c.HeadingPath,
			c.Content, c.ContextPrefix, vec, c.Model,
		).Scan(&c.ID, &c.CreatedAt); err != nil {
			return fmt.Errorf("insert chunk %d: %w", c.Ordinal, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chunk replace: %w", err)
	}
	return nil
}

const chunkColumns = `c.id, c.document_id, c.company_id, c.ordinal, c.page_from, c.page_to,
	c.heading_path, c.content, c.context_prefix, c.model, c.created_at, d.filename`

func scanChunkHit(rows *sql.Rows, score *float64) (*domain.DocumentChunkHit, error) {
	h := &domain.DocumentChunkHit{}
	if err := rows.Scan(
		&h.ID, &h.DocumentID, &h.CompanyID, &h.Ordinal, &h.PageFrom, &h.PageTo,
		&h.HeadingPath, &h.Content, &h.ContextPrefix, &h.Model, &h.CreatedAt, &h.Filename,
		score,
	); err != nil {
		return nil, err
	}
	return h, nil
}

// SearchDense is the vector half.
func (r *DocumentChunkRepo) SearchDense(
	ctx context.Context, companyID, documentID string, query []float32, limit int,
) ([]*domain.DocumentChunkHit, error) {
	if len(query) == 0 {
		return nil, nil
	}
	limit = boundLimit(limit)
	args := []any{companyID, pgvector.NewVector(query), limit}
	where := `WHERE c.company_id = $1 AND c.embedding IS NOT NULL`
	if documentID != "" {
		where += ` AND c.document_id = $4`
		args = append(args, documentID)
	}
	q := `SELECT ` + chunkColumns + `, c.embedding <=> $2 AS distance
		FROM document_chunks c
		JOIN source_documents d ON d.id = c.document_id ` + where + `
		ORDER BY c.embedding <=> $2
		LIMIT $3`
	return r.search(ctx, q, args...)
}

// SearchLexical is the tsvector half.
//
// `plainto_tsquery` rather than `to_tsquery`: the input is a question a person
// or a model wrote, and `to_tsquery` refuses anything with an unescaped
// operator in it — a query containing "&" or a bare apostrophe would come back
// as a database error rather than as results.
func (r *DocumentChunkRepo) SearchLexical(
	ctx context.Context, companyID, documentID, query string, limit int,
) ([]*domain.DocumentChunkHit, error) {
	if query == "" {
		return nil, nil
	}
	limit = boundLimit(limit)
	args := []any{companyID, query, limit}
	where := `WHERE c.company_id = $1 AND c.tsv @@ plainto_tsquery('simple', $2)`
	if documentID != "" {
		where += ` AND c.document_id = $4`
		args = append(args, documentID)
	}
	q := `SELECT ` + chunkColumns + `, ts_rank(c.tsv, plainto_tsquery('simple', $2)) AS rank
		FROM document_chunks c
		JOIN source_documents d ON d.id = c.document_id ` + where + `
		ORDER BY rank DESC
		LIMIT $3`
	return r.search(ctx, q, args...)
}

func (r *DocumentChunkRepo) search(ctx context.Context, q string, args ...any) ([]*domain.DocumentChunkHit, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search document chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.DocumentChunkHit
	for rows.Next() {
		var score float64
		hit, err := scanChunkHit(rows, &score)
		if err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		hit.Score = score
		out = append(out, hit)
	}
	return out, rows.Err()
}

func (r *DocumentChunkRepo) CountForDocument(ctx context.Context, companyID, documentID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM document_chunks WHERE company_id = $1 AND document_id = $2`,
		companyID, documentID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count document chunks: %w", err)
	}
	return n, nil
}

// DeleteForDocument exists beside the ON DELETE CASCADE rather than instead of
// it: the cascade covers deleting the document, and this covers withdrawing its
// prose while keeping the file — which is what a re-ingest that finds nothing
// readable should leave behind.
func (r *DocumentChunkRepo) DeleteForDocument(ctx context.Context, companyID, documentID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM document_chunks WHERE company_id = $1 AND document_id = $2`,
		companyID, documentID)
	if err != nil {
		return fmt.Errorf("delete document chunks: %w", err)
	}
	return nil
}

// boundLimit keeps a caller — including a model choosing top_k — inside what
// the retrieval path can serve without turning a turn's prompt into a document.
func boundLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > 20 {
		return 20
	}
	return limit
}
