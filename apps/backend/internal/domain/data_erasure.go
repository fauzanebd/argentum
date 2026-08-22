package domain

import (
	"context"
	"encoding/json"
	"time"
)

// Retention and erasure (T-H6).
//
// **The obligation is the customer's and the API is ours.** Under UU PDP
// 27/2022 the tenant is the *pengendali data*; they carry the erasure duty and
// cannot discharge it without a route from us. So this file describes two
// things that look similar and are not: a *purge*, which happens on a schedule
// because the tenant set a retention window, and an *erasure*, which happens
// because somebody asked. Both are recorded, and the record outlives what it
// describes.
//
// **Audit rows are the deliberate exception and it is load-bearing.**
// `agent_actions` holds no result contents by design (`args_redacted`, and
// migration 023's own comment on why neither thread_id nor message_id carries
// a foreign key). It records what the agent *did*, under whose authority —
// evidence a tenant needs precisely when something has gone wrong, and which
// an erasure request must not be able to launder. Nothing in the retention path
// deletes one, and `TestRetentionStatementsNeverNameTheProtectedTables`
// (adapters/postgres) says so as an assertion rather than as this comment —
// alongside the live arm, which proves it against a real database rather than
// against the source.

// ErasureScope is why rows were deleted.
type ErasureScope string

const (
	// ErasureScopeAll is an on-request erasure of every conversation a company
	// has: what `DELETE /api/company/data` performs.
	ErasureScopeAll ErasureScope = "all"
	// ErasureScopeRetention is one purge tick, recording what the nightly job
	// removed for a company whose retention window had passed.
	//
	// It shares a table with the on-request kind on purpose. "When did you last
	// delete my data, and how much?" is one question, and answering it from two
	// tables that have drifted apart is how the answer becomes wrong.
	ErasureScopeRetention ErasureScope = "retention"
)

// Valid reports whether the scope is one this product writes.
func (s ErasureScope) Valid() bool {
	return s == ErasureScopeAll || s == ErasureScopeRetention
}

// Erasure status values. A row is written `running` *before* the delete and
// updated after it, so a process that dies mid-erasure leaves evidence that it
// was attempted rather than no evidence at all.
const (
	ErasureStatusRunning   = "running"
	ErasureStatusCompleted = "completed"
	ErasureStatusFailed    = "failed"
)

// DataErasure is the written completion record for one purge or erasure. It
// holds counts and timestamps and never content — the whole point is that it
// survives the thing it describes, including the erasure that created it.
type DataErasure struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	// RequestedBy is empty for a retention purge, which nobody requested, and
	// stays empty if the user who asked has since been deleted.
	RequestedBy     string       `json:"requested_by,omitempty"`
	Scope           ErasureScope `json:"scope"`
	Status          string       `json:"status"`
	ThreadsDeleted  int          `json:"threads_deleted"`
	MessagesDeleted int          `json:"messages_deleted"`
	ErrorText       string       `json:"error_text,omitempty"`
	RequestedAt     time.Time    `json:"requested_at"`
	// CompletedAt is nil while the erasure is running, and on one that died.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// DataErasureRepository persists the record. Deliberately append-and-close:
// there is no Delete, because a route that erases the evidence of an erasure
// is the one thing this table cannot offer.
type DataErasureRepository interface {
	// Begin writes the `running` row and fills in e.ID.
	Begin(ctx context.Context, e *DataErasure) error
	// Complete closes a row with its counts. Called with the same id Begin
	// returned.
	Complete(ctx context.Context, id string, threads, messages int) error
	// Fail closes a row with the reason it did not finish.
	Fail(ctx context.Context, id string, reason string) error
	// ListByCompany returns a company's history newest-first.
	ListByCompany(ctx context.Context, companyID string, limit int) ([]*DataErasure, error)
}

// RetentionRepository is the narrow contract the purge and the erasure need.
// Declared here beside the record rather than on ThreadRepository, because
// every method is a bulk delete and mixing them into the repository the
// request path uses puts a `DELETE FROM messages` one autocomplete away from a
// handler.
type RetentionRepository interface {
	// PurgeCompanyMessages deletes one company's messages older than `before`,
	// then the threads that are both empty and older than `before`. Returns
	// what went.
	//
	// Two steps rather than one, and the order matters. Deleting whole threads
	// by age would keep a 400-day-old message alive inside a thread somebody
	// posted to yesterday, which is exactly what a retention promise says will
	// not happen. Deleting only messages would leave empty husks in the
	// tenant's thread list forever. So: messages by their own age, then the
	// threads that no longer have any.
	PurgeCompanyMessages(ctx context.Context, companyID string, before time.Time) (threads, messages int, err error)
	// HasExpired reports whether this company has anything older than
	// `before` — a message, or a thread already empty and past the window.
	//
	// It exists so a tick with nothing to do can write nothing at all. The
	// record is opened *before* the delete so a crash leaves evidence, which
	// means the service cannot learn the counts are zero until the row is
	// already written; the §1q gate found two nights of `0 threads / 0
	// messages` rows sitting above a tenant's real erasure in their own
	// history. One EXISTS probe per opted-in tenant per night is the cost of
	// keeping the evidence table readable.
	HasExpired(ctx context.Context, companyID string, before time.Time) (bool, error)
	// EraseCompanyConversations deletes every thread and message a company has.
	// Audit rows are untouched; see the file comment.
	EraseCompanyConversations(ctx context.Context, companyID string) (threads, messages int, err error)
	// CompaniesWithRetention lists the companies that have set a window, with
	// the window, so the purge does not read every tenant to find the few that
	// opted in.
	CompaniesWithRetention(ctx context.Context) ([]CompanyRetention, error)
	// ExportCompanyConversations streams a company's whole transcript history
	// oldest-first, so erasure is not the only exit.
	ExportCompanyConversations(ctx context.Context, companyID string, fn func(ExportedMessage) error) error
}

// CompanyRetention is one tenant's purge window.
type CompanyRetention struct {
	CompanyID string
	Days      int
}

// ExportedMessage is one row of the export. Flat rather than nested by thread:
// the export is streamed and a nested document cannot be written without
// holding a thread's whole transcript in memory, which is the shape that turns
// one large tenant into an OOM.
type ExportedMessage struct {
	ThreadID    string `json:"thread_id"`
	ThreadTitle string `json:"thread_title"`
	Channel     string `json:"channel"`
	MessageID   string `json:"message_id"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	// json.RawMessage rather than []byte: the column is JSONB, and []byte
	// marshals to base64 — an export nobody can read without decoding it first,
	// which defeats the point of offering one.
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}
