package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// TableEmbeddingRepo persists per-source table embeddings in the control
// Postgres DB via pgvector. Cosine distance (`<=>`) drives TopK.
type TableEmbeddingRepo struct{ db *sql.DB }

func NewTableEmbeddingRepo(db *sql.DB) *TableEmbeddingRepo {
	return &TableEmbeddingRepo{db: db}
}

func (r *TableEmbeddingRepo) DeleteBySource(ctx context.Context, sourceID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM table_embeddings WHERE source_id = $1`, sourceID)
	return err
}

// UpsertBatch wraps DeleteBySource + per-row INSERT in a single transaction,
// so a partial failure leaves the previous embeddings untouched. Per-row
// inserts are fine at our scale (hundreds of tables, not millions).
func (r *TableEmbeddingRepo) UpsertBatch(ctx context.Context, companyID string, items []domain.TableEmbedding) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	const q = `
		INSERT INTO table_embeddings
			(company_id, source_id, table_name, doc_text, doc_hash, embedding, model)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source_id, table_name) DO UPDATE
			SET doc_text  = EXCLUDED.doc_text,
				doc_hash  = EXCLUDED.doc_hash,
				embedding = EXCLUDED.embedding,
				model     = EXCLUDED.model,
				created_at = now()
	`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, it := range items {
		vec := pgvector.NewVector(it.Embedding)
		if _, err := stmt.ExecContext(ctx,
			companyID, it.SourceID, it.TableName, it.DocText, it.DocHash, vec, it.Model,
		); err != nil {
			return fmt.Errorf("upsert table %q: %w", it.TableName, err)
		}
	}
	return tx.Commit()
}

func (r *TableEmbeddingRepo) CountBySource(ctx context.Context, sourceID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM table_embeddings WHERE source_id = $1`, sourceID,
	).Scan(&n)
	return n, err
}

// TopK uses cosine distance (`<=>`); ivfflat index defined in migration 011
// makes this an approximate-nearest-neighbour lookup at scale.
func (r *TableEmbeddingRepo) TopK(ctx context.Context, sourceID string, queryVec []float32, k int) ([]domain.TableHit, error) {
	if k <= 0 {
		return nil, nil
	}
	start := time.Now()
	vec := pgvector.NewVector(queryVec)
	const q = `
		SELECT table_name, (embedding <=> $1)::float4 AS distance
		FROM table_embeddings
		WHERE source_id = $2
		ORDER BY embedding <=> $1
		LIMIT $3
	`
	rows, err := r.db.QueryContext(ctx, q, vec, sourceID, k)
	if err != nil {
		return nil, fmt.Errorf("topk query: %w", err)
	}
	defer rows.Close()
	var out []domain.TableHit
	for rows.Next() {
		var h domain.TableHit
		if err := rows.Scan(&h.TableName, &h.Distance); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"source_id":   sourceID,
		"k":           k,
		"rows":        len(out),
		"duration_ms": time.Since(start).Milliseconds(),
	}).Debug("table_embeddings: TopK query")
	return out, nil
}
