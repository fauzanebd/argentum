package domain

import (
	"context"
	"strings"
	"time"
)

// FeedbackRating is what somebody said about one answer: it was right, or it
// was not.
//
// Two values, deliberately. A five-point scale measures how a reader *feels*
// about an answer, and what this type is for is whether the answer was
// correct — the one property this product's whole guardrail stack is arranged
// around and the one nothing has ever recorded. A neutral middle would be
// indistinguishable from the absent row that already means "nobody said".
type FeedbackRating int16

const (
	// FeedbackDown is the row that matters. A wrong answer that nobody flags
	// is a wrong answer that gets learned from (T-Q8).
	FeedbackDown FeedbackRating = -1
	// FeedbackUp is the label the cookbook reads, and the weaker of the two
	// signals: a reader who is not checking will approve a plausible answer.
	// It is why T-Q8 requires more than this before it will keep an example.
	FeedbackUp FeedbackRating = 1
)

// Valid reports whether r is one of the two ratings. The database carries the
// same rule as a CHECK; this is so a bad value is refused before it becomes a
// constraint violation the caller cannot read.
func (r FeedbackRating) Valid() bool { return r == FeedbackUp || r == FeedbackDown }

// FeedbackReasonMaxChars caps the free-text reason.
//
// Generous, because the reason is the most useful column in the table for
// anyone tuning the agent and the least predictable in length — "the number is
// double what it should be, I think it's counting line items not orders" is
// the sentence that makes a bug findable, and it is 96 characters. The cap
// exists to bound the row, not to discipline the writer.
const FeedbackReasonMaxChars = 2000

// MessageFeedback is one actor's verdict on one assistant message.
type MessageFeedback struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	ThreadID  string `json:"thread_id"`
	MessageID string `json:"message_id"`

	Rating FeedbackRating `json:"rating"`
	Reason string         `json:"reason,omitempty"`

	// ActorKind and ActorRef name the witness in the same vocabulary
	// agent_actions uses (T-05), so "who complained" is answerable in the
	// terms the audit log already speaks. A dashboard user, a widget visitor
	// and an API caller are three different kinds of witness and must stay
	// distinguishable — a tenant's own analyst noticing a wrong number is
	// worth more than an anonymous thumbs-down from their customer's website.
	ActorKind ActorKind `json:"actor_kind"`
	ActorRef  string    `json:"actor_ref,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FeedbackWithContext is one verdict and enough of the turn to act on it.
//
// The bare verdict is unactionable and that is why nothing read it for a month
// (T-Q16): `MessageFeedback` carries a `message_id`, a `thread_id` and a
// reason, so a list of them says "somebody disliked 4f2a-… on 9b1c-…" and the
// only way to learn anything is to open each thread by hand.
//
// The excerpts are the whole point. Somebody triaging wrong answers is reading
// for a pattern — the same metric, the same table, the same misread question —
// and a pattern is visible across twenty rows on one screen and invisible
// across twenty tabs.
// **Not an embedded `MessageFeedback`, and that is load-bearing.** Go's
// encoding/json inlines an embedded struct's fields, so the wire is flat —
// but tygo renders embedding as a *named field*, and the generated TypeScript
// would have described `{ MessageFeedback: {...}, question, answer }` for a
// body that is `{ id, thread_id, ..., question, answer }`. A type that
// misdescribes its own wire is the defect this repo spent 2026-09-10 removing
// from the widget; spelling the fields out is the cost of not reintroducing it
// one layer down. See generated-types.md §Limits for the two latent cases.
type FeedbackWithContext struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	ThreadID  string `json:"thread_id"`
	MessageID string `json:"message_id"`

	Rating FeedbackRating `json:"rating"`
	Reason string         `json:"reason,omitempty"`

	ActorKind ActorKind `json:"actor_kind"`
	ActorRef  string    `json:"actor_ref,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Question is the user message that provoked the answer, empty when the
	// thread's shape does not resolve one. Not required: a verdict against an
	// answer whose question cannot be found is still a verdict, and dropping
	// the row would hide the complaint to protect the layout.
	Question string `json:"question,omitempty"`
	// Answer is the assistant message the verdict is about.
	Answer string `json:"answer,omitempty"`
}

// FeedbackExcerptChars bounds each of the two excerpts.
//
// Enough to recognise a question and to see the top of an answer — which is
// where a wrong number usually is, because the agent states the figure and then
// explains it. Whoever needs the rest has the thread link.
const FeedbackExcerptChars = 600

// Normalize trims the reason to the cap and squares up whitespace. Returns
// true when the reason was shortened, so the caller can say so rather than
// silently keeping half a sentence.
func (f *MessageFeedback) Normalize() (truncated bool) {
	f.Reason = strings.TrimSpace(f.Reason)
	if len(f.Reason) <= FeedbackReasonMaxChars {
		return false
	}
	f.Reason = f.Reason[:FeedbackReasonMaxChars]
	return true
}

// FeedbackSummary is the roll-up for one tenant over a window: how many
// answers were rated, and how many of them were wrong.
//
// The denominator is deliberately the number of *rated* answers rather than
// the number of turns. A rate over all turns measures how often people press
// the button, which moves whenever the UI moves; a rate over rated turns
// measures the agent. Both are worth knowing and only one of them is this.
type FeedbackSummary struct {
	Rated int `json:"rated"`
	Up    int `json:"up"`
	Down  int `json:"down"`
}

// DownRate is the fraction of rated answers marked wrong, or 0 when nobody has
// rated anything. Zero is the honest answer for an empty window: "no reported
// failures" and "nobody looked" are different claims, and the caller can tell
// them apart from Rated.
func (s FeedbackSummary) DownRate() float64 {
	if s.Rated == 0 {
		return 0
	}
	return float64(s.Down) / float64(s.Rated)
}

// MessageFeedbackRepository is the persistence contract for verdicts.
type MessageFeedbackRepository interface {
	// Upsert records or replaces one actor's verdict on one message. Pressing
	// the button twice is a correction, not a second opinion — the unique key
	// is (message_id, actor_kind, actor_ref).
	Upsert(ctx context.Context, f *MessageFeedback) error
	// GetByMessage returns every verdict on one message, newest first.
	GetByMessage(ctx context.Context, companyID, messageID string) ([]*MessageFeedback, error)
	// ListByCompany returns recent verdicts for a tenant, newest first,
	// optionally only the negative ones — which is the list anyone tuning the
	// agent actually opens.
	ListByCompany(ctx context.Context, companyID string, onlyNegative bool, limit, offset int) ([]*MessageFeedback, error)
	// ListWithContext is ListByCompany plus the question and the answer each
	// verdict is about (T-Q16).
	//
	// A separate method rather than a flag on the one above, because the two
	// have different costs: this one joins `messages` twice per row and the
	// other reads one table. The caller that wants to know whether a verdict
	// exists should not pay for the caller that wants to read it.
	ListWithContext(ctx context.Context, companyID string, onlyNegative bool, limit, offset int) ([]*FeedbackWithContext, error)
	// Summarize rolls up a window for the dashboard.
	Summarize(ctx context.Context, companyID string, from, to time.Time) (FeedbackSummary, error)
	// NegativeMessageIDs returns the ids, within the given set, that somebody
	// marked wrong. T-Q8's guard: an answer a human called wrong must never
	// become an example the agent learns from, and the cookbook asks this
	// question about a batch of candidates at once rather than one at a time.
	NegativeMessageIDs(ctx context.Context, companyID string, messageIDs []string) (map[string]bool, error)
}
