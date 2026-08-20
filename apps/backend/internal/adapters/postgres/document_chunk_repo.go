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
			 content, context_prefix, embedding, model, source_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
			c.Content, c.ContextPrefix, vec, c.Model, c.SourceName,
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

// conjunctiveTSQuery is what a question becomes by default: every term must be
// present.
//
// `plainto_tsquery` rather than `to_tsquery`: the input is a question a person
// or a model wrote, and `to_tsquery` refuses anything with an unescaped
// operator in it — a query containing "&" or a bare apostrophe would come back
// as a database error rather than as results.
const conjunctiveTSQuery = `plainto_tsquery('simple', $2)`

// disjunctiveTSQuery is the same question with the AND turned into OR (T-P14).
//
// It is built by rewriting `plainto_tsquery`'s own output rather than by
// splitting the string in Go, which keeps one tokenizer in the system: the
// terms, the stopword handling and the escaping are whatever Postgres decided,
// and only the operator changes. `plainto_tsquery` emits `&` and nothing else —
// phrase operators come from `phraseto_tsquery` — so the rewrite is total.
const disjunctiveTSQuery = `replace(plainto_tsquery('simple', $2)::text, ' & ', ' | ')::tsquery`

// SearchLexical is the tsvector half.
//
// **Conjunctive first, disjunctive only on nothing.** A question mixing
// languages — "Kopi Arabika 1kg faktur invoice" against Indonesian prose — has
// every term in the document except one, and a conjunctive tsquery answers that
// with zero rows rather than with a slightly worse match. Zero rows is the
// worst possible answer here: the model either says the document holds nothing
// (T-P14's first failure) or spends another iteration and another model call
// re-asking with fewer words. The fallback costs one more index scan on exactly
// the turns that currently cost a whole turn.
//
// The second return says the fallback fired, so the tool can tell the model its
// query was loosened. A weak match presented as a strong one is the only thing
// this retry can make worse.
func (r *DocumentChunkRepo) SearchLexical(
	ctx context.Context, companyID, documentID, query string, limit int,
) ([]*domain.DocumentChunkHit, bool, error) {
	if query == "" {
		return nil, false, nil
	}
	limit = boundLimit(limit)
	args := []any{companyID, query, limit}
	if documentID != "" {
		args = append(args, documentID)
	}

	hits, err := r.search(ctx, lexicalQuery(conjunctiveTSQuery, documentID != ""), args...)
	if err != nil || len(hits) > 0 {
		return hits, false, err
	}
	// A one-term question rewrites to itself, so this arm re-runs an identical
	// query against an index that just returned nothing. That is a second scan
	// of an empty result and no rows to rank — cheap enough not to be worth a
	// round trip to ask Postgres how many terms it found.
	hits, err = r.search(ctx, lexicalQuery(disjunctiveTSQuery, documentID != ""), args...)
	if err != nil {
		return nil, false, err
	}
	return hits, len(hits) > 0, nil
}

// lexicalQuery is the one SELECT both arms run, differing only in how the
// question was turned into a tsquery. Written as one function so the ranking,
// the join and the tenant scope cannot drift apart between the two paths.
func lexicalQuery(tsquery string, byDocument bool) string {
	where := `WHERE c.company_id = $1 AND c.tsv @@ ` + tsquery
	if byDocument {
		where += ` AND c.document_id = $4`
	}
	// ts_rank over a weighted tsvector, which is where "the filename ranks
	// below the content" is actually decided: migration 065 stores the prose at
	// weight A and the filename's terms at B, and ts_rank's default weights
	// (0.1, 0.2, 0.4, 1.0) do the rest. On the disjunctive arm the same
	// function is what makes the chunk matching the most terms win.
	return `SELECT ` + chunkColumns + `, ts_rank(c.tsv, ` + tsquery + `) AS rank
		FROM document_chunks c
		JOIN source_documents d ON d.id = c.document_id ` + where + `
		ORDER BY rank DESC
		LIMIT $3`
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
