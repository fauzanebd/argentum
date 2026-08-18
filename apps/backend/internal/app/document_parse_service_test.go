package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeParser returns whatever it was given, and records the page limit it was
// asked to enforce.
type fakeParser struct {
	doc         *docparse.Document
	err         error
	gotMaxPages int
	calls       int
}

func (f *fakeParser) Parse(_ context.Context, body io.Reader, maxPages int) (*docparse.Document, error) {
	f.calls++
	f.gotMaxPages = maxPages
	_, _ = io.ReadAll(body)
	return f.doc, f.err
}

// artifactStore is fakeBlobs plus the download half. Separate from the ingest
// service's fake because the two services need different halves of storage, and
// a shared double would let a change to one hide a break in the other.
type artifactStore struct {
	objects     map[string][]byte
	downloadErr error
	uploadErr   error
}

func newArtifactStore(seed map[string][]byte) *artifactStore {
	if seed == nil {
		seed = map[string][]byte{}
	}
	return &artifactStore{objects: seed}
}

func (a *artifactStore) DownloadKey(_ context.Context, key string) ([]byte, error) {
	if a.downloadErr != nil {
		return nil, a.downloadErr
	}
	b, ok := a.objects[key]
	if !ok {
		return nil, fmt.Errorf("no such key %q", key)
	}
	return b, nil
}

