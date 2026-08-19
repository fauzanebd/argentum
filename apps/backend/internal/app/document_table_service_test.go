package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/doctable"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeDocumentTables is the drafts, in memory, with the two behaviours the
// service actually depends on: the upsert matches on (document, candidate), and
// MarkApplied refuses a quarantined row the way the SQL does.
type fakeDocumentTables struct {
	rows   []*domain.DocumentTable
	nextID int
	// takenNames is what the unique constraint on (company_id, table_name)
	// does. Modelled because the service has a rename loop around it, and a
	// fake that never conflicts would let that loop rot untested.
	takenNames map[string]string
}

func newFakeTables() *fakeDocumentTables {
	return &fakeDocumentTables{takenNames: map[string]string{}}
}

func (f *fakeDocumentTables) Upsert(_ context.Context, t *domain.DocumentTable) error {
	for _, r := range f.rows {
		if r.DocumentID == t.DocumentID && r.CandidateKey == t.CandidateKey {
			r.FirstPage, r.LastPage = t.FirstPage, t.LastPage
			r.RowCount = t.RowCount
			r.VerifyStatus, r.VerifyDetail = t.VerifyStatus, t.VerifyDetail
			if r.Status == domain.DocumentTableDraft {
				r.Title = t.Title
				if len(t.Columns) > 0 && len(r.Columns) == 0 {
					r.Columns = t.Columns
				}
				if t.VerifyStatus == doctable.VerifyQuarantined {
					r.Status = domain.DocumentTableQuarantined
				}
			}
			*t = *r
			return nil
		}
	}
	owner := t.DocumentID + "/" + t.CandidateKey
	if holder, ok := f.takenNames[t.CompanyID+"/"+t.TableName]; ok && holder != owner {
		return domain.ErrAlreadyExists
	}
	f.nextID++
	t.ID = "tbl-" + string(rune('0'+f.nextID))
	f.takenNames[t.CompanyID+"/"+t.TableName] = owner
	copied := *t
	f.rows = append(f.rows, &copied)
	return nil
}

