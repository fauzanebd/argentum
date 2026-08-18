package domain

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"time"
)

// SourceDocument is one PDF a tenant uploaded, and how far through the parse
// pipeline it has got (T-P1).
//
// **It is not [Document].** That is what the agent generated — a file this
// product wrote, addressed by thread. This is what a tenant supplied, and the
// difference matters beyond bookkeeping: a generated document is finished when
// it is written, while an uploaded one is raw material that later tickets read
// repeatedly and with improving accuracy.
//
// Nothing here reaches a turn. A document becomes answerable only when a table
// extracted from it is published as a source (T-P6) or its prose is retrieved
// by a tool (T-P9), and both of those are separate tickets with a human in the
// first one. The roadmap's Decision 1 is the reason: a figure that arrives in
// the prompt as text is a figure `CheckGrounding` cannot check, which is the
// failure the last three sittings of this project were spent instrumenting.
type SourceDocument struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	// Filename as the tenant named it. The only human handle on a row whose
	// other identifier is a hash.
	Filename string `json:"filename"`
	// ContentSHA256 is the hex digest of the bytes, and the dedupe key. Uploading
	// the same file twice returns the first document rather than parsing it
	// again.
	ContentSHA256 string `json:"content_sha256"`
	ByteSize      int64  `json:"byte_size"`
	// PageCount is 0 until something has read the file. Written by the parse,
	// never by the upload.
	PageCount int `json:"page_count"`
	// StorageKey is the object key, not a URL — an endpoint changes when a
	// deployment moves buckets or puts a CDN in front, and a stored URL would
	// then point at nothing.
	StorageKey string               `json:"-"`
	Status     SourceDocumentStatus `json:"status"`
	// StatusDetail is why, in a sentence somebody can act on. Empty on the happy
	// path.
	StatusDetail string    `json:"status_detail,omitempty"`
	UploadedBy   string    `json:"uploaded_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SourceDocumentStatus is how far the pipeline has got with one document.
//
// The handler writes exactly one of these — StatusUploaded — and the worker
// writes the rest. One writer per state is what makes a stuck row a question
// about the worker rather than a question about which process last touched it.
type SourceDocumentStatus string

const (
	// SourceDocumentUploaded means the bytes are stored and nothing has read
	// them. It is also the resting state on a deployment with no parser: the
	// document is safe, and it is not pretending to be understood.
	SourceDocumentUploaded SourceDocumentStatus = "uploaded"
	SourceDocumentParsing  SourceDocumentStatus = "parsing"
	SourceDocumentParsed   SourceDocumentStatus = "parsed"
	SourceDocumentFailed   SourceDocumentStatus = "failed"
)

// SourceDocumentFilenameMaxChars caps the stored filename. Long enough for a
// descriptive export name, short enough that a list stays readable and a
// pathological name cannot be used to bloat a row.
const SourceDocumentFilenameMaxChars = 200

// Normalize squares up what came off a multipart form.
//
// The base name is taken deliberately: browsers on some platforms submit a full
// path in the filename field, and `C:\Users\andi\Desktop\laporan.pdf` is both
// ugly in a list and a directory traversal waiting for the first caller that
// treats a filename as a key. The stored object key is derived from the hash,
// never from this.
func (d *SourceDocument) Normalize() {
	name := strings.TrimSpace(d.Filename)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	if name == "." || name == "/" {
		name = ""
	}
	d.Filename = ClampRunes(name, SourceDocumentFilenameMaxChars)
	d.StatusDetail = strings.TrimSpace(d.StatusDetail)
	if d.Status == "" {
		d.Status = SourceDocumentUploaded
	}
}

// pdfMagic is the header every PDF starts with. A version digit follows, which
// is why the prefix stops at the hyphen.
var pdfMagic = []byte("%PDF-")

// LooksLikePDF reports whether these leading bytes are a PDF's.
//
// Checked on content rather than on the extension or the browser's declared
// content type, because both are supplied by the caller: a `.docx` renamed to
// `.pdf` would otherwise reach the parser, and "the parser said no" is a worse
// error than "that is not a PDF".
//
// Some producers emit a few junk bytes before the header and readers tolerate
// it, so a small window is scanned rather than only offset zero. The window is
// small on purpose — a file with the header a kilobyte in is not a PDF a
// tenant meant to send.
func LooksLikePDF(head []byte) bool {
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, pdfMagic)
}

// SourceDocumentRepository persists uploaded documents.
//
// There is no general GetByID. Every read is scoped to a company in the query
// rather than in the caller's memory, for the reason DocumentRepository states
// for GetForCompany: an id from another tenant has to be a not-found, and a
// handler that fetches first and compares afterwards is one forgotten
// comparison away from a cross-tenant read.
type SourceDocumentRepository interface {
	Create(ctx context.Context, d *SourceDocument) error
	GetForCompany(ctx context.Context, companyID, id string) (*SourceDocument, error)
	// GetBySHA is the dedupe lookup. ErrNotFound means "new file", which is the
	// common case and not a failure.
	GetBySHA(ctx context.Context, companyID, sha256 string) (*SourceDocument, error)
	ListByCompany(ctx context.Context, companyID string, limit, offset int) ([]*SourceDocument, error)
	// UpdateStatus is the worker's write. pageCount is applied only when
	// positive, so a status change cannot silently zero a count an earlier pass
	// established.
	UpdateStatus(ctx context.Context, id string, status SourceDocumentStatus, detail string, pageCount int) error
	Delete(ctx context.Context, companyID, id string) error
}