func (a *artifactStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	if a.uploadErr != nil {
		return "", a.uploadErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	a.objects[key] = b
	return "http://example/" + key, nil
}

func seededDoc(t *testing.T) (*fakeSourceDocs, *artifactStore, *domain.SourceDocument) {
	t.Helper()
	doc := &domain.SourceDocument{
		ID: "doc-1", CompanyID: "co-1", Filename: "laporan.pdf",
		ContentSHA256: "abc123", ByteSize: 9,
		StorageKey: documentStorageKey("co-1", "abc123"),
		Status:     domain.SourceDocumentUploaded,
	}
	docs := &fakeSourceDocs{rows: []*domain.SourceDocument{doc}}
	blobs := newArtifactStore(map[string][]byte{doc.StorageKey: []byte("%PDF-1.7\n")})
	return docs, blobs, doc
}

func twoPageDocument() *docparse.Document {
	return &docparse.Document{
		PageCount: 2,
		Parser:    docparse.ParserInfo{Name: "pdfplumber", Version: "0.11.4"},
		Pages: []docparse.Page{
			{Number: 1, Kind: docparse.KindText, CharCount: 812, Markdown: "Laporan",
				Tables: []docparse.Table{{Index: 0, Strategy: "lines", RowCount: 2, ColCount: 2,
					Rows: [][]string{{"Region", "Revenue"}, {"Jakarta", "3.863.405.700"}}}}},
			{Number: 2, Kind: docparse.KindNeedsOCR, CharCount: 3, ImageAreaRatio: 0.97},
		},
	}
}

func TestParseWritesOneArtifactPerPageAndAManifest(t *testing.T) {
	docs, blobs, doc := seededDoc(t)
	parser := &fakeParser{doc: twoPageDocument()}
	svc := NewDocumentParseService(docs, blobs, parser, 200)

	if err := svc.Parse(context.Background(), "doc-1"); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parser.gotMaxPages != 200 {
		t.Errorf("page limit passed to the parser = %d, want 200", parser.gotMaxPages)
	}
	for _, page := range []int{1, 2} {
		key := PageArtifactKey(doc.CompanyID, doc.ContentSHA256, page)
		if _, ok := blobs.objects[key]; !ok {
			t.Errorf("no artifact at %q", key)
		}
	}
	manifest, ok := blobs.objects[ManifestKey(doc.CompanyID, doc.ContentSHA256)]
	if !ok {
		t.Fatal("no manifest written")
	}
	var m parseManifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	// T-P13 reads the parser build off this, for T-Q15's reason: a score that
	// cannot name what produced it cannot be re-run as the same measurement.
	if m.Parser.Version != "0.11.4" || m.PageCount != 2 || len(m.Pages) != 2 {
		t.Errorf("manifest = %+v", m)
	}
	if m.Pages[0].Tables != 1 || m.Pages[1].Kind != docparse.KindNeedsOCR {
		t.Errorf("manifest pages = %+v", m.Pages)
	}
}

// The status is what a tenant reads. A document that is mostly scan must not
// say "ready" without saying how much of it nothing read.
func TestParsedStatusNamesThePagesNobodyRead(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	svc := NewDocumentParseService(docs, blobs, &fakeParser{doc: twoPageDocument()}, 200)

	if err := svc.Parse(context.Background(), "doc-1"); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := docs.rows[0]
	if got.Status != domain.SourceDocumentParsed {
		t.Fatalf("status = %q, want parsed", got.Status)
	}
	if got.PageCount != 2 {
		t.Errorf("page count = %d, want 2", got.PageCount)
	}
	if got.StatusDetail == "" {
		t.Error("a document with an unreadable page says nothing about it")
	}
}

func TestFullyReadableDocumentCarriesNoWarning(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	clean := twoPageDocument()
	clean.Pages[1].Kind = docparse.KindText
	svc := NewDocumentParseService(docs, blobs, &fakeParser{doc: clean}, 200)

	if err := svc.Parse(context.Background(), "doc-1"); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d := docs.rows[0].StatusDetail; d != "" {
		t.Errorf("status detail = %q, want empty on a document that was read in full", d)
	}
}

// A refusal is terminal. It must write the parser's own sentence and return nil,
// because a queue that retries a 412-page document retries it forever.
func TestRefusalIsTerminalAndKeepsTheReason(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	parser := &fakeParser{err: fmt.Errorf("%w: the document has 412 pages and this deployment reads at most 200",
		docparse.ErrRefused)}
	svc := NewDocumentParseService(docs, blobs, parser, 200)

	if err := svc.Parse(context.Background(), "doc-1"); err != nil {
		t.Fatalf("a refusal was returned as retryable: %v", err)
	}
	got := docs.rows[0]
	if got.Status != domain.SourceDocumentFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.StatusDetail != "the document has 412 pages and this deployment reads at most 200" {
		t.Errorf("detail = %q; the Go error prefix should not reach a tenant", got.StatusDetail)
	}
}

// An unreachable parser is the opposite: retryable, and the document goes back
// to `uploaded` rather than sitting at `parsing` after the retries run out.
func TestUnavailableParserLeavesTheDocumentUploadedAndRetries(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	parser := &fakeParser{err: fmt.Errorf("%w: connection refused", docparse.ErrUnavailable)}
	svc := NewDocumentParseService(docs, blobs, parser, 200)

	err := svc.Parse(context.Background(), "doc-1")
	if err == nil {
		t.Fatal("an unreachable parser was reported as a finished parse")
	}
	got := docs.rows[0]
	if got.Status != domain.SourceDocumentUploaded {
		t.Errorf("status = %q, want uploaded — 'parsing' would describe a process that is not running", got.Status)
	}
	if got.StatusDetail == "" {
		t.Error("the row says nothing about why it was not read")
	}
}

func TestMissingBytesFailTheDocumentAndReturnAnError(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	blobs.downloadErr = errors.New("object store is down")
	svc := NewDocumentParseService(docs, blobs, &fakeParser{doc: twoPageDocument()}, 200)

	if err := svc.Parse(context.Background(), "doc-1"); err == nil {
		t.Fatal("expected an error")
	}
	if docs.rows[0].Status != domain.SourceDocumentFailed {
		t.Errorf("status = %q, want failed", docs.rows[0].Status)
	}
}

// Artifacts that cannot be written must not produce a 'parsed' document: the
// status would promise pages that are not there.
func TestArtifactFailureDoesNotMarkParsed(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	blobs.uploadErr = errors.New("bucket is full")
	svc := NewDocumentParseService(docs, blobs, &fakeParser{doc: twoPageDocument()}, 200)

	if err := svc.Parse(context.Background(), "doc-1"); err == nil {
		t.Fatal("expected an error")
	}
	if docs.rows[0].Status == domain.SourceDocumentParsed {
		t.Error("a document whose pages were not stored was marked parsed")
	}
}

// A task that outlives its document is not a failure — somebody deleted the
// document while its parse was queued, which is ordinary.
func TestDeletedDocumentIsNotFound(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	svc := NewDocumentParseService(docs, blobs, &fakeParser{doc: twoPageDocument()}, 200)

	if err := svc.Parse(context.Background(), "doc-gone"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestNoParserIsNotConfigured(t *testing.T) {
	docs, blobs, _ := seededDoc(t)
	svc := NewDocumentParseService(docs, blobs, nil, 200)

	if err := svc.Parse(context.Background(), "doc-1"); !errors.Is(err, ErrParseNotConfigured) {
		t.Fatalf("err = %v, want ErrParseNotConfigured", err)
	}
	if docs.rows[0].Status != domain.SourceDocumentUploaded {
		t.Errorf("status = %q; a deployment with no parser must leave the document alone", docs.rows[0].Status)
	}
}

// Re-parsing the same document overwrites its artifacts rather than
// accumulating a set per attempt — the keys are derived from the content hash,
// which is what makes that true.
func TestReparseOverwritesRatherThanAccumulates(t *testing.T) {
	docs, blobs, doc := seededDoc(t)
	svc := NewDocumentParseService(docs, blobs, &fakeParser{doc: twoPageDocument()}, 200)

	for i := 0; i < 2; i++ {
		if err := svc.Parse(context.Background(), "doc-1"); err != nil {
			t.Fatalf("parse %d: %v", i, err)
		}
	}
	// The uploaded PDF, two pages, one manifest. Nothing else.
	if len(blobs.objects) != 4 {
		keys := make([]string, 0, len(blobs.objects))
		for k := range blobs.objects {
			keys = append(keys, k)
		}
		t.Errorf("objects = %v, want the file, two pages and one manifest", keys)
	}
	if _, ok := blobs.objects[PageArtifactKey(doc.CompanyID, doc.ContentSHA256, 1)]; !ok {
		t.Error("page 1 artifact missing after the second parse")
	}
}
