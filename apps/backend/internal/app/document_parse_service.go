package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentParseService reads an uploaded PDF into per-page artifacts (T-P2).
//
// **The status is the product here, not the text.** Nothing downstream exists
// yet — typing is `T-P4`, publishing is `T-P6`, retrieval is `T-P9` — so what
// this service owes a tenant today is an honest answer to "what happened to my
// document": read, partly read, or not read and here is why. Every path below
// ends in a status somebody can act on, and the one thing none of them does is
// leave a row saying `parsing` forever.
//
// It stores each page as its own object rather than one blob per document. A
// reviewer in `T-P7` opens one page at a time beside its rectangle, and a
// hundred-page artifact fetched to render page four is a design that works on
// the demo and not on a real report.
type DocumentParseService struct {
	docs   domain.SourceDocumentRepository
	blobs  DocumentArtifactStore
	parser docparse.Parser
	// maxPages is passed to the parser rather than checked here. The page count
	// is not knowable without opening the file, and opening the file is the
	// parser's job — so the refusal happens where the number first exists,
	// before any page is read.
	maxPages int
}

// DocumentArtifactStore is the storage this service needs: the uploaded bytes
// back, and somewhere to put what was read out of them.
type DocumentArtifactStore interface {
	DownloadKey(ctx context.Context, key string) ([]byte, error)
	UploadKey(ctx context.Context, key string, r io.Reader, contentType string) (string, error)
}

// ErrParseNotConfigured is returned when this deployment has no parser. The
// worker treats it as terminal rather than retrying — no amount of waiting
// gives a process a sidecar it was not configured with.
var ErrParseNotConfigured = errors.New("document parsing is not configured on this deployment")

// artifactContentType is what a page artifact is stored as. Fixed: these are
// written by this service and read by this service.
const artifactContentType = "application/json"

func NewDocumentParseService(
	docs domain.SourceDocumentRepository,
	blobs DocumentArtifactStore,
	parser docparse.Parser,
	maxPages int,
) *DocumentParseService {
	if maxPages < 0 {
		maxPages = 0
	}
	return &DocumentParseService{docs: docs, blobs: blobs, parser: parser, maxPages: maxPages}
}

// Parse reads one document and records what happened to it.
//
// Returning an error means "retry this": the queue's backoff is the right
// response to a parser that is restarting. Returning nil after writing a
// `failed` status means the opposite — the document will fail the same way
// next time, and a queue that keeps trying is a queue filling with work nobody
// wants done.
func (s *DocumentParseService) Parse(ctx context.Context, documentID string) error {
	if s == nil || s.docs == nil || s.blobs == nil || s.parser == nil {
		return ErrParseNotConfigured
	}
	doc, err := s.docs.GetByID(ctx, documentID)
	if err != nil {
		// A deleted document is not a failure. The task outlived the row it
		// names, which is the ordinary consequence of somebody deleting a
		// document while its parse was queued.
		return err
	}

	started := time.Now()
	if err := s.docs.UpdateStatus(ctx, doc.ID, domain.SourceDocumentParsing, "", 0); err != nil {
		return fmt.Errorf("mark parsing: %w", err)
	}

	data, err := s.blobs.DownloadKey(ctx, doc.StorageKey)
	if err != nil {
		// Both causes look identical from here — a missing object and an object
		// store that is down — so the row says what is true either way and the
		// error is returned so a retry can correct it. A retry that succeeds
		// overwrites this status; one that does not leaves a sentence naming the
		// key, which is what somebody debugging it needs.
		s.fail(ctx, doc, "the stored file could not be read")
		return fmt.Errorf("download document bytes: %w", err)
	}

	parsed, err := s.parser.Parse(ctx, bytes.NewReader(data), s.maxPages)
	switch {
	case err == nil:
	case errors.Is(err, docparse.ErrRefused):
		// Terminal, and the parser's own sentence is the useful part: "the
		// document has 412 pages and this deployment reads at most 200" is
		// something a tenant can act on. Nil is returned so nothing retries it.
		s.fail(ctx, doc, refusalSentence(err))
		return nil
	case errors.Is(err, docparse.ErrNotConfigured):
		// Queued on a deployment that cannot parse. The document goes back to
		// `uploaded` rather than to `failed`, because nothing is wrong with it —
		// it is a file nobody has read, which is exactly what `uploaded` means.
		s.restoreUploaded(ctx, doc, "no parser is configured on this deployment")
		return ErrParseNotConfigured
	default:
		// Unavailable, or an answer we could not read. Retryable, and the status
		// goes back to `uploaded` rather than staying at `parsing`: if the
		// retries run out, a row saying "parsing" describes a process that is not
		// running, and a row saying "uploaded" describes the truth.
		s.restoreUploaded(ctx, doc, "the parser could not be reached; the document has not been read")
		return err
	}

	written, err := s.storePages(ctx, doc, parsed)
	if err != nil {
		s.fail(ctx, doc, "the pages could not be stored")
		return fmt.Errorf("store page artifacts: %w", err)
	}

	detail := ""
	if n := parsed.NeedsOCRPages(); n > 0 {
		// Said on the row rather than only in a log line, because it is the
		// difference between "your document is ready" and "most of it is a scan
		// nothing has read". T-P3 is what makes this number go down.
		detail = strconv.Itoa(n) + " of " + strconv.Itoa(parsed.PageCount) +
			" pages hold no readable text and were not read"
	}
	if err := s.docs.UpdateStatus(ctx, doc.ID, domain.SourceDocumentParsed, detail, parsed.PageCount); err != nil {
		return fmt.Errorf("mark parsed: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"company_id":      doc.CompanyID,
		"document_id":     doc.ID,
		"pages":           parsed.PageCount,
		"pages_text":      parsed.TextPages(),
		"pages_needs_ocr": parsed.NeedsOCRPages(),
		"artifacts":       written,
		"parser":          parsed.Parser.Name + " " + parsed.Parser.Version,
		"ms":              time.Since(started).Milliseconds(),
	}).Info("document parsed")
	return nil
}

