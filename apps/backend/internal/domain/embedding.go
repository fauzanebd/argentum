package domain

import "context"

// TableEmbedding is one row in the table_embeddings store: a single table's
// doc string and its vector representation, scoped to a source.
type TableEmbedding struct {
	SourceID  string
	TableName string
	DocText   string
	DocHash   string
	Model     string
	Embedding []float32
}

// TableHit is the result of a TopK similarity search.
type TableHit struct {
	TableName string
	Distance  float32
}

// TableEmbeddingRepository persists per-source table embeddings and serves
// cosine-similarity lookups at query time. Implementations are expected to
// use pgvector (cosine distance via the `<=>` operator).
type TableEmbeddingRepository interface {
	// DeleteBySource removes every embedding row for a source. Called at the
	// start of a reindex so stale tables don't linger.
	DeleteBySource(ctx context.Context, sourceID string) error
	// UpsertBatch inserts/updates a batch of embeddings atomically. Use
	// within the same tx as DeleteBySource for clean swaps.
	UpsertBatch(ctx context.Context, companyID string, items []TableEmbedding) error
	// CountBySource returns the number of stored embeddings for a source.
	// Chat runner uses 0 to skip injection silently.
	CountBySource(ctx context.Context, sourceID string) (int, error)
	// TopK returns up to k tables ranked by cosine distance to queryVec
	// (smaller distance = better match).
	TopK(ctx context.Context, sourceID string, queryVec []float32, k int) ([]TableHit, error)
}
