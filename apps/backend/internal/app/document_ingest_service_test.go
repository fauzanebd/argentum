package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeSourceDocs is an in-memory SourceDocumentRepository keyed the way the real
// table is: (company, sha) unique, ids handed out in order.
type fakeSourceDocs struct {
	rows      []*domain.SourceDocument
	nextID    int
	createErr error
}

func (f *fakeSourceDocs) Create(_ context.Context, d *domain.SourceDocument) error {
	if f.createErr != nil {
		return f.createErr
	}
	for _, r := range f.rows {
		if r.CompanyID == d.CompanyID && r.ContentSHA256 == d.ContentSHA256 {
			return domain.ErrAlreadyExists
		}
	}
	f.nextID++
	d.ID = string(rune('a'+f.nextID-1)) + "-doc"
	copied := *d
	f.rows = append(f.rows, &copied)
	return nil
}

// GetByID is the worker's unscoped read. The fake keeps it beside the scoped
// one so a test that reaches for the wrong one fails on the tenant assertions
// rather than compiling into a false pass.
func (f *fakeSourceDocs) GetByID(_ context.Context, id string) (*domain.SourceDocument, error) {
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeSourceDocs) GetForCompany(_ context.Context, companyID, id string) (*domain.SourceDocument, error) {
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeSourceDocs) GetBySHA(_ context.Context, companyID, sha string) (*domain.SourceDocument, error) {
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.ContentSHA256 == sha {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeSourceDocs) ListByCompany(_ context.Context, companyID string, _, _ int) ([]*domain.SourceDocument, error) {
	var out []*domain.SourceDocument
	for _, r := range f.rows {
		if r.CompanyID == companyID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSourceDocs) UpdateStatus(_ context.Context, id string, st domain.SourceDocumentStatus, detail string, pages int) error {
	for _, r := range f.rows {
		if r.ID == id {
			r.Status, r.StatusDetail = st, detail
			if pages > r.PageCount {
				r.PageCount = pages
			}
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeSourceDocs) Delete(_ context.Context, companyID, id string) error {
	for i, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

type fakeBlobs struct {
	put             map[string][]byte
	removed         []string
	removedPrefixes []string
	putErr          error
	removeErr       error
}

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{put: map[string][]byte{}} }

func (f *fakeBlobs) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	if f.putErr != nil {
		return "", f.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	f.put[key] = b
	return "http://example/" + key, nil
}

func (f *fakeBlobs) RemoveKey(_ context.Context, key string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, key)
	delete(f.put, key)
	return nil
}

// RemovePrefix mirrors the real store: everything under the prefix goes, and
// what went is recorded so a test can assert the parse artifacts were taken and
// another document's were not.
func (f *fakeBlobs) RemovePrefix(_ context.Context, prefix string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	for key := range f.put {
		if strings.HasPrefix(key, prefix) {
			f.removed = append(f.removed, key)
			delete(f.put, key)
		}
	}
	f.removedPrefixes = append(f.removedPrefixes, prefix)
	return nil
}

type fakeParseQueue struct {
	ids []string
	err error
}

func (f *fakeParseQueue) EnqueueDocumentParse(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	f.ids = append(f.ids, id)
	return nil
}

// pdfBytes is a minimal file that passes the magic-byte check. It is not a valid
// PDF and does not need to be: nothing in T-P1 reads the content.
func pdfBytes(body string) io.Reader { return strings.NewReader("%PDF-1.7\n" + body) }

func TestUploadStoresHashesAndQueues(t *testing.T) {
	docs, blobs, q := &fakeSourceDocs{}, newFakeBlobs(), &fakeParseQueue{}
	svc := NewDocumentIngestService(docs, blobs, 25).WithParseQueue(q)

	out, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", UserID: "user-1", Filename: "laporan.pdf", Body: pdfBytes("penjualan"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if out.Deduplicated {
		t.Error("a first upload reported itself as a duplicate")
	}
	if !out.Queued || len(q.ids) != 1 {
		t.Fatalf("expected exactly one parse enqueued, got queued=%v ids=%v", out.Queued, q.ids)
	}
	if out.Document.Status != domain.SourceDocumentUploaded {
		t.Errorf("status = %q, want uploaded", out.Document.Status)
	}
	if out.Document.PageCount != 0 {
		t.Errorf("page count = %d; nothing has read the file yet", out.Document.PageCount)
	}
	// The key is derived from the content hash, not the id or the filename.
	wantKey := documentStorageKey("co-1", out.Document.ContentSHA256)
	if _, ok := blobs.put[wantKey]; !ok {
		t.Fatalf("object not stored under %q; have %v", wantKey, keysOf(blobs.put))
	}
}

func TestUploadOfIdenticalBytesIsIdempotent(t *testing.T) {
	docs, blobs, q := &fakeSourceDocs{}, newFakeBlobs(), &fakeParseQueue{}
	svc := NewDocumentIngestService(docs, blobs, 25).WithParseQueue(q)

	first, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: pdfBytes("same"),
	})
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	second, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan-copy.pdf", Body: pdfBytes("same"),
	})
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if !second.Deduplicated {
		t.Error("the same bytes uploaded twice did not report as a duplicate")
	}
	if second.Document.ID != first.Document.ID {
		t.Errorf("second upload returned a different document: %q vs %q", second.Document.ID, first.Document.ID)
	}
	if len(docs.rows) != 1 {
		t.Errorf("rows = %d, want 1", len(docs.rows))
	}
	// The whole point of the dedupe: no second parse is paid for.
	if len(q.ids) != 1 {
		t.Errorf("parses enqueued = %d, want 1", len(q.ids))
	}
}