// storePages writes one artifact per page plus a manifest.
//
// The manifest is not a duplicate of the pages: it is what a list view needs —
// how many pages, of what kind, from which parser build — without fetching a
// megabyte of markdown to render a row. `T-P13` reads the parser build off it,
// for the reason `T-Q15` exists.
func (s *DocumentParseService) storePages(ctx context.Context, doc *domain.SourceDocument, parsed *docparse.Document) (int, error) {
	written := 0
	for i := range parsed.Pages {
		page := parsed.Pages[i]
		body, err := json.Marshal(page)
		if err != nil {
			return written, fmt.Errorf("encode page %d: %w", page.Number, err)
		}
		key := PageArtifactKey(doc.CompanyID, doc.ContentSHA256, page.Number)
		if _, err := s.blobs.UploadKey(ctx, key, bytes.NewReader(body), artifactContentType); err != nil {
			return written, fmt.Errorf("store page %d: %w", page.Number, err)
		}
		written++
	}

	manifest := parseManifest{
		DocumentID: doc.ID,
		PageCount:  parsed.PageCount,
		Parser:     parsed.Parser,
		ParsedAt:   time.Now().UTC(),
	}
	for _, p := range parsed.Pages {
		manifest.Pages = append(manifest.Pages, manifestPage{
			Number: p.Number, Kind: p.Kind, Tables: len(p.Tables),
			CharCount: p.CharCount, Error: p.Error,
		})
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return written, fmt.Errorf("encode manifest: %w", err)
	}
	if _, err := s.blobs.UploadKey(ctx, ManifestKey(doc.CompanyID, doc.ContentSHA256),
		bytes.NewReader(body), artifactContentType); err != nil {
		return written, fmt.Errorf("store manifest: %w", err)
	}
	return written, nil
}

// parseManifest is the index of what one parse produced. No page content: this
// is read to draw a list, and the content is one fetch away when somebody opens
// a page.
type parseManifest struct {
	DocumentID string              `json:"document_id"`
	PageCount  int                 `json:"page_count"`
	Parser     docparse.ParserInfo `json:"parser"`
	ParsedAt   time.Time           `json:"parsed_at"`
	Pages      []manifestPage      `json:"pages"`
}

type manifestPage struct {
	Number    int    `json:"page_no"`
	Kind      string `json:"kind"`
	Tables    int    `json:"tables"`
	CharCount int    `json:"char_count"`
	Error     string `json:"error,omitempty"`
}

// PageArtifactKey addresses a page under the document's own content hash, so a
// re-parse of the same bytes overwrites its previous answer rather than
// accumulating one set of pages per attempt.
func PageArtifactKey(companyID, sha string, page int) string {
	return fmt.Sprintf("source-documents/%s/%s/pages/%d.json", companyID, sha, page)
}

// ManifestKey is the same prefix's index.
func ManifestKey(companyID, sha string) string {
	return fmt.Sprintf("source-documents/%s/%s/parse.json", companyID, sha)
}

// fail records a terminal outcome. A status write that itself fails is logged
// and swallowed: the caller is already returning the real error, and losing it
// behind a second one would hide what actually happened.
func (s *DocumentParseService) fail(ctx context.Context, doc *domain.SourceDocument, detail string) {
	if err := s.docs.UpdateStatus(ctx, doc.ID, domain.SourceDocumentFailed, detail, 0); err != nil {
		logrus.WithError(err).WithField("document_id", doc.ID).
			Warn("could not record the parse failure; the document is left mid-parse")
	}
}

func (s *DocumentParseService) restoreUploaded(ctx context.Context, doc *domain.SourceDocument, detail string) {
	if err := s.docs.UpdateStatus(ctx, doc.ID, domain.SourceDocumentUploaded, detail, 0); err != nil {
		logrus.WithError(err).WithField("document_id", doc.ID).
			Warn("could not restore the document to uploaded; it is left mid-parse")
	}
}

// refusalSentence strips the client's error prefix so the stored detail reads
// as a statement about the document rather than as a Go error chain.
func refusalSentence(err error) string {
	msg := err.Error()
	const prefix = "the document parser refused this document: "
	if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		return msg[len(prefix):]
	}
	return msg
}
