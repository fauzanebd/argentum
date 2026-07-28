package docgen

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/spec"
)

type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	failOn  string
}

func newFakeStore() *fakeStore { return &fakeStore{objects: map[string][]byte{}} }

func (s *fakeStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	if s.failOn == key || s.failOn == "*" {
		return "", errors.New("storage: put failed")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	return "http://store.invalid/" + key, nil
}

func (s *fakeStore) PresignKey(_ context.Context, key string, ttl time.Duration) (string, error) {
	return "http://store.invalid/" + key + "?exp=" + ttl.String(), nil
}

type fakeDocs struct {
	mu   sync.Mutex
	rows []*domain.Document
	fail bool
}

func (f *fakeDocs) Insert(_ context.Context, d *domain.Document) error {
	if f.fail {
		return errors.New("insert failed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d.CreatedAt = time.Unix(1_800_000_000, 0)
	cp := *d
	f.rows = append(f.rows, &cp)
	return nil
}

func (f *fakeDocs) GetByID(context.Context, string) (*domain.Document, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeDocs) GetForCompany(context.Context, string, string) (*domain.Document, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeDocs) ListByCompany(context.Context, string, domain.DocumentFilter) ([]*domain.Document, bool, error) {
	return nil, false, nil
}
func (f *fakeDocs) ListByThread(context.Context, string) ([]*domain.Document, error) {
	return nil, nil
}
func (f *fakeDocs) NewestForThreadSince(context.Context, string, string, time.Time) (*domain.Document, error) {
	return nil, domain.ErrNotFound
}

type countingMeter struct {
	calls   int
	threads []string
}

func (m *countingMeter) RecordDocument(_ context.Context, _, threadID, _ string) {
	m.calls++
	m.threads = append(m.threads, threadID)
}

// csvSpec is the cheapest format that exercises the whole path: no fonts, no
// branding lookup, deterministic bytes.
func csvSpec() *spec.Document {
	return &spec.Document{
		Format: "csv",
		Title:  "Monthly sales",
		Content: spec.Content{Table: &spec.Table{
			Columns: []spec.Column{{Label: "Month"}, {Label: "Revenue", Fmt: "number"}},
			Rows: [][]spec.Cell{
				{{V: "2026-05"}, {V: 3863405700}},
				{{V: "2026-06"}, {V: 4012118800}},
			},
		}},
	}
}

func newTestService(store ObjectStore, docs domain.DocumentRepository, meter Meter) *Service {
	return New(store, docs, nil, nil, meter, time.Hour)
}

// The render door has no thread, so its key cannot carry one. The threaded key
// is untouched for every other path — rewriting it would strand every document
// generated before T-A2.
func TestStorageKeyBranchesOnTheThread(t *testing.T) {
	cases := []struct {
		name     string
		threadID string
		wantPart string
	}{
		{"render door", "", "documents/co-1/api/"},
		{"agent path", "th-9", "documents/co-1/th-9/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, docs := newFakeStore(), &fakeDocs{}
			svc := newTestService(store, docs, nil)

			res, err := svc.Generate(context.Background(), Input{
				Spec: csvSpec(), CompanyID: "co-1", ThreadID: tc.threadID,
			})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if !strings.HasPrefix(res.Document.StorageKey, tc.wantPart) {
				t.Errorf("storage key %q, want prefix %q", res.Document.StorageKey, tc.wantPart)
			}
			if !strings.HasSuffix(res.Document.StorageKey, ".csv") {
				t.Errorf("storage key %q has no format extension", res.Document.StorageKey)
			}
			if _, ok := store.objects[res.Document.StorageKey]; !ok {
				t.Error("nothing was uploaded under the key the row records")
			}
		})
	}
}

// source and api_key_id are what make "which door produced this, and which
// credential paid for it" answerable — one of the four layers the sprint's
// risk register names against a leaked key.
func TestGenerateRecordsItsProvenance(t *testing.T) {
	docs := &fakeDocs{}
	svc := newTestService(newFakeStore(), docs, nil)

	res, err := svc.Generate(context.Background(), Input{
		Spec: csvSpec(), CompanyID: "co-1",
		Source: domain.DocumentSourceAPI, APIKeyID: "key-7",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Document.Source != domain.DocumentSourceAPI {
		t.Errorf("source = %q, want api", res.Document.Source)
	}
	if res.Document.APIKeyID != "key-7" {
		t.Errorf("api_key_id = %q, want key-7", res.Document.APIKeyID)
	}
	if res.Document.ThreadID != "" {
		t.Errorf("thread_id = %q, want empty for the render door", res.Document.ThreadID)
	}

	// An unset source defaults to the agent, so a caller that predates T-A2
	// still writes what it always wrote.
	res, err = svc.Generate(context.Background(), Input{Spec: csvSpec(), CompanyID: "co-1", ThreadID: "th-1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Document.Source != domain.DocumentSourceAgent {
		t.Errorf("source = %q, want agent by default", res.Document.Source)
	}
}

// The caps exist to stop a renderer being handed 500 000 rows, which only
// works if nothing renders first. The proof is that no object was uploaded:
// the upload happens after the render, so an empty store means the render
// never ran.
func TestLimitsRejectBeforeAnythingRenders(t *testing.T) {
	store, docs := newFakeStore(), &fakeDocs{}
	meter := &countingMeter{}
	svc := newTestService(store, docs, meter).WithLimits(spec.Limits{MaxRows: 1})

	huge := csvSpec() // two rows against a cap of one
	_, err := svc.Generate(context.Background(), Input{
		Spec: huge, CompanyID: "co-1", EnforceLimits: true,
	})
	if err == nil {
		t.Fatal("an oversized spec was accepted")
	}
	if le, ok := AsLimitError(err); !ok {
		t.Errorf("error is %T, want a *spec.LimitError a handler can name a param from", err)
	} else if le.Param == "" {
		t.Error("LimitError names no param")
	}
	if len(store.objects) != 0 {
		t.Error("something was uploaded; the cap ran after the render")
	}
	if len(docs.rows) != 0 {
		t.Error("a document row was written for a refused spec")
	}
	if meter.calls != 0 {
		t.Error("a refused spec was metered")
	}
}

// The agent's own spec is not checked against the API's caps: it comes from a
// model on the other side of a tool description that already asks for small
// tables, and a turn refused by a row cap the agent cannot see fails with
// nothing to act on.
func TestLimitsAreOptOut(t *testing.T) {
	svc := newTestService(newFakeStore(), &fakeDocs{}, nil).WithLimits(spec.Limits{MaxRows: 1})
	if _, err := svc.Generate(context.Background(), Input{
		Spec: csvSpec(), CompanyID: "co-1", ThreadID: "th-1", EnforceLimits: false,
	}); err != nil {
		t.Fatalf("the agent path was refused by an API cap: %v", err)
	}
}

// Upload before insert, so a failed upload leaves no row pointing at an object
// that is not there. The reverse order produces a document a caller can list
// and cannot download, which is the worse of the two failures.
func TestAFailedUploadLeavesNoRow(t *testing.T) {
	store := newFakeStore()
	store.failOn = "*"
	docs := &fakeDocs{}
	meter := &countingMeter{}
	svc := newTestService(store, docs, meter)

	if _, err := svc.Generate(context.Background(), Input{Spec: csvSpec(), CompanyID: "co-1"}); err == nil {
		t.Fatal("Generate succeeded with a failing store")
	}
	if len(docs.rows) != 0 {
		t.Error("an orphan document row survived a failed upload")
	}
	if meter.calls != 0 {
		t.Error("a document that was never stored was metered")
	}
}

func TestGenerateMetersOnce(t *testing.T) {
	meter := &countingMeter{}
	svc := newTestService(newFakeStore(), &fakeDocs{}, meter)
	if _, err := svc.Generate(context.Background(), Input{Spec: csvSpec(), CompanyID: "co-1", ThreadID: "th-2"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if meter.calls != 1 {
		t.Fatalf("metered %d times, want 1", meter.calls)
	}
	if meter.threads[0] != "th-2" {
		t.Errorf("metered thread %q, want th-2", meter.threads[0])
	}
}

// The bytes come back with the result so the render door can answer with them
// inline. A round trip through object storage to hand back what is already in
// memory is latency bought for nothing.
func TestGenerateReturnsTheBytesItStored(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store, &fakeDocs{}, nil)
	res, err := svc.Generate(context.Background(), Input{Spec: csvSpec(), CompanyID: "co-1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(res.Data) != string(store.objects[res.Document.StorageKey]) {
		t.Error("the returned bytes differ from the stored object")
	}
	if int64(len(res.Data)) != res.Document.SizeBytes {
		t.Errorf("size_bytes = %d, len(data) = %d", res.Document.SizeBytes, len(res.Data))
	}

	rows, err := csv.NewReader(strings.NewReader(string(res.Data))).ReadAll()
	if err != nil {
		t.Fatalf("the CSV does not parse: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d CSV rows, want a header and two data rows", len(rows))
	}
}

func TestNormalizeFilename(t *testing.T) {
	cases := []struct {
		suggested, title string
		format           domain.DocumentFormat
		want             string
	}{
		{"report.pdf", "", domain.DocumentFormatPDF, "report.pdf"},
		{"report", "", domain.DocumentFormatPDF, "report.pdf"},
		{"report.txt", "", domain.DocumentFormatPDF, "report.pdf"},
		{"a\\b.csv", "", domain.DocumentFormatCSV, "a_b.csv"},
	}
	for _, tc := range cases {
		if got := NormalizeFilename(tc.suggested, tc.title, tc.format); got != tc.want {
			t.Errorf("NormalizeFilename(%q) = %q, want %q", tc.suggested, got, tc.want)
		}
	}

	// The property that matters for a name chosen by a stranger: nothing that
	// reaches a Content-Disposition header can carry a path separator, because
	// whoever downloads it writes it to disk. The exact output is not asserted
	// — the extension rewrite truncates at the last dot wherever it falls, so
	// "../../etc/passwd" comes back as ".._..csv" rather than a readable name.
	// That is pre-T-A2 behaviour, it is ugly, and it is safe; a caller who
	// wants a readable filename sends one.
	for _, hostile := range []string{"../../etc/passwd", "a/b/c.csv", "x\x00.csv", "..\\..\\win.ini"} {
		got := NormalizeFilename(hostile, "", domain.DocumentFormatCSV)
		if strings.ContainsAny(got, "/\\\x00") {
			t.Errorf("NormalizeFilename(%q) = %q — a separator survived", hostile, got)
		}
	}

	// No suggestion and no title still produces something with the right
	// extension rather than a bare timestamp.
	if got := NormalizeFilename("", "", domain.DocumentFormatXLSX); !strings.HasPrefix(got, "document-") || !strings.HasSuffix(got, ".xlsx") {
		t.Errorf("NormalizeFilename(\"\", \"\") = %q", got)
	}
}

func TestGenerateRefusesAnEmptyTenant(t *testing.T) {
	svc := newTestService(newFakeStore(), &fakeDocs{}, nil)
	if _, err := svc.Generate(context.Background(), Input{Spec: csvSpec()}); err == nil {
		t.Fatal("Generate accepted a document with no company")
	}
	if _, err := svc.Generate(context.Background(), Input{CompanyID: "co-1"}); err == nil {
		t.Fatal("Generate accepted a nil spec")
	}
}