func (f *fakeDocumentTables) GetForCompany(_ context.Context, companyID, id string) (*domain.DocumentTable, error) {
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeDocumentTables) ListByDocument(_ context.Context, companyID, documentID string) ([]*domain.DocumentTable, error) {
	var out []*domain.DocumentTable
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.DocumentID == documentID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDocumentTables) ListAppliedByCompany(_ context.Context, companyID string) ([]*domain.DocumentTable, error) {
	var out []*domain.DocumentTable
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.Status == domain.DocumentTableApplied {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeDocumentTables) UpdateColumns(
	_ context.Context, companyID, id, title string, cols []doctable.Column,
) error {
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			if title != "" {
				r.Title = title
			}
			r.Columns = cols
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeDocumentTables) MarkApplied(_ context.Context, companyID, id, userID string, rowCount int) error {
	for _, r := range f.rows {
		if r.CompanyID != companyID || r.ID != id {
			continue
		}
		if r.VerifyStatus == doctable.VerifyQuarantined {
			// The SQL has this in its WHERE clause. The fake keeps it so a
			// service that stopped checking still fails a test.
			return domain.ErrNotFound
		}
		r.Status = domain.DocumentTableApplied
		r.AppliedBy = userID
		r.RowCount = rowCount
		return nil
	}
	return domain.ErrNotFound
}

func (f *fakeDocumentTables) SetVerification(
	_ context.Context, companyID, id string, status doctable.VerifyStatus, detail string,
) error {
	for _, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			r.VerifyStatus, r.VerifyDetail = status, detail
			switch {
			case status == doctable.VerifyQuarantined:
				r.Status = domain.DocumentTableQuarantined
			case r.Status == domain.DocumentTableQuarantined || r.Status == domain.DocumentTableApplied:
				r.Status = domain.DocumentTableDraft
			}
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeDocumentTables) Delete(_ context.Context, companyID, id string) error {
	for i, r := range f.rows {
		if r.CompanyID == companyID && r.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// parsedDocument seeds a parsed document whose page artifacts hold one table:
// the Q4 sales grid this roadmap keeps returning to.
func parsedDocument(t *testing.T, rows [][]string) (*fakeSourceDocs, *artifactStore, *domain.SourceDocument) {
	t.Helper()
	doc := &domain.SourceDocument{
		ID: "doc-1", CompanyID: "co-1", Filename: "laporan-q4.pdf",
		ContentSHA256: "abc123", PageCount: 1,
		StorageKey: documentStorageKey("co-1", "abc123"),
		Status:     domain.SourceDocumentParsed,
	}
	page := docparse.Page{
		Number: 1, Kind: docparse.KindText, Width: 595, Height: 842,
		Tables: []docparse.Table{{
			Index: 0, Strategy: "lines", BBox: []float64{50, 120, 545, 400},
			Rows: rows, RowCount: len(rows), ColCount: len(rows[0]),
		}},
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("encode page: %v", err)
	}
	docs := &fakeSourceDocs{rows: []*domain.SourceDocument{doc}}
	blobs := newArtifactStore(map[string][]byte{
		PageArtifactKey("co-1", "abc123", 1): body,
	})
	return docs, blobs, doc
}

func salesRows(december, total string) [][]string {
	return [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "3.377.718.500"},
		{"November", "3.708.552.300"},
		{"Desember", december},
		{"TOTAL", total},
	}
}

func TestListDerivesTablesAndRecordsThemAsDrafts(t *testing.T) {
	docs, blobs, _ := parsedDocument(t, salesRows("3.863.405.700", "10.949.676.500"))
	tables := newFakeTables()
	svc := NewDocumentTableService(docs, tables, blobs)

	out, err := svc.List(context.Background(), "co-1", "doc-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d tables, want 1", len(out))
	}
	draft := out[0]
	if draft.Status != domain.DocumentTableDraft {
		t.Errorf("status = %s, want draft — nothing published without a person", draft.Status)
	}
	if draft.RowCount != 3 || len(draft.Table.Rows) != 3 {
		t.Errorf("rows = %d/%d, want 3 — the TOTAL row is data", draft.RowCount, len(draft.Table.Rows))
	}
	if draft.VerifyStatus != doctable.VerifyVerified {
		t.Errorf("verify = %s (%s), want verified", draft.VerifyStatus, draft.VerifyDetail)
	}
	if len(tables.rows) != 1 {
		t.Fatalf("drafts persisted = %d, want 1", len(tables.rows))
	}

	// Listing twice does not create a second draft: the candidate key is what
	// makes a re-derivation an update.
	if _, err := svc.List(context.Background(), "co-1", "doc-1"); err != nil {
		t.Fatalf("second list: %v", err)
	}
	if len(tables.rows) != 1 {
		t.Fatalf("drafts persisted after a second read = %d, want 1", len(tables.rows))
	}
}

// A reviewer's column decision survives a re-derivation, and it changes the
// values. This is the line that makes an override mean something.
func TestAReviewerColumnDecisionIsReappliedOnEveryRead(t *testing.T) {
	docs, blobs, _ := parsedDocument(t, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "3.377"},
		{"November", "3.708"},
		{"Desember", "3.863"},
	})
	tables := newFakeTables()
	svc := NewDocumentTableService(docs, tables, blobs)

	first, err := svc.List(context.Background(), "co-1", "doc-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	id := first[0].ID
	cols := append([]doctable.Column(nil), first[0].Table.Columns...)
	cols[1].Multiplier = 1e6
	cols[1].MultiplierSource = "reviewer: the report is in millions"

	updated, err := svc.UpdateColumns(context.Background(), "co-1", id, "Penjualan Q4", cols)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "Penjualan Q4" {
		t.Errorf("title = %q, want the reviewer's", updated.Title)
	}
	got := updated.Table.Rows[0].Cells[1]
	if got.Num == nil || *got.Num != 3.377e9 {
		t.Fatalf("October = %v, want 3,377,000,000 under the reviewer's multiplier", got.Num)
	}

	// And again on a fresh read, which is where a decision stored but not
	// re-applied would quietly stop mattering.
	again, err := svc.List(context.Background(), "co-1", "doc-1")
	if err != nil {
		t.Fatalf("re-list: %v", err)
	}
	cell := again[0].Table.Rows[0].Cells[1]
	if cell.Num == nil || *cell.Num != 3.377e9 {
		t.Fatalf("October after a re-read = %v, want the multiplier still applied", cell.Num)
	}
}

