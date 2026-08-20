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
	// OCRPageCount is how many of those pages a model read (T-P3), and
	// OCRCostMicroUSD is what that cost. Zero on every document a deployment
	// with OCR off ever ingests, which is the default — a rendered page leaving
	// for a third-party model is the operator's decision that `LLM_ZDR` exists
	// to let them make.
	OCRPageCount    int   `json:"ocr_page_count"`
	OCRCostMicroUSD int64 `json:"ocr_cost_micro_usd"`
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

// FilenameSearchTerms is what a filename contributes to the lexical index
// (T-P14): the name itself, then its stem split on the separators people put in
// filenames.
//
// **Both halves are load-bearing, and the reason is the tokenizer.** Postgres's
// default parser reads `09-scan-invoice.pdf` as one `host` token — a single
// lexeme, matched only by somebody who types the whole name including the
// extension. That is the query a person who just uploaded the file actually
// writes, so the raw name stays. But `invoice` and `scan invoice` — what
// anybody else would ask — match nothing against that lexeme, so the stem is
// split as well: `09-scan-invoice.pdf 09 scan invoice`.
//
// The extension is deliberately dropped from the split half. Indexing `pdf` on
// every document would make it a term that matches everything, which is the
// same as a term that discriminates nothing; it survives only inside the whole
// filename, where it is part of an exact handle.
//
// Called at ingest and stored on the chunk row rather than computed at query
// time, because `document_chunks.tsv` is a generated column and a generated
// expression cannot read the joined `source_documents` row (migration `065`).
func FilenameSearchTerms(filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		return ""
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	split := strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.':
			return ' '
		}
		return r
	}, stem)
	// Fields collapses the runs of spaces the mapping above can produce, so two
	// filenames differing only in separator punctuation index identically.
	return strings.Join(append([]string{name}, strings.Fields(split)...), " ")
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
	// GetByID is the one unscoped read, and it exists for exactly one caller:
	// the parse worker (T-P2), which is handed a document id by a queue and has
	// no tenant to scope it to. It is not a hole in the boundary above — the
	// company is on the row it returns, and everything the worker writes is
	// keyed by that id — but it is the method to look at first if one ever
	// appears on an HTTP path.
	GetByID(ctx context.Context, id string) (*SourceDocument, error)
	// GetBySHA is the dedupe lookup. ErrNotFound means "new file", which is the
	// common case and not a failure.
	GetBySHA(ctx context.Context, companyID, sha256 string) (*SourceDocument, error)
	ListByCompany(ctx context.Context, companyID string, limit, offset int) ([]*SourceDocument, error)
	// UpdateStatus is the worker's write. pageCount is applied only when
	// positive, so a status change cannot silently zero a count an earlier pass
	// established.
	UpdateStatus(ctx context.Context, id string, status SourceDocumentStatus, detail string, pageCount int) error
	// RecordOCR stores what reading this document with a model cost (T-P3), and
	// OCRPagesSince is what the monthly budget is checked against (T-P11).
	//
	// The budget reads pages rather than money on purpose: a page is what a
	// tenant can count, an operator can reason about and a refusal can name,
	// where a micro-USD ceiling would need somebody to know this month's model
	// pricing to predict whether their upload will work.
	RecordOCR(ctx context.Context, id string, pages int, costMicroUSD int64) error
	OCRPagesSince(ctx context.Context, companyID string, since time.Time) (int, error)
	Delete(ctx context.Context, companyID, id string) error
}
