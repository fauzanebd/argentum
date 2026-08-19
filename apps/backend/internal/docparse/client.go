// Package docparse is the client for `apps/docparse`, the Python service that
// reads a PDF's own text layer and says which pages it could not (T-P2).
//
// It is the second outbound dependency this backend has, and it is shaped like
// the first (`internal/report/video`) for the reason that package states: a
// call into another container fails in ways that mean different things to
// whoever reads the message, and collapsing them into "parse failed" throws
// away the only information the reader needed.
//
//   - not configured on this deployment;
//   - configured and unreachable, or broken;
//   - reachable and the document was refused.
//
// The third one is the interesting one here, because a refusal is *terminal*:
// a document with 400 pages will have 400 pages on the retry, and a queue that
// retries it is a queue that fills with work nobody wants done.
//
// **No page content is ever logged from this package.** The pages are the
// tenant's document — a supplier's invoice, a bank statement — and the service
// itself is careful about this. A client that logged its response body would
// undo that from our side.
package docparse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The failures, told apart.
var (
	// ErrNotConfigured means this deployment has no parser. A valid deployment,
	// exactly as one without object storage is: uploaded documents rest at
	// `uploaded` and nothing pretends to have read them.
	ErrNotConfigured = errors.New("document parsing is not configured on this deployment")
	// ErrUnavailable is the service being unreachable, slow or broken. The only
	// one of these a retry could fix.
	ErrUnavailable = errors.New("the document parser could not be reached")
	// ErrRefused is the parser reading the document and declining it — too many
	// pages, or not a PDF it can open. Deterministic, so nothing retries it.
	ErrRefused = errors.New("the document parser refused this document")
)

// Parser is the narrow contract the parse service depends on.
//
// An interface with one method, so the hosted-parser option the roadmap keeps
// open is a swap of this implementation and nothing else — and so the service
// above it can be tested without a Python process.
type Parser interface {
	// Parse reads the whole document. maxPages of 0 means no limit; a document
	// over the limit comes back as ErrRefused with the count in the message,
	// because the caller's next act is to write that sentence onto a row a
	// person will read.
	Parse(ctx context.Context, body io.Reader, maxPages int) (*Document, error)
}

// Document is one parsed file.
type Document struct {
	PageCount int        `json:"page_count"`
	Parser    ParserInfo `json:"parser"`
	Pages     []Page     `json:"pages"`
}

// ParserInfo is which build produced this. Recorded on the artifact and read
// back by T-P13, for the reason T-Q15 exists: a score that cannot name what
// produced it cannot be re-run as the same measurement — and a sidecar serving
// from a previous image looks exactly like a passing run.
type ParserInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Page kinds.
const (
	// KindText means the page's own text layer was read and can be believed.
	KindText = "text"
	// KindNeedsOCR means it could not: no text, a broken font map, or a scan.
	// T-P3 is what turns these into content, and until it exists they are
	// counted and left empty rather than guessed at.
	KindNeedsOCR = "needs_ocr"
	// KindFailed is one page that raised while the rest of the document parsed.
	KindFailed = "failed"
	// KindOCR is a page the text layer could not read and a model did (T-P3).
	// A third state rather than promoting the page to `text`, because the two
	// are not equally trustworthy and every later reader — the reviewer, the
	// eval set, whoever is asked why a figure is wrong — needs to know which
	// one produced a given line.
	KindOCR = "ocr"
)

