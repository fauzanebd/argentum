package domain

import (
	"context"
	"time"
)

type DocumentFormat string

const (
	DocumentFormatPDF  DocumentFormat = "pdf"
	DocumentFormatXLSX DocumentFormat = "xlsx"
	DocumentFormatCSV  DocumentFormat = "csv"
	DocumentFormatPPTX DocumentFormat = "pptx"
	// DocumentFormatMP4 is the same report spec as a silent 1080p video
	// (T-V3). It is a document like the other four — one row, one storage key,
	// one presigned URL — and unlike them it is produced by another process and
	// takes minutes rather than milliseconds, which is why every door that
	// serves it is asynchronous.
	DocumentFormatMP4 DocumentFormat = "mp4"
)

func (f DocumentFormat) Valid() bool {
	switch f {
	case DocumentFormatPDF, DocumentFormatXLSX, DocumentFormatCSV, DocumentFormatPPTX, DocumentFormatMP4:
		return true
	}
	return false
}

// Async reports whether producing this format is too slow to hold a request
// open for. One predicate rather than `format == mp4` at four call sites: the
// next slow format must not have to find them all.
func (f DocumentFormat) Async() bool { return f == DocumentFormatMP4 }

func (f DocumentFormat) Extension() string {
	switch f {
	case DocumentFormatPDF:
		return "pdf"
	case DocumentFormatXLSX:
		return "xlsx"
	case DocumentFormatCSV:
		return "csv"
	case DocumentFormatPPTX:
		return "pptx"
	case DocumentFormatMP4:
		return "mp4"
	}
	return ""
}

func (f DocumentFormat) ContentType() string {
	switch f {
	case DocumentFormatPDF:
		return "application/pdf"
	case DocumentFormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case DocumentFormatCSV:
		return "text/csv; charset=utf-8"
	case DocumentFormatPPTX:
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case DocumentFormatMP4:
		return "video/mp4"
	}
	return "application/octet-stream"
}

// DocumentSource is which door produced a document.
type DocumentSource string

const (
	// DocumentSourceAgent — the generate_document tool, inside a turn.
	DocumentSourceAgent DocumentSource = "agent"
	// DocumentSourceAPI — either `/v1` door (T-A2). It says nothing about
	// whether there was a thread: the agentic door has one and the render door
	// does not, and reading the thread off the source is the mistake the
	// migration's comment exists to prevent.
	DocumentSourceAPI DocumentSource = "api"
)

// Document is a persisted artifact produced by the generate_document tool or
// by a `/v1` report route. Generic-purpose: invoices, agreements, T&Cs,
// research summaries, exports.
type Document struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	// ThreadID is empty for a document `POST /v1/reports/render` produced.
	// That door takes a spec and returns a file: no LLM, no conversation, and
	// so nothing for a thread to be. Every other path still has one.
	ThreadID   string         `json:"thread_id,omitempty"`
	MessageID  string         `json:"message_id,omitempty"`
	Format     DocumentFormat `json:"format"`
	Filename   string         `json:"filename"`
	StorageKey string         `json:"storage_key"`
	SizeBytes  int64          `json:"size_bytes"`
	Source     DocumentSource `json:"source"`
	// APIKeyID is the credential that paid for it, empty for the agent path.
	// It is what makes per-key usage answerable, which is one of the four
	// layers the sprint's risk register names against a leaked key.
	APIKeyID  string    `json:"api_key_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// HasPlan reports whether a video plan was stored beside this document
	// (T-V4), which is what decides whether the dashboard offers to share it
	// as a player. Set by docgen when it writes one, and by the share handler
	// when it reads one back — it is **not** a column: the object store is the
	// only thing that knows, and a boolean in Postgres saying otherwise is a
	// second answer that can drift from the bucket.
	HasPlan bool `json:"has_plan,omitempty"`
}

// DocumentFilter narrows a company-scoped document listing. A zero value
// lists everything for the tenant, newest first.
type DocumentFilter struct {
	Format DocumentFormat
	// From/To bound created_at. Zero means unbounded on that side.
	From time.Time
	To   time.Time
	// CursorTime/CursorID are the (created_at, id) of the last row the caller
	// saw. Both empty starts at the newest row. The pair, not the id alone:
	// two documents can share a microsecond, and a keyset predicate needs a
	// total order or it drops rows silently at a page boundary.
	CursorTime time.Time
	CursorID   string
	// Limit is the page size. The repository normalises a non-positive or
	// oversized value rather than erroring, matching every other constructor
	// in this codebase.
	Limit int
}

// DocumentRepository persists document metadata.
type DocumentRepository interface {
	Insert(ctx context.Context, d *Document) error
	GetByID(ctx context.Context, id string) (*Document, error)
	// GetForCompany is GetByID with the tenant boundary in the query rather
	// than in the caller's memory. `/v1` uses only this one: an id from
	// another tenant has to be a not-found, and a handler that fetches first
	// and compares afterwards is one forgotten comparison away from a
	// cross-tenant read.
	GetForCompany(ctx context.Context, companyID, id string) (*Document, error)
	// ListByCompany returns one page plus whether another exists. The extra
	// bool is not len(rows) == limit: a caller cannot tell a full last page
	// from a full middle one.
	ListByCompany(ctx context.Context, companyID string, f DocumentFilter) ([]*Document, bool, error)
	ListByThread(ctx context.Context, threadID string) ([]*Document, error)
}