// The same file uploaded by two tenants is two documents. Deduplicating across
// the company boundary would let one tenant learn that another holds a file.
func TestUploadDoesNotDeduplicateAcrossCompanies(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)

	for _, co := range []string{"co-1", "co-2"} {
		if _, err := svc.Upload(context.Background(), UploadInput{
			CompanyID: co, Filename: "filing.pdf", Body: pdfBytes("public filing"),
		}); err != nil {
			t.Fatalf("upload for %s: %v", co, err)
		}
	}
	if len(docs.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(docs.rows))
	}
}

func TestUploadRejectsNonPDFOnContent(t *testing.T) {
	svc := NewDocumentIngestService(&fakeSourceDocs{}, newFakeBlobs(), 25)
	// A .docx renamed to .pdf: the extension and the field name are both right
	// and the bytes are not.
	_, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: strings.NewReader("PK\x03\x04 not a pdf"),
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUploadRefusesOversizeWithoutStoring(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	// One megabyte, and a file two bytes over it.
	svc := NewDocumentIngestService(docs, blobs, 1)
	big := strings.Repeat("x", (1<<20)+2)

	_, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "scan.pdf", Body: pdfBytes(big),
	})
	if !errors.Is(err, ErrDocumentTooLarge) {
		t.Fatalf("err = %v, want ErrDocumentTooLarge", err)
	}
	if len(blobs.put) != 0 {
		t.Errorf("an oversized upload reached object storage: %v", keysOf(blobs.put))
	}
	if len(docs.rows) != 0 {
		t.Errorf("an oversized upload wrote a row")
	}
}

// A row that cannot be written must not leave bytes nothing can name.
func TestUploadRemovesTheObjectWhenTheRowFails(t *testing.T) {
	docs := &fakeSourceDocs{createErr: errors.New("control database is down")}
	blobs := newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)

	if _, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: pdfBytes("orphan"),
	}); err == nil {
		t.Fatal("expected the upload to fail")
	}
	if len(blobs.removed) != 1 {
		t.Fatalf("object removals = %d, want 1 (the orphan)", len(blobs.removed))
	}
	if len(blobs.put) != 0 {
		t.Errorf("the orphaned object is still stored: %v", keysOf(blobs.put))
	}
}

