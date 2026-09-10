package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The sweep's tests are about one decision — archiving an example a tenant paid
// an embedding call to learn — and every case below is a way of getting that
// decision wrong in the direction that loses something.

// fakeWarehouse answers "what tables does this source have" and records how
// often it was asked, because one introspection per *source* rather than per
// example is the difference between three round trips and four hundred.
type fakeWarehouse struct {
	tables map[string][]string // source id -> table names
	err    error
	calls  int
}

func (f *fakeWarehouse) For(_ context.Context, _, sourceID string) (db.Conn, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	names, ok := f.tables[sourceID]
	if !ok {
		return nil, errors.New("no such source")
	}
	return &sweepConn{names: names}, nil
}

type sweepConn struct {
	db.Conn
	names []string
}

func (c *sweepConn) ExtractSchema(context.Context) (*db.SchemaMetadata, error) {
	out := &db.SchemaMetadata{}
	for _, n := range c.names {
		out.Tables = append(out.Tables, db.TableInfo{Name: n})
	}
	return out, nil
}

func sweepService(ex *fakeExamples, wh *fakeWarehouse) *CookbookService {
	s := NewCookbookService(ex, &fakeCandidates{}, &fakeVerdicts{}, &fakeEmbedder{})
	if wh != nil {
		s = s.WithSweep(wh)
	}
	return s
}

