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
	origin_message_id, model, uses, last_used_at, created_at,
	archived_at, archive_reason`

func scanQueryExample(s interface{ Scan(...any) error }, distance *float32) (*domain.QueryExample, error) {
	e := &domain.QueryExample{}
	var lastUsed, archivedAt sql.NullTime
	var reason sql.NullString
	targets := []any{
		&e.ID, &e.CompanyID, &e.SourceID, &e.Question, &e.SQL, &e.RowCount,
		&e.OriginMessageID, &e.Model, &e.Uses, &lastUsed, &e.CreatedAt,
		&archivedAt, &reason,
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
	if archivedAt.Valid {
		t := archivedAt.Time
		e.ArchivedAt = &t
	}
	e.ArchiveReason = reason.String
	return e, nil
}

// Upsert writes one example, replacing whatever the same origin turn taught
// before.
//
// **It deliberately leaves `archived_at` alone.** A conflict here means the
// harvester is re-reading the *same turn*, and if a sweep retired that turn's
// example because the table it queries no longer exists, re-learning the
// identical SQL would resurrect a query that still does not run. In practice
// the collision never happens — `ExistingOrigins` skips known origins before
// this is called — so this is the behaviour of the force path and of a future
// caller who forgets, which is exactly when it matters.
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
	// `archived_at IS NULL` matches the partial index 076 creates, so the live
	// half of a cookbook is scanned rather than the whole table.
	where := `WHERE company_id = $1 AND archived_at IS NULL`
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

// CountByCompany counts the live half. Its caller is the turn path asking "is
// there a cookbook at all", and an all-archived cookbook is not one.
func (r *QueryExampleRepo) CountByCompany(ctx context.Context, companyID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM query_examples WHERE company_id = $1 AND archived_at IS NULL`,
		companyID).Scan(&n)
	return n, err
}

func (r *QueryExampleRepo) CountArchivedByCompany(ctx context.Context, companyID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM query_examples WHERE company_id = $1 AND archived_at IS NOT NULL`,
		companyID).Scan(&n)
	return n, err
}

// LiveRefs reads the three fields a sweep needs, ordered by source so the
// caller can introspect each warehouse once (T-Q15).
func (r *QueryExampleRepo) LiveRefs(ctx context.Context, companyID string) ([]domain.QueryExampleRef, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, source_id, sql_text FROM query_examples
		 WHERE company_id = $1 AND archived_at IS NULL
		 ORDER BY source_id, id`, companyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.QueryExampleRef
	for rows.Next() {
		var ref domain.QueryExampleRef
		if err := rows.Scan(&ref.ID, &ref.SourceID, &ref.SQL); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// Archive retires examples by id.
//
// `archived_at IS NULL` in the predicate is what makes the returned count
// meaningful: without it a re-run would report the same rows again and a log
// line saying "archived 40 examples" would say that every night forever.
func (r *QueryExampleRepo) Archive(ctx context.Context, ids []int64, reason string, at time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE query_examples SET archived_at = $3, archive_reason = $2
		 WHERE id = ANY($1) AND archived_at IS NULL`,
		pq.Array(ids), reason, at)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ArchiveUnused retires the live examples of one company that have never been
// retrieved and are older than createdBefore.
//
// `uses = 0` and not `uses < n`: this is "nothing has ever matched it", which
// is a fact, where any positive threshold would be a quality judgement the
// cookbook has no signal for.
func (r *QueryExampleRepo) ArchiveUnused(
	ctx context.Context, companyID string, createdBefore, at time.Time,
) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE query_examples SET archived_at = $4, archive_reason = $3
		 WHERE company_id = $1 AND archived_at IS NULL
		   AND uses = 0 AND created_at < $2`,
		companyID, createdBefore, domain.ArchiveReasonUnused, at)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
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

func (r *QueryExampleRepo) CompaniesWithExamples(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT company_id::text FROM query_examples WHERE archived_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ExistingOrigins deliberately does **not** filter archived rows.
//
// Its job is to stop the harvester paying for an embedding it already paid
// for, and an archived example is still a turn this cookbook has learned from.
// Filtering here would make every harvest re-learn everything a sweep had
// retired the night before, and the two jobs would take turns undoing each
// other on a schedule.
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