// A queue that is briefly unreachable costs the parse, never the upload.
func TestUploadSurvivesAQueueFailure(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	q := &fakeParseQueue{err: errors.New("redis is down")}
	svc := NewDocumentIngestService(docs, blobs, 25).WithParseQueue(q)

	out, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: pdfBytes("queue down"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if out.Queued {
		t.Error("Queued is true after the enqueue failed")
	}
	if len(docs.rows) != 1 {
		t.Errorf("rows = %d, want 1 — the document must survive a queue failure", len(docs.rows))
	}
}

// With no parser configured the upload works and nothing is queued. This is the
// default deployment, not an error state.
func TestUploadWithNoParserLeavesTheDocumentUploaded(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)

	out, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: pdfBytes("no parser"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if out.Queued {
		t.Error("a deployment with no parser reported a queued parse")
	}
	if out.Document.Status != domain.SourceDocumentUploaded {
		t.Errorf("status = %q, want uploaded", out.Document.Status)
	}
}

func TestDeleteRemovesRowAndObject(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)
	out, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: pdfBytes("delete me"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if err := svc.Delete(context.Background(), "co-1", out.Document.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(docs.rows) != 0 {
		t.Errorf("the row survived the delete")
	}
	if len(blobs.put) != 0 {
		t.Errorf("the object survived the delete: %v", keysOf(blobs.put))
	}
}

// The test that was missing, and the reason the defect lived for a day: the
// parse artifacts are written by DocumentParseService, so a test that only ever
// uploads has nothing under the prefix to leave behind. The 2026-08-19 live gate
// found `<sha>/pages/1.json` — the document's own text — surviving a delete,
// with every unit test in this file passing.
func TestDeleteRemovesTheParseArtifactsToo(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)
	ctx := context.Background()

	out, err := svc.Upload(ctx, UploadInput{
		CompanyID: "co-1", Filename: "pelanggan.pdf", Body: pdfBytes("customer list"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	sha := out.Document.ContentSHA256

	// What a parse leaves beside the original: one artifact per page and the
	// manifest. Written straight into the fake, because that is what
	// DocumentParseService.storePages does to the real store.
	blobs.put[PageArtifactKey("co-1", sha, 1)] = []byte(`{"markdown":"andi@maju.co.id"}`)
	blobs.put[PageArtifactKey("co-1", sha, 2)] = []byte(`{"markdown":"budi@sentosa.co.id"}`)
	blobs.put[ManifestKey("co-1", sha)] = []byte(`{"pages":2}`)

	// And another document's, which must survive: the prefix is per document,
	// not per company.
	other := "source-documents/co-1/deadbeef/pages/1.json"
	blobs.put[other] = []byte(`{"markdown":"someone else"}`)

	if err := svc.Delete(ctx, "co-1", out.Document.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	for _, key := range []string{
		PageArtifactKey("co-1", sha, 1),
		PageArtifactKey("co-1", sha, 2),
		ManifestKey("co-1", sha),
	} {
		if _, still := blobs.put[key]; still {
			t.Errorf("the parse artifact survived the delete: %s", key)
		}
	}
	if _, still := blobs.put[other]; !still {
		t.Errorf("delete took another document's artifacts")
	}
}

// An empty prefix would match the whole bucket. The one caller builds it from a
// company id and a content hash, so this is a guard against a future caller
// rather than against this one.
func TestArtifactPrefixIsScopedToOneDocument(t *testing.T) {
	prefix := DocumentArtifactPrefix("co-1", "abc123")
	if prefix != "source-documents/co-1/abc123/" {
		t.Fatalf("unexpected prefix: %q", prefix)
	}
	if !strings.HasPrefix(PageArtifactKey("co-1", "abc123", 7), prefix) {
		t.Errorf("a page artifact is not under the prefix the delete removes")
	}
	if !strings.HasPrefix(ManifestKey("co-1", "abc123"), prefix) {
		t.Errorf("the manifest is not under the prefix the delete removes")
	}
	// The trailing slash is what stops `abc123` from taking `abc1234`.
	if strings.HasPrefix(PageArtifactKey("co-1", "abc1234", 1), prefix) {
		t.Errorf("the prefix reaches a document whose hash merely starts the same")
	}
}

// The tenant boundary is in the query, not in the caller's memory.
func TestAnotherCompanyCannotReadOrDelete(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)
	out, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: pdfBytes("private"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if _, err := svc.Get(context.Background(), "co-2", out.Document.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant Get err = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(context.Background(), "co-2", out.Document.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant Delete err = %v, want ErrNotFound", err)
	}
	if len(docs.rows) != 1 || len(blobs.put) != 1 {
		t.Error("a cross-tenant delete removed something")
	}
}

// A delete whose object removal fails changes nothing, so the retry that follows
// can succeed. The reverse order has no recovery path.
func TestDeleteKeepsTheRowWhenTheObjectCannotBeRemoved(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)
	out, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: "laporan.pdf", Body: pdfBytes("stuck"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	blobs.removeErr = errors.New("object store is down")
	if err := svc.Delete(context.Background(), "co-1", out.Document.ID); err == nil {
		t.Fatal("expected the delete to fail")
	}
	if len(docs.rows) != 1 {
		t.Error("the row was deleted even though its bytes could not be")
	}
}

// A filename arriving as a Windows path is stored as its base name. The object
// key is derived from the hash, so this is about what a list shows rather than
// about where bytes land — but a stored `..\..\etc\passwd` is a trap set for the
// first caller that treats a filename as a path.
func TestUploadNormalizesTheFilename(t *testing.T) {
	docs, blobs := &fakeSourceDocs{}, newFakeBlobs()
	svc := NewDocumentIngestService(docs, blobs, 25)

	out, err := svc.Upload(context.Background(), UploadInput{
		CompanyID: "co-1", Filename: `C:\Users\andi\Desktop\laporan.pdf`, Body: pdfBytes("windows"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if out.Document.Filename != "laporan.pdf" {
		t.Errorf("filename = %q, want laporan.pdf", out.Document.Filename)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The OCR meter (T-P3/T-P11). The fake keeps a running total the way the SQL
// does — additively — because the budget's whole point is that a re-parse does
// not forget what the first pass already spent.
func (f *fakeSourceDocs) RecordOCR(_ context.Context, id string, pages int, costMicroUSD int64) error {
	for _, r := range f.rows {
		if r.ID == id {
			r.OCRPageCount += pages
			r.OCRCostMicroUSD += costMicroUSD
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeSourceDocs) OCRPagesSince(_ context.Context, companyID string, since time.Time) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.CompanyID == companyID && !r.CreatedAt.Before(since) {
			n += r.OCRPageCount
		}
	}
	return n, nil
}
