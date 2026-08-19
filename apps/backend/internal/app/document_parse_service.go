package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/dococr"
	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
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
	// chunker indexes the document's prose for retrieval (T-P8). Nil is legal
	// and is what a deployment without the chunk store gets: the tables half of
	// this feature works, and `search_documents` says it is not configured.
	chunker DocumentChunker
	// ocr reads the pages the text layer could not (T-P3). Nil is the default
	// and the state every deployment is in until an operator decides that
	// sending a rendered page to a third-party model is acceptable here.
	ocr      DocumentOCR
	renderer docparse.Renderer
	usage    DocumentUsageRecorder
	// ocrMaxPages bounds one document and pagesPerMonth bounds one company
	// (T-P11). Zero means unlimited on the second and "use the default" on the
	// first, because a per-document cap of zero is a feature that is on and
	// does nothing — the shape T-Q10 shipped by accident and had to be told
	// about by a measurement.
	ocrMaxPages   int
	pagesPerMonth int
}

// DocumentOCR is the narrow contract the OCR pass needs: one page in, its text
// and what it cost out. `dococr.Client` satisfies it.
type DocumentOCR interface {
	Configured() bool
	Model() string
	ReadPage(ctx context.Context, contentType, base64Image string) (string, dococr.Usage, error)
}

// DocumentUsageRecorder is the half of UsageService this service spends
// through. A parse that costs money and does not appear in the ledger is a bill
// nobody can explain, which is the whole of T-P11's argument.
type DocumentUsageRecorder interface {
	RecordDocumentOCR(ctx context.Context, companyID, documentID, model string, tokensIn, tokensOut int) int64
}