// Page is what the parser found on one page.
type Page struct {
	Number int     `json:"page_no"`
	Kind   string  `json:"kind"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	// CharCount, AlnumRatio and ImageAreaRatio are the three numbers the
	// routing decision was made from. They are carried rather than dropped so
	// that "why was this page sent to OCR?" is answerable from the artifact
	// instead of by re-running the parser.
	CharCount      int     `json:"char_count"`
	AlnumRatio     float64 `json:"alnum_ratio"`
	ImageAreaRatio float64 `json:"image_area_ratio"`
	Markdown       string  `json:"markdown"`
	Words          []Word  `json:"words"`
	Tables         []Table `json:"tables"`
	// HiddenCharCount is how many characters the sidecar dropped as invisible
	// — type below the legibility floor, or type the colour of the page
	// (T-P10). Carried rather than discarded because a page holding two hundred
	// characters nobody can see is a fact a reviewer should be shown, and a
	// hygiene step with no counter is one nobody can prove ran.
	HiddenCharCount int    `json:"hidden_char_count,omitempty"`
	Error           string `json:"error,omitempty"`
}

// Word is one word and where it sat, in PDF points from the top-left.
type Word struct {
	Text   string  `json:"text"`
	X0     float64 `json:"x0"`
	Top    float64 `json:"top"`
	X1     float64 `json:"x1"`
	Bottom float64 `json:"bottom"`
}

// Table is a candidate: a grid of strings and the rectangle it came from.
//
// Candidate, not table. Whether these rows mean anything — which is the header,
// what a TOTAL row is, what the numbers are — is T-P4, and whether it becomes
// data is a person's decision in T-P7.
type Table struct {
	Index int `json:"index"`
	// Strategy is `lines` when ruling lines defined the grid and `text` when
	// word alignment did. It reaches the reviewer because the second is an
	// inference: a wrong column boundary looks exactly like a right one until
	// somebody reads the page beside it.
	Strategy string     `json:"strategy"`
	BBox     []float64  `json:"bbox"`
	Rows     [][]string `json:"rows"`
	RowCount int        `json:"row_count"`
	ColCount int        `json:"col_count"`
}

// TextPages is how many pages were read, which is the number worth logging
// beside the total: a document where it is zero parsed successfully and
// produced nothing.
func (d *Document) TextPages() int {
	n := 0
	for _, p := range d.Pages {
		if p.Kind == KindText {
			n++
		}
	}
	return n
}

// NeedsOCRPages is how many pages this build cannot read. The number that
// decides whether T-P3 is worth turning on for a tenant.
func (d *Document) NeedsOCRPages() int {
	n := 0
	for _, p := range d.Pages {
		if p.Kind == KindNeedsOCR {
			n++
		}
	}
	return n
}

// RenderedPage is one page as an image, for the OCR path (T-P3).
type RenderedPage struct {
	Number int `json:"page_no"`
	DPI    int `json:"dpi"`
	// ContentType and Base64 are what a multimodal model needs as a data URI,
	// and they are kept apart so the caller builds that string rather than this
	// package assuming which provider will read it.
	ContentType string `json:"content_type"`
	Base64      string `json:"base64"`
	Error       string `json:"error,omitempty"`
}

// Renderer turns pages nobody could read into images (T-P3).
//
// A second interface rather than a second method on Parser, because the two
// have different costs and a caller should have to ask for the expensive one by
// name: parsing stays inside the deployment and rendering exists to send a page
// to a model. Every implementation of Parser here also implements this, and a
// caller that does not want OCR simply never asks.
type Renderer interface {
	Render(ctx context.Context, body io.Reader, pages []int) ([]RenderedPage, error)
}

// Options configures an HTTPParser.
type Options struct {
	// BaseURL of the parser, e.g. http://argentum-docparse:8091. Empty means
	// this deployment has none.
	BaseURL string
	// Secret is sent as `x-docparse-secret`. Empty matches a service with no
	// secret set, which is the developer-machine configuration.
	Secret string
	// Timeout bounds one whole parse. It sits above the service's own work
	// rather than below it: a parser that is slow on a 200-page scan should
	// answer us, not be cut off mid-answer and reported as unreachable.
	Timeout time.Duration
}

// HTTPParser talks to one parser service.
type HTTPParser struct {
	base    string
	secret  string
	http    *http.Client
	timeout time.Duration
}

// New returns a parser, or nil when no base URL is configured.
//
// Nil rather than an error, and nil is legal everywhere it is used: the caller
// checks once at construction and a deployment without a parser is a supported
// configuration, not a broken one.
func New(o Options) *HTTPParser {
	if o.BaseURL == "" {
		return nil
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	return &HTTPParser{
		base:   o.BaseURL,
		secret: o.Secret,
		// No timeout on the client: the deadline lives on the context, so a
		// caller with a shorter one wins and a caller with none still gets the
		// bound below.
		http:    &http.Client{},
		timeout: o.Timeout,
	}
}

// Parse sends the bytes and returns what came back.
func (p *HTTPParser) Parse(ctx context.Context, body io.Reader, maxPages int) (*Document, error) {
	if p == nil {
		return nil, ErrNotConfigured
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read document: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	url := p.base + "/parse?max_pages=" + strconv.Itoa(maxPages)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build parse request: %w", err)
	}
	req.Header.Set("Content-Type", "application/pdf")
	if p.secret != "" {
		req.Header.Set("x-docparse-secret", p.secret)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		// Unreachable, refused connection, DNS, or the deadline. All of them are
		// "try again later", and all of them are wrapped so the caller can tell
		// this apart from a document the parser looked at and declined.
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnprocessableEntity:
		return nil, fmt.Errorf("%w: %s", ErrRefused, refusalReason(resp.Body))
	case http.StatusRequestEntityTooLarge:
		// The parser's own body cap. Terminal for the same reason a page limit
		// is: the file will be the same size next time.
		return nil, fmt.Errorf("%w: the document is larger than the parser accepts", ErrRefused)
	case http.StatusUnauthorized:
		// Not ErrUnavailable: retrying will not fix a wrong shared secret, and
		// calling it "unreachable" sends an operator to look at the network.
		return nil, fmt.Errorf("%w: the parser rejected our shared secret", ErrNotConfigured)
	default:
		return nil, fmt.Errorf("%w: parser answered %d", ErrUnavailable, resp.StatusCode)
	}

	var doc Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: parser answer could not be read: %v", ErrUnavailable, err)
	}
	if doc.PageCount == 0 && len(doc.Pages) == 0 {
		// A 200 carrying nothing is the parser agreeing it read a document with
		// no pages, which no PDF has. Treated as a broken answer rather than an
		// empty document, because writing 'parsed' on it would tell a tenant
		// their file holds nothing.
		return nil, fmt.Errorf("%w: parser returned no pages", ErrUnavailable)
	}
	return &doc, nil
}

// refusalReason pulls the service's own words out of a 422 so the sentence
// stored on the document row says what happened.
//
// The body is read with a cap: it is a small JSON object by contract, and a
// caller that streamed an unbounded error body into a status column would be
// trusting the other side about a size for no reason.
func refusalReason(r io.Reader) string {
	var payload struct {
		Error     string `json:"error"`
		Detail    string `json:"detail"`
		PageCount int    `json:"page_count"`
		MaxPages  int    `json:"max_pages"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 8<<10)).Decode(&payload); err != nil {
		return "the parser declined the document"
	}
	switch payload.Error {
	case "page_limit":
		return fmt.Sprintf("the document has %d pages and this deployment reads at most %d",
			payload.PageCount, payload.MaxPages)
	case "unreadable":
		if payload.Detail != "" {
			return "the file could not be opened as a PDF: " + payload.Detail
		}
		return "the file could not be opened as a PDF"
	default:
		return "the parser declined the document"
	}
}