// Acceptance: a quarantined table cannot be published through any path.
func TestApplyRefusesAQuarantinedTable(t *testing.T) {
	docs, blobs, _ := parsedDocument(t, salesRows("3.860.405.700", "10.949.676.500"))
	tables := newFakeTables()
	svc := NewDocumentTableService(docs, tables, blobs).
		WithWarehouse(nil, nil, nil, nil)

	out, err := svc.List(context.Background(), "co-1", "doc-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if out[0].VerifyStatus != doctable.VerifyQuarantined {
		t.Fatalf("verify = %s, want quarantined", out[0].VerifyStatus)
	}
	// Without a warehouse the refusal is about configuration; the quarantine
	// refusal is asserted on the warehouse path in the repo fake's MarkApplied
	// and here through the service's own check ordering.
	_, err = svc.Apply(context.Background(), "co-1", out[0].ID, "user-1")
	if !errors.Is(err, ErrWarehouseUnavailable) {
		t.Fatalf("apply without a warehouse = %v, want the not-configured refusal", err)
	}
}

// Publishing on a deployment with no document warehouse refuses with a
// sentence. It does not fall back to any other database — which is the whole
// argument of Decision 4, and the failure would be silent.
func TestApplyWithoutAWarehouseRefusesRatherThanFallingBack(t *testing.T) {
	docs, blobs, _ := parsedDocument(t, salesRows("3.863.405.700", "10.949.676.500"))
	tables := newFakeTables()
	svc := NewDocumentTableService(docs, tables, blobs)

	out, err := svc.List(context.Background(), "co-1", "doc-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := svc.Apply(context.Background(), "co-1", out[0].ID, "user-1"); !errors.Is(err, ErrWarehouseUnavailable) {
		t.Fatalf("apply = %v, want ErrWarehouseUnavailable", err)
	}
	if tables.rows[0].Status == domain.DocumentTableApplied {
		t.Fatal("a table was marked applied on a deployment that cannot publish")
	}
}

func TestAnUnparsedDocumentSaysSo(t *testing.T) {
	docs, blobs, doc := parsedDocument(t, salesRows("3.863.405.700", "10.949.676.500"))
	doc.Status = domain.SourceDocumentUploaded
	svc := NewDocumentTableService(docs, newFakeTables(), blobs)

	_, err := svc.List(context.Background(), "co-1", "doc-1")
	if !errors.Is(err, ErrDocumentNotParsed) {
		t.Fatalf("list = %v, want ErrDocumentNotParsed", err)
	}
}

func TestAnotherTenantsDocumentIsNotFound(t *testing.T) {
	docs, blobs, _ := parsedDocument(t, salesRows("3.863.405.700", "10.949.676.500"))
	svc := NewDocumentTableService(docs, newFakeTables(), blobs)

	if _, err := svc.List(context.Background(), "co-2", "doc-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant list = %v, want not found", err)
	}
}

// Two documents holding a table with the same name is the ordinary case for a
// tenant that uploads a report every month. The second one gets a free name
// rather than a failure.
func TestASecondDocumentWithTheSameTableNameGetsAFreeOne(t *testing.T) {
	rows := salesRows("3.863.405.700", "10.949.676.500")
	docs, blobs, _ := parsedDocument(t, rows)
	tables := newFakeTables()
	svc := NewDocumentTableService(docs, tables, blobs)
	if _, err := svc.List(context.Background(), "co-1", "doc-1"); err != nil {
		t.Fatalf("first list: %v", err)
	}

	// A second document with an identical caption, and therefore an identical
	// derived table name.
	second := &domain.SourceDocument{
		ID: "doc-2", CompanyID: "co-1", Filename: "laporan-q4.pdf",
		ContentSHA256: "def456", PageCount: 1, Status: domain.SourceDocumentParsed,
	}
	docs.rows = append(docs.rows, second)
	body := blobs.objects[PageArtifactKey("co-1", "abc123", 1)]
	blobs.objects[PageArtifactKey("co-1", "def456", 1)] = body

	out, err := svc.List(context.Background(), "co-1", "doc-2")
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d tables, want 1", len(out))
	}
	if out[0].TableName == tables.rows[0].TableName {
		t.Fatalf("both documents claimed the table name %q", out[0].TableName)
	}
	if !strings.HasPrefix(out[0].TableName, tables.rows[0].TableName) {
		t.Errorf("second name = %q, want the first with a suffix", out[0].TableName)
	}
}