// DocumentChunker is the prose half of the pipeline, called with the pages this
// service already holds.
//
// Handed the parsed pages rather than left to re-read the artifacts, because
// this is the one moment the whole document is in memory: a second pass over
// object storage would buy nothing but a window in which the chunks describe a
// previous parse.
type DocumentChunker interface {
	Ingest(ctx context.Context, doc *domain.SourceDocument, pages []docparse.Page) error
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

// WithOCR turns on reading the pages the text layer could not (T-P3).
//
// Four dependencies rather than one, and none of them optional once the first
// is present: a reader, something to render the page, somewhere to record what
// it cost, and the two caps. A deployment that had the model and not the ledger
// would be spending a tenant's money invisibly, which is the state T-P11 exists
// to make impossible.
func (s *DocumentParseService) WithOCR(
	ocr DocumentOCR, renderer docparse.Renderer, usage DocumentUsageRecorder,
	maxPagesPerDoc, pagesPerMonth int,
) *DocumentParseService {
	if ocr == nil || !ocr.Configured() || renderer == nil {
		return s
	}
	if maxPagesPerDoc <= 0 {
		maxPagesPerDoc = 20
	}
	s.ocr = ocr
	s.renderer = renderer
	s.usage = usage
	s.ocrMaxPages = maxPagesPerDoc
	s.pagesPerMonth = pagesPerMonth
	return s
}

// WithChunker turns on prose indexing after a successful parse (T-P8).
func (s *DocumentParseService) WithChunker(c DocumentChunker) *DocumentParseService {
	if c == nil {
		return s
	}
	s.chunker = c
	return s
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

	// The scan tail (T-P3), between the parse and the artifacts: what OCR reads
	// has to be in the page it is stored as, or the review surface and the
	// chunker would both be working from the empty version.
	ocrDetail := s.readScannedPages(ctx, doc, data, parsed)

	written, err := s.storePages(ctx, doc, parsed)
	if err != nil {
		s.fail(ctx, doc, "the pages could not be stored")
		return fmt.Errorf("store page artifacts: %w", err)
	}

	detail := ocrDetail
	if n := parsed.NeedsOCRPages(); n > 0 && detail == "" {
		// Said on the row rather than only in a log line, because it is the
		// difference between "your document is ready" and "most of it is a scan
		// nothing has read". T-P3 is what makes this number go down.
		detail = strconv.Itoa(n) + " of " + strconv.Itoa(parsed.PageCount) +
			" pages hold no readable text and were not read"
	}
	if s.chunker != nil {
		// The prose half (T-P8). A failure here does not fail the parse: the
		// pages are stored, the tables are derivable, and a document whose
		// retrieval index could not be built is worse than one with no index
		// only for questions about its prose. Said out loud so a tenant asking
		// "why does search find nothing?" has an answer in the log.
		if err := s.chunker.Ingest(ctx, doc, parsed.Pages); err != nil {
			logrus.WithError(err).WithField("document_id", doc.ID).
				Warn("document parsed but its text could not be indexed; search_documents will not find it")
		}
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

// readScannedPages reads the pages the text layer could not, and returns the
// sentence that goes on the document row (T-P3/T-P11).
//
// **Every refusal happens before a model is called, and every one of them says
// why.** Off, over the per-document cap, over the company's monthly budget: all
// three leave the pages exactly as `T-P2` left them — empty, counted, and
// honest about it — and none of them fails the document, because a scan nobody
// read is not a broken file.
func (s *DocumentParseService) readScannedPages(
	ctx context.Context, doc *domain.SourceDocument, data []byte, parsed *docparse.Document,
) string {
	if s.ocr == nil || s.renderer == nil {
		return ""
	}
	var wanted []int
	for i := range parsed.Pages {
		if parsed.Pages[i].Kind == docparse.KindNeedsOCR {
			wanted = append(wanted, parsed.Pages[i].Number)
		}
	}
	if len(wanted) == 0 {
		return ""
	}

	capped := false
	if len(wanted) > s.ocrMaxPages {
		wanted, capped = wanted[:s.ocrMaxPages], true
	}
	if allowed, used, ok := s.budgetRoom(ctx, doc.CompanyID, len(wanted)); !ok {
		// Refused before any model call, which is T-P11's acceptance line
		// word for word. The sentence names both numbers because "budget
		// exceeded" is not something a tenant can act on.
		return fmt.Sprintf(
			"this workspace has had %d of %d document pages read by a model this month, "+
				"so %d scanned page(s) here were left unread",
			used, allowed, len(wanted))
	}

	images, err := s.renderer.Render(ctx, bytes.NewReader(data), wanted)
	if err != nil {
		logrus.WithError(err).WithField("document_id", doc.ID).
			Warn("scanned pages could not be rendered; they are left unread")
		return ""
	}

	byNumber := map[int]*docparse.Page{}
	for i := range parsed.Pages {
		byNumber[parsed.Pages[i].Number] = &parsed.Pages[i]
	}

	var (
		read    int
		cost    int64
		started = time.Now()
	)
	for _, img := range images {
		if img.Error != "" || img.Base64 == "" {
			continue
		}
		page := byNumber[img.Number]
		if page == nil {
			continue
		}
		text, usage, err := s.ocr.ReadPage(ctx, img.ContentType, img.Base64)
		if err != nil {
			// One page, not the document. A provider that fails on page four of
			// a forty-page scan should cost page four, and the retry is the next
			// re-parse rather than a loop here spending on every page again.
			logrus.WithError(err).WithFields(logrus.Fields{
				"document_id": doc.ID, "page": img.Number,
			}).Warn("a scanned page could not be read by the model")
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		page.Kind = docparse.KindOCR
		page.Markdown = text
		page.CharCount = len(strings.TrimSpace(text))
		read++
		if s.usage != nil {
			cost += s.usage.RecordDocumentOCR(ctx, doc.CompanyID, doc.ID,
				s.ocr.Model(), usage.PromptTokens, usage.CompletionTokens)
		}
	}

	if read > 0 {
		if err := s.docs.RecordOCR(ctx, doc.ID, read, cost); err != nil {
			logrus.WithError(err).WithField("document_id", doc.ID).
				Warn("pages were read by a model but the meter could not be written")
		}
		metrics.DocumentPagesOCR(doc.CompanyID, read)
	}
	logrus.WithFields(logrus.Fields{
		"company_id": doc.CompanyID, "document_id": doc.ID,
		"pages_ocr": read, "pages_requested": len(wanted),
		"model": s.ocr.Model(), "cost_micro_usd": cost,
		"ms": time.Since(started).Milliseconds(),
	}).Info("scanned pages read by a model")

	switch {
	case capped:
		return fmt.Sprintf(
			"%d scanned page(s) were read by a model; the rest were left unread at this deployment's "+
				"per-document limit of %d", read, s.ocrMaxPages)
	case read < len(wanted):
		return fmt.Sprintf("%d of %d scanned page(s) were read by a model; the others could not be read",
			read, len(wanted))
	case read > 0:
		return ""
	default:
		return ""
	}
}

// budgetRoom answers whether this company may have `want` more pages read this
// month (T-P11).
//
// A read that fails is fail-open, deliberately and narrowly: the meter is a
// cost control, not a security boundary, and a control database hiccup that
// silently stopped every tenant's ingestion would be a worse outage than a
// month that overshoots its page budget by one document. The failure is logged
// where the budget is set.
func (s *DocumentParseService) budgetRoom(ctx context.Context, companyID string, want int) (allowed, used int, ok bool) {
	if s.pagesPerMonth <= 0 {
		return 0, 0, true
	}
	now := time.Now().UTC()
	since := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	used, err := s.docs.OCRPagesSince(ctx, companyID, since)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("monthly document page budget could not be read; allowing this parse")
		return s.pagesPerMonth, 0, true
	}
	return s.pagesPerMonth, used, used+want <= s.pagesPerMonth
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

// DocumentArtifactPrefix is what PageArtifactKey and ManifestKey share: every
// object the parse wrote for one document, and nothing belonging to another.
//
// Named here, beside the two keys it covers, so a third artifact added later
// falls under the delete automatically. That is the failure it exists to
// prevent: the two functions above were added by T-P2 and the delete written by
// T-P1 knew about neither.
func DocumentArtifactPrefix(companyID, sha string) string {
	return fmt.Sprintf("source-documents/%s/%s/", companyID, sha)
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
