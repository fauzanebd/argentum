package postgres

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/fauzanebd/argentum/internal/domain"
)

// QueryExampleRepo persists the per-tenant query cookbook (T-Q8). Cosine
// distance (`<=>`) drives TopK, matching TableEmbeddingRepo.
type QueryExampleRepo struct{ db *sql.DB }

func NewQueryExampleRepo(db *sql.DB) *QueryExampleRepo { return &QueryExampleRepo{db: db} }

const queryExampleColumns = `
	id, company_id, source_id, question, sql_text, row_count,
	origin_message_id, model, uses, last_used_at, created_at`

func scanQueryExample(s interface{ Scan(...any) error }, distance *float32) (*domain.QueryExample, error) {
	e := &domain.QueryExample{}
	var lastUsed sql.NullTime
	targets := []any{
		&e.ID, &e.CompanyID, &e.SourceID, &e.Question, &e.SQL, &e.RowCount,
		&e.OriginMessageID, &e.Model, &e.Uses, &lastUsed, &e.CreatedAt,
	}
	if distance != nil {
		targets = append(targets, distance)
	}
	if err := s.Scan(targets...); err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		e.LastUsedAt = &t
	}
	return e, nil
}

func (r *QueryExampleRepo) Upsert(ctx context.Context, e *domain.QueryExample) error {
	const q = `
		INSERT INTO query_examples
			(company_id, source_id, question, sql_text, row_count, origin_message_id, embedding, model)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (origin_message_id) DO UPDATE
			SET question   = EXCLUDED.question,
			    sql_text   = EXCLUDED.sql_text,
			    row_count  = EXCLUDED.row_count,
			    embedding  = EXCLUDED.embedding,
			    model      = EXCLUDED.model,
			    created_at = now()
		RETURNING id, created_at`
	return r.db.QueryRowContext(ctx, q,
		e.CompanyID, e.SourceID, e.Question, e.SQL, e.RowCount,
		e.OriginMessageID, pgvector.NewVector(e.Embedding), e.Model,
	).Scan(&e.ID, &e.CreatedAt)
}

// TopK returns the closest examples, optionally narrowed to the sources this
// turn may read.
//
// The source filter is a permission, not an optimisation: an agent scoped away
// from the HR warehouse must not be shown queries against it, or its own
// prompt would carry that warehouse's table and column names — a scope that
// leaks the schema it is meant to hide is not a scope (T-S2).
func (r *QueryExampleRepo) TopK(
	ctx context.Context, companyID string, sourceIDs []string, queryVec []float32, k int,
) ([]domain.QueryExampleHit, error) {
	if k <= 0 || k > 20 {
		k = 3
	}
	vec := pgvector.NewVector(queryVec)
	args := []any{companyID, vec, k}
	where := `WHERE company_id = $1`
	if len(sourceIDs) > 0 {
		where += ` AND source_id = ANY($4)`
		args = append(args, pq.Array(sourceIDs))
	}
	q := `SELECT ` + queryExampleColumns + `, embedding <=> $2 AS distance
		FROM query_examples ` + where + `
		ORDER BY embedding <=> $2
		LIMIT $3`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.QueryExampleHit
	for rows.Next() {
		var d float32
		e, err := scanQueryExample(rows, &d)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.QueryExampleHit{QueryExample: *e, Distance: d})
	}
	return out, rows.Err()
}

func (r *QueryExampleRepo) CountByCompany(ctx context.Context, companyID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM query_examples WHERE company_id = $1`, companyID).Scan(&n)
	return n, err
}

// MarkUsed is fire-and-forget bookkeeping. It runs on the turn path, so it is
// one statement over an id array rather than a loop.
func (r *QueryExampleRepo) MarkUsed(ctx context.Context, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE query_examples SET uses = uses + 1, last_used_at = $2 WHERE id = ANY($1)`,
		pq.Array(ids), at)
	return err
}

func (r *QueryExampleRepo) ExistingOrigins(ctx context.Context, companyID string, messageIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	ids := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT origin_message_id::text FROM query_examples
		 WHERE company_id = $1 AND origin_message_id = ANY($2)`,
		companyID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (r *QueryExampleRepo) DeleteByCompany(ctx context.Context, companyID string) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM query_examples WHERE company_id = $1`, companyID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
