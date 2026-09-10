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

	// ArchivedAt retires an example from retrieval without deleting it
	// (T-Q15). Nil is the ordinary state.
	//
	// Archived rather than deleted for a reason the recovery path makes
	// obvious: the two things that retire an example — the warehouse moved, or
	// it never matched anything — are both reversible, and `origin_message_id`
	// is unique, so a deleted example cannot be re-learned by the harvester
	// without the original turn still being there. Clearing this column is a
	// full recovery; a DELETE is not.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// ArchiveReason is why, in one machine-readable word. Empty while live.
	ArchiveReason string `json:"archive_reason,omitempty"`
}

// Why an example was retired. Two causes, and they want opposite responses
// from whoever reads the number: one says the warehouse moved underneath the
// cookbook, the other says the example was never earning its place.
const (
	// ArchiveReasonSchemaDrift is a table the example queries that the source
	// no longer has. The example is not stale, it is *wrong* — it would fail if
	// the model imitated it.
	ArchiveReasonSchemaDrift = "schema_drift"
	// ArchiveReasonUnused is an example old enough to have had its chance and
	// never once retrieved.
	//
	// `uses = 0` is read here as absence of evidence, not as a bad score, which
	// is why it is paired with an age bound and never used to *rank*. An
	// example nobody has had occasion to need is not a bad example, and a quiet
	// tenant must not have their cookbook emptied for being quiet.
	ArchiveReasonUnused = "unused"
)

// QueryExampleRef is the little of an example a sweep needs: which one, which
// warehouse, and the SQL to read table names out of.
//
// Deliberately not the whole row. The embedding is 1,536 float32s and a sweep
// has no use for it; reading a tenant's entire cookbook to check table names
// would pull about 6 KB per example for the 800 characters it actually reads.
type QueryExampleRef struct {
	ID       int64
	SourceID string
	SQL      string
}

// QueryExampleHit is one retrieved example and how close it was.
//
// **Do not serve this to a browser without flattening it.** The embedded
// QueryExample is inlined by encoding/json and rendered as a *nested field* by
// tygo, so `packages/api-types` describes a shape this type does not have. It
// is harmless today because nothing outside the turn path reads one; see
// coverage/generated-types.md §"Go struct embedding does not survive tygo".
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
	// Counts live examples only: a tenant whose whole cookbook has been
	// archived has no cookbook, and paying for an embedding call to retrieve
	// from it would be the same defect as paying for one before the first
	// harvest.
	CountByCompany(ctx context.Context, companyID string) (int, error)
	// CountArchivedByCompany is the same number for the retired half, for the
	// admin surface. Separate rather than a filter argument because every
	// caller of CountByCompany means "is there a cookbook", and one that meant
	// "how big is the table" would be a different question wearing its name.
	CountArchivedByCompany(ctx context.Context, companyID string) (int, error)
	// MarkUsed records that these examples were retrieved. Written on every
	// retrieval; read by the sweep (T-Q15) and by nothing on the turn path.
	MarkUsed(ctx context.Context, ids []int64, at time.Time) error
	// LiveRefs lists the examples still eligible for retrieval, for the sweep
	// to check against the warehouse they query (T-Q15). Archived rows are
	// excluded: a sweep that re-examined its own archive would spend an
	// introspection per run discovering the same drift forever.
	LiveRefs(ctx context.Context, companyID string) ([]QueryExampleRef, error)
	// Archive retires examples by id, recording why. Returns how many rows it
	// changed — which is not always len(ids), because a concurrent sweep or an
	// admin may have archived some already, and the count is what gets logged.
	Archive(ctx context.Context, ids []int64, reason string, at time.Time) (int, error)
	// ArchiveUnused retires every live example for one company that has never
	// been retrieved and was created before createdBefore.
	//
	// A statement rather than a read-then-write, because the alternative is
	// pulling every row of a cookbook to compare two integers the database
	// already has indexed. The age bound is the caller's policy, not this
	// method's.
	ArchiveUnused(ctx context.Context, companyID string, createdBefore, at time.Time) (int, error)
	// CompaniesWithExamples lists the tenants that have a live cookbook, for
	// the sweep's deployment-wide tick (T-Q15).
	//
	// Not CompaniesWithActivity, which is the harvester's list: that one asks
	// who ran a query lately, and the tenant this job most needs to reach is
	// the one who did not — a quiet quarter is exactly when a warehouse gets
	// migrated underneath a cookbook nobody is watching.
	CompaniesWithExamples(ctx context.Context) ([]string, error)
	// ExistingOrigins reports which of the given origin message ids the
	// cookbook already holds, so a harvest run does not re-embed what it
	// already learned. Batched, because the alternative is one round trip per
	// candidate.
	ExistingOrigins(ctx context.Context, companyID string, messageIDs []string) (map[string]bool, error)
	// DeleteByCompany empties a tenant's cookbook, archived rows included.
	//
	// This used to be the *only* answer to a schema that moved, which is what
	// T-Q15 is about: it is all-or-nothing, swung by hand, for a condition that
	// is almost never all-or-nothing. The sweep now handles the ordinary case
	// one example at a time and reversibly. What is left for this is the real
	// escape hatch — a tenant who wants the cookbook gone.
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
