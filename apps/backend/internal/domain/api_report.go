package domain

import (
	"context"
	"time"
)

// APIReportKind is which door created the job.
type APIReportKind string

const (
	// APIReportAgentic — `POST /v1/reports` took a prompt, ran a real turn and
	// billed tokens for it.
	APIReportAgentic APIReportKind = "agentic"
	// APIReportRender — `POST /v1/reports/render` took a spec that overran the
	// synchronous window and became a job. The same shape on purpose: an
	// integrator writing a collection path should write it once.
	APIReportRender APIReportKind = "render"
)

// APIReportStatus is where a job got to.
type APIReportStatus string

const (
	APIReportQueued    APIReportStatus = "queued"
	APIReportRunning   APIReportStatus = "running"
	APIReportCompleted APIReportStatus = "completed"
	APIReportFailed    APIReportStatus = "failed"
)

// Terminal reports whether the job has stopped moving. The SSE bridge and the
// poll route both need this, and two copies of the comparison would be two
// places to forget a state when one is added.
func (s APIReportStatus) Terminal() bool {
	return s == APIReportCompleted || s == APIReportFailed
}

// APIReport is one asynchronous report job (T-A2).
//
// It exists because `POST /v1/reports` answers 202 and finishes minutes later,
// so it has to hand back something the caller can name afterwards. A thread id
// will not do: a thread outlives the turn and accumulates more of them, so
// "is my report ready?" would have no answer.
type APIReport struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	APIKeyID  string `json:"api_key_id,omitempty"`
	// Kind and Format are fixed at creation; everything below Status moves.
	Kind   APIReportKind   `json:"kind"`
	Status APIReportStatus `json:"status"`
	Format DocumentFormat  `json:"format"`
	Prompt string          `json:"prompt,omitempty"`
	// ThreadID is empty for a render job — it has no conversation.
	ThreadID   string `json:"thread_id,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	// CallbackURL is where the signed `report.completed` body goes. Empty is
	// the ordinary case: most callers poll or stream.
	CallbackURL string `json:"callback_url,omitempty"`
	// Error is written for an integrator reading it in their own logs, never a
	// raw Go error — a wrapped chain names our internals and means nothing to
	// the person who has to act on it.
	Error       string     `json:"error,omitempty"`
	RequestID   string     `json:"request_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// APIReportRepository persists report jobs.
//
// Every method is company-scoped except Create, which carries the company on
// the record. `/v1` resolves an id the caller supplied, so the tenant boundary
// belongs in the query.
type APIReportRepository interface {
	Create(ctx context.Context, r *APIReport) error
	GetForCompany(ctx context.Context, companyID, id string) (*APIReport, error)
	// Get is the worker's read: it holds a report id off a queue payload and
	// has no company to scope by until it has the row.
	Get(ctx context.Context, id string) (*APIReport, error)
	// AttachThread records which conversation the job is running on.
	//
	// It is a second write rather than a field on Create because the order is
	// forced: the report id has to exist before the turn is enqueued (the
	// worker needs it in the payload), and the thread is not resolved until
	// that enqueue happens. The live gate found what happens without it — the
	// row keeps a null thread, so the poll route omits it and the SSE bridge
	// has no channel to subscribe to and closes immediately on the one
	// operation it exists to stream.
	AttachThread(ctx context.Context, id, threadID string) error
	// MarkRunning is best-effort progress. A job that goes straight from
	// queued to completed is not a bug — a fast turn can beat the update — so
	// nothing reads this as a precondition.
	MarkRunning(ctx context.Context, id string) error
	// Complete writes the terminal state in one statement. documentID may be
	// empty: a turn that answered in prose without calling generate_document
	// completed, and reporting it as failed would be a lie about what happened.
	Complete(ctx context.Context, id string, status APIReportStatus, documentID, errMsg string, at time.Time) error
}
