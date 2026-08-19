package domain

import (
	"context"
	"time"
)

// DocumentChunk is one retrievable piece of a document's prose (T-P8).
//
// **It reaches a turn through a tool and never through the prompt.** That is
// Decision 6 of the PDF roadmap, and the reason is the instrument stack: a
// chunk injected into the user's message is not in `returned`, so a figure the
// model quotes out of it is invisible to `CheckGrounding` — unfalsifiable
// rather than merely unchecked. `search_documents` (T-P9) puts these where the
// checks can see them.
type DocumentChunk struct {
	ID          int64  `json:"id"`
	DocumentID  string `json:"document_id"`
	CompanyID   string `json:"company_id"`
	Ordinal     int    `json:"ordinal"`
	PageFrom    int    `json:"page_from"`
	PageTo      int    `json:"page_to"`
	HeadingPath string `json:"heading_path,omitempty"`
	Content     string `json:"content"`
	// ContextPrefix is one generated sentence situating this chunk in its
	// document, written once at ingest on the light model and stored. It is
	// embedded with the content and shown to nobody: it exists to make
	// retrieval find the right chunk, not to be quoted.
	ContextPrefix string `json:"context_prefix,omitempty"`
	// Embedding is nil where this deployment has no embedding credentials. The
	// lexical half still answers, which is why the column is nullable and this
	// field is a slice rather than a fixed array.
	Embedding []float32 `json:"-"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// DocumentChunkHit is one retrieved chunk and why it was retrieved.
type DocumentChunkHit struct {
	DocumentChunk
	// Filename and DocumentTitle come from the join, because a citation names
	// the document a person recognises rather than a uuid.
	Filename string `json:"filename"`
	// Score is the merged rank, not a distance: the dense and lexical halves
	// are on incomparable scales, so what is carried is what reciprocal-rank
	// fusion produced. Higher is better, and the absolute value means nothing
	// outside one result set.
	Score float64 `json:"score"`
	// Matched says which half found it — "dense", "lexical" or "both". It is
	// in the tool result because a chunk only the lexical half found is a
	// literal term match, and that is worth different confidence from a
	// semantic neighbour.
	Matched string `json:"matched"`
}

// DocumentChunkRepository persists and retrieves chunks.
type DocumentChunkRepository interface {
	// ReplaceForDocument writes a document's chunks, removing whatever was
	// there before. Replace rather than append: a re-parse produces the same
	// prose, and appending would leave retrieval choosing between two copies of
	// every paragraph — and quoting one of them twice.
	ReplaceForDocument(ctx context.Context, companyID, documentID string, chunks []*DocumentChunk) error
	// SearchDense is the vector half. Returns nothing when the deployment has
	// no embeddings, rather than erroring: the lexical half is a complete
	// answer on its own.
	SearchDense(ctx context.Context, companyID, documentID string, query []float32, limit int) ([]*DocumentChunkHit, error)
	// SearchLexical is the tsvector half — exact terms, clause numbers, product
	// codes: everything a dense vector is worst at and a contract is full of.
	SearchLexical(ctx context.Context, companyID, documentID, query string, limit int) ([]*DocumentChunkHit, error)
	CountForDocument(ctx context.Context, companyID, documentID string) (int, error)
	DeleteForDocument(ctx context.Context, companyID, documentID string) error
}
