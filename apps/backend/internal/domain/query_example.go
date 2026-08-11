package domain

import (
	"context"
	"time"
)

// QueryExample is one worked example from a tenant's own history: a question
// somebody asked, and the SQL that answered it (T-Q8).
//
// It is the answer to a cost this product pays on every single turn — the
// agent rediscovering how this company's questions map onto this company's
// schema. The table picker narrows *which tables*; nothing until now carried
// forward that "revenue" means SUM(sales_amount) here, or that the fiscal year
// starts in April. All of it was already recorded in `agent_actions`; this is
// that history distilled into something a prompt can hold.
type QueryExample struct {
	ID        int64  `json:"id"`
	CompanyID string `json:"company_id"`
	// SourceID is which warehouse the query ran against. An example is only an
	// example for its own source: the same question against a different
	// database is a different answer, and offering the wrong dialect's SQL is
	// worse than offering none.
	SourceID string `json:"source_id"`

	Question string `json:"question"`
	SQL      string `json:"sql"`
	RowCount int    `json:"row_count"`

	// OriginMessageID is the turn this was learned from. It makes the example
	// auditable, and it is the unique key that stops a re-run of the harvester
	// writing the same turn twice.
	OriginMessageID string `json:"origin_message_id"`

	Embedding []float32 `json:"-"`
	Model     string    `json:"model"`

	Uses       int        `json:"uses"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// QueryExampleHit is one retrieved example and how close it was.
type QueryExampleHit struct {
	QueryExample
	Distance float32 `json:"distance"`
}

// QueryExampleMaxSQLChars bounds the SQL an example may carry into a prompt.
//
// Three examples at this size is roughly 2,400 characters of context, which is
// the same order as the source catalog block a turn already pays for. A query
// longer than this is usually a report query rather than the answer to a
// question, and a bad example to imitate.
const QueryExampleMaxSQLChars = 800

// QueryExampleRepository persists the cookbook and serves it at turn time.
// Implementations use pgvector cosine distance, like TableEmbeddingRepository.
type QueryExampleRepository interface {
	// Upsert records one example, replacing whatever was learned from the same
	// origin turn.
	Upsert(ctx context.Context, e *QueryExample) error
	// TopK returns up to k examples for one company, ranked by cosine distance
	// to queryVec. sourceIDs narrows to the sources this turn may read: an
	// agent scoped away from a warehouse must not be shown queries against it,
	// which would leak that warehouse's table names into its prompt. Empty
	// means every source the company has.
	TopK(ctx context.Context, companyID string, sourceIDs []string, queryVec []float32, k int) ([]QueryExampleHit, error)
	// CountByCompany is how the turn-time path skips the work silently for a
	// tenant with no cookbook, which is every tenant until the harvester runs.
	CountByCompany(ctx context.Context, companyID string) (int, error)
	// MarkUsed records that these examples were retrieved. Bookkeeping for
	// whoever prunes the cookbook later; nothing reads it at turn time.
	MarkUsed(ctx context.Context, ids []int64, at time.Time) error
	// ExistingOrigins reports which of the given origin message ids the
	// cookbook already holds, so a harvest run does not re-embed what it
	// already learned. Batched, because the alternative is one round trip per
	// candidate.
	ExistingOrigins(ctx context.Context, companyID string, messageIDs []string) (map[string]bool, error)
	// DeleteByCompany empties a tenant's cookbook. The escape hatch for a
	// tenant whose schema changed underneath it, where every example is now
	// wrong and the fastest fix is to forget and re-harvest.
	DeleteByCompany(ctx context.Context, companyID string) (int, error)
}

// CookbookCandidate is one finished turn the harvester is considering (T-Q8):
// a question somebody asked, and the SQL that answered it.
//
// A candidate is not yet an example. It has passed the SQL-level filters —
// the query ran, returned rows, and a person asked for it — and has still to
// clear the two the service applies: nobody marked the answer wrong (T-Q2),
// and the cookbook does not already hold this turn.
type CookbookCandidate struct {
	// MessageID is the USER message — the question. It is what
	// agent_actions.message_id points at, and what becomes
	// QueryExample.OriginMessageID.
	MessageID string
	// AnswerMessageID is the assistant message that replied to it, and it is a
	// separate field because the two ids live in disjoint spaces.
	//
	// A verdict is recorded against the ANSWER: FeedbackService.Rate refuses
	// anything that is not an assistant message (ErrNotAssistantMessage), so
	// every row in message_feedback is keyed by an assistant message id. Every
	// row in agent_actions is keyed by the user message that provoked the turn
	// — verified against 717 real rows, of which 717 join to role='user' and 0
	// to role='assistant'.
	//
	// So the harvester's verdict gate cannot be asked about MessageID: it would
	// be looking up a question in a table that only holds answers, and the
	// answer is always "nobody complained". That is the defect this field
	// exists to close, and it is invisible to a unit test whose fake keys the
	// verdict map on whatever id the candidate carries.
	//
	// Empty when no assistant reply can be found for the turn.
	AnswerMessageID string
	SourceID        string
	Question        string
	SQL             string
	RowCount        int
	RanAt           time.Time
}

// VerdictKeys returns the message ids a verdict about this turn could be filed
// against, for the harvester's batch read.
//
// Both, rather than only the answer: the answer id is the one the product
// writes today, and the question id costs nothing to include and keeps the gate
// from getting weaker if a caller ever files a verdict the other way round. A
// gate that exists to keep wrong answers out should fail closed.
func (c CookbookCandidate) VerdictKeys() []string {
	if c.AnswerMessageID == "" || c.AnswerMessageID == c.MessageID {
		return []string{c.MessageID}
	}
	return []string{c.MessageID, c.AnswerMessageID}
}

// CookbookCandidateSource mines finished turns for candidates.
// *postgres.CookbookCandidateRepo satisfies it.
type CookbookCandidateSource interface {
	Candidates(ctx context.Context, companyID string, since time.Time, limit int) ([]CookbookCandidate, error)
	CompaniesWithActivity(ctx context.Context, since time.Time) ([]string, error)
}