// Render asks the sidecar for the named pages as PNGs (T-P3).
//
// The whole document is sent again rather than the pages being cached from the
// parse: the sidecar holds no state by design, and a stateful one would be a
// second place a tenant's document lives.
func (p *HTTPParser) Render(ctx context.Context, body io.Reader, pages []int) ([]RenderedPage, error) {
	if p == nil {
		return nil, ErrNotConfigured
	}
	if len(pages) == 0 {
		return nil, nil
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read document: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	list := make([]string, 0, len(pages))
	for _, n := range pages {
		list = append(list, strconv.Itoa(n))
	}
	url := p.base + "/render?pages=" + strings.Join(list, ",")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build render request: %w", err)
	}
	req.Header.Set("Content-Type", "application/pdf")
	if p.secret != "" {
		req.Header.Set("x-docparse-secret", p.secret)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return nil, fmt.Errorf("%w: %s", ErrRefused, refusalReason(resp.Body))
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: the parser rejected our shared secret", ErrNotConfigured)
	default:
		return nil, fmt.Errorf("%w: parser answered %d", ErrUnavailable, resp.StatusCode)
	}

	var out struct {
		Pages []RenderedPage `json:"pages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: render answer could not be read: %v", ErrUnavailable, err)
	}
	return out.Pages, nil
}