// The whole point: a query against a table the warehouse no longer has is
// archived, and the one beside it against a table that still exists is not.
func TestSweepArchivesAnExampleWhoseTableIsGone(t *testing.T) {
	ex := &fakeExamples{refs: []domain.QueryExampleRef{
		{ID: 1, SourceID: "src-a", SQL: "SELECT sum(amount) FROM fact_sales WHERE year = 2026"},
		{ID: 2, SourceID: "src-a", SQL: "SELECT count(*) FROM dim_customer"},
	}}
	wh := &fakeWarehouse{tables: map[string][]string{"src-a": {"dim_customer", "dim_date"}}}

	got, err := sweepService(ex, wh).Sweep(context.Background(), "co-1", 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got.ArchivedDrift != 1 {
		t.Errorf("archived_drift = %d, want 1 (%+v)", got.ArchivedDrift, got)
	}
	if ex.archived[1] != domain.ArchiveReasonSchemaDrift {
		t.Errorf("example 1 reason = %q, want %q", ex.archived[1], domain.ArchiveReasonSchemaDrift)
	}
	if _, gone := ex.archived[2]; gone {
		t.Error("example 2 queries a table that still exists and was archived anyway")
	}
}

// One introspection per source, not per example. A tenant with four hundred
// examples across two warehouses must not open four hundred connections.
func TestSweepIntrospectsEachSourceOnce(t *testing.T) {
	ex := &fakeExamples{refs: []domain.QueryExampleRef{
		{ID: 1, SourceID: "src-a", SQL: "SELECT * FROM t1"},
		{ID: 2, SourceID: "src-a", SQL: "SELECT * FROM t1"},
		{ID: 3, SourceID: "src-a", SQL: "SELECT * FROM t1"},
		{ID: 4, SourceID: "src-b", SQL: "SELECT * FROM t2"},
	}}
	wh := &fakeWarehouse{tables: map[string][]string{"src-a": {"t1"}, "src-b": {"t2"}}}

	if _, err := sweepService(ex, wh).Sweep(context.Background(), "co-1", 0); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if wh.calls != 2 {
		t.Errorf("opened %d connections for 2 sources — LiveRefs is ordered by source so this should be one each", wh.calls)
	}
}

// A warehouse that is down has not dropped a table. Treating "we could not
// ask" as "it is gone" would empty a tenant's cookbook during an outage, which
// is the one failure here that is both silent and expensive.
func TestSweepKeepsEverythingWhenTheSourceIsUnreachable(t *testing.T) {
	ex := &fakeExamples{refs: []domain.QueryExampleRef{
		{ID: 1, SourceID: "src-a", SQL: "SELECT sum(amount) FROM fact_sales"},
	}}
	wh := &fakeWarehouse{err: errors.New("dial tcp: connection refused")}

	got, err := sweepService(ex, wh).Sweep(context.Background(), "co-1", 0)
	if err != nil {
		t.Fatalf("sweep must not fail the run because one warehouse is down: %v", err)
	}
	if got.ArchivedDrift != 0 || len(ex.archived) != 0 {
		t.Errorf("archived %d examples against an unreachable source", len(ex.archived))
	}
	if got.UnreachableSources != 1 {
		t.Errorf("unreachable_sources = %d, want 1 — the number is how anybody notices", got.UnreachableSources)
	}
}

// A source reporting zero tables is a permissions change far more often than a
// warehouse somebody emptied, and the two are indistinguishable from here.
func TestSweepDeclinesToArchiveOnAnEmptySchema(t *testing.T) {
	ex := &fakeExamples{refs: []domain.QueryExampleRef{
		{ID: 1, SourceID: "src-a", SQL: "SELECT sum(amount) FROM fact_sales"},
	}}
	wh := &fakeWarehouse{tables: map[string][]string{"src-a": {}}}

	got, err := sweepService(ex, wh).Sweep(context.Background(), "co-1", 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(ex.archived) != 0 {
		t.Errorf("archived %d examples because a source reported no tables at all", len(ex.archived))
	}
	if got.UnreachableSources != 1 {
		t.Errorf("unreachable_sources = %d, want 1", got.UnreachableSources)
	}
}

// SQL the lexer cannot attribute to tables is kept and counted, never archived.
// The count is the instrument: if it climbs, the sweep has gone blind rather
// than found nothing.
func TestSweepKeepsSQLItCannotRead(t *testing.T) {
	ex := &fakeExamples{refs: []domain.QueryExampleRef{
		// An unbalanced parenthesis: sqlguard reports Uncertain rather than
		// guessing, and this is the case where guessing costs an example.
		{ID: 1, SourceID: "src-a", SQL: "SELECT * FROM (SELECT * FROM fact_sales"},
	}}
	wh := &fakeWarehouse{tables: map[string][]string{"src-a": {"dim_customer"}}}

	got, err := sweepService(ex, wh).Sweep(context.Background(), "co-1", 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(ex.archived) != 0 {
		t.Errorf("archived an example whose SQL could not be read: %+v", ex.archived)
	}
	if got.SkippedUnreadable != 1 {
		t.Errorf("skipped_unreadable = %d, want 1 — an unread example must be visible, not invisible", got.SkippedUnreadable)
	}
}

// A warehouse reports `public.fact_sales` and a query says `fact_sales`. Both
// spellings are right, and a mismatch here archives every example against that
// source in one sweep — the loudest possible way to get this wrong.
func TestSweepMatchesQualifiedAndBareTableNames(t *testing.T) {
	ex := &fakeExamples{refs: []domain.QueryExampleRef{
		{ID: 1, SourceID: "src-a", SQL: "SELECT sum(amount) FROM fact_sales"},
		{ID: 2, SourceID: "src-a", SQL: "SELECT count(*) FROM public.dim_customer"},
	}}
	// The live side qualified, the queries mixed.
	wh := &fakeWarehouse{tables: map[string][]string{"src-a": {"public.fact_sales", "dim_customer"}}}

	got, err := sweepService(ex, wh).Sweep(context.Background(), "co-1", 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got.ArchivedDrift != 0 {
		t.Errorf("archived %d examples over a schema qualifier: %+v", got.ArchivedDrift, ex.archived)
	}
}

// The age half needs no warehouse, so a deployment that wired no pool — or one
// whose sources are all down — still gets it.
func TestSweepArchivesOnAgeWithoutAWarehouse(t *testing.T) {
	ex := &fakeExamples{unusedCount: 4}
	got, err := sweepService(ex, nil).Sweep(context.Background(), "co-1", SweepUnusedAfter)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got.ArchivedUnused != 4 {
		t.Errorf("archived_unused = %d, want 4", got.ArchivedUnused)
	}
	if len(ex.unusedCalls) != 1 {
		t.Fatalf("ArchiveUnused called %d times, want 1", len(ex.unusedCalls))
	}
	if age := time.Since(ex.unusedCalls[0]); age < SweepUnusedAfter-time.Minute {
		t.Errorf("cutoff was %s ago, want about %s — a shorter window empties quiet tenants", age, SweepUnusedAfter)
	}
}

// Zero switches the age half off entirely. It is the setting a deployment picks
// when it would rather keep a useless example than lose a seasonal one.
func TestSweepSkipsTheAgeHalfWhenDisabled(t *testing.T) {
	ex := &fakeExamples{unusedCount: 9}
	got, err := sweepService(ex, nil).Sweep(context.Background(), "co-1", 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got.ArchivedUnused != 0 || len(ex.unusedCalls) != 0 {
		t.Errorf("age half ran with unusedAfter=0: archived %d, calls %d", got.ArchivedUnused, len(ex.unusedCalls))
	}
}
