package taint

import (
	"context"
	"sync"
	"testing"
)

// These are `internal/doctaint`'s tests, carried over when T-H8 widened that
// package into this one, plus the properties the widening added. They are kept
// rather than rewritten because every one of them is a property T-H9's live
// gate depends on, and a rename is exactly the change that quietly drops a
// guarantee nobody re-reads.

func TestATrackerStartsUntainted(t *testing.T) {
	tr := New()
	if tr.Any() || tr.Has(KindDocument) || tr.Has(KindData) {
		t.Fatal("a fresh tracker reports taint")
	}
	if got := tr.Sources(KindDocument); len(got) != 0 {
		t.Fatalf("a fresh tracker names sources: %v", got)
	}
	if got := tr.Kinds(); len(got) != 0 {
		t.Fatalf("a fresh tracker names kinds: %v", got)
	}
}

// The distinction T-H9's gate turns on: a turn where search_documents ran and
// matched nothing is NOT tainted, and a turn where it matched IS.
func TestMarkIsWhatTaints(t *testing.T) {
	tr := New()
	if tr.Has(KindDocument) {
		t.Fatal("untainted precondition failed")
	}
	tr.Mark(KindDocument, "09-scan-invoice.pdf")
	if !tr.Has(KindDocument) {
		t.Fatal("a marked tracker does not report taint")
	}
	if got := tr.Sources(KindDocument); len(got) != 1 || got[0] != "09-scan-invoice.pdf" {
		t.Fatalf("sources = %v", got)
	}
}

// **The property T-H8 must not break.** Reading warehouse rows is not reading a
// document, and T-H9's gate keys on documents alone: if data taint leaked into
// KindDocument, every ordinary analytics turn would need human approval for an
// action its workspace auto-approves.
func TestDataTaintIsNotDocumentTaint(t *testing.T) {
	tr := New()
	tr.Mark(KindData, "run_sql")
	if tr.Has(KindDocument) {
		t.Fatal("a warehouse read reports document taint — T-H9's gate would fire on every data turn")
	}
	if !tr.Has(KindData) {
		t.Fatal("a warehouse read did not record data taint")
	}
	if !tr.Any() {
		t.Fatal("Any() is false after a data read")
	}
	if got := Join(tr.Kinds()); got != "data" {
		t.Fatalf("Kinds joined = %q, want \"data\"", got)
	}
}

func TestBothKindsAreRecordedTogether(t *testing.T) {
	tr := New()
	tr.Mark(KindData, "run_sql")
	tr.Mark(KindDocument, "kontrak.pdf")
	if !tr.Has(KindDocument) || !tr.Has(KindData) {
		t.Fatal("a turn that read both does not report both")
	}
	// Sorted, so an audit column is comparable between runs.
	if got := Join(tr.Kinds()); got != "data,document" {
		t.Fatalf("Kinds joined = %q, want \"data,document\"", got)
	}
	if got := tr.Sources(KindData); len(got) != 1 || got[0] != "run_sql" {
		t.Fatalf("data sources = %v", got)
	}
	if got := tr.Sources(KindDocument); len(got) != 1 || got[0] != "kontrak.pdf" {
		t.Fatalf("document sources = %v", got)
	}
}

// A read with no nameable source still taints. T-H9's reason builder reads Has
// first and the names second precisely because of this case, so if the flag
// ever stopped being set here the gate would open silently.
func TestAnUnnamedReadTaintsAndNamesNothing(t *testing.T) {
	tr := New()
	tr.Mark(KindDocument, "   ") // whitespace only: a read whose source could not be named
	if !tr.Has(KindDocument) {
		t.Fatal("an unnamed read did not taint")
	}
	if got := tr.Sources(KindDocument); len(got) != 0 {
		t.Fatalf("an unnamed read produced a source name: %v", got)
	}
}

// An empty kind is a caller bug, and it must not become a taint nobody can
// query for.
func TestAnEmptyKindMarksNothing(t *testing.T) {
	tr := New()
	tr.Mark("", "run_sql")
	if tr.Any() {
		t.Fatal("marking an empty kind tainted the turn")
	}
}

func TestSourcesAreDedupedAndSorted(t *testing.T) {
	tr := New()
	for _, s := range []string{"b.pdf", "a.pdf", "b.pdf", ""} {
		tr.Mark(KindDocument, s)
	}
	got := tr.Sources(KindDocument)
	if len(got) != 2 || got[0] != "a.pdf" || got[1] != "b.pdf" {
		t.Fatalf("sources = %v, want [a.pdf b.pdf] — sorted, deduped, and without the unnamed read", got)
	}
}

// Every method is documented as nil-safe so callers do not branch. A nil
// tracker is what FromContext returns on every turn that never installed one —
// which is every eval run and every MCP call.
func TestNilTrackerIsSafeAndUntainted(t *testing.T) {
	var tr *Tracker
	tr.Mark(KindDocument, "a.pdf")
	if tr.Has(KindDocument) || tr.Any() {
		t.Fatal("a nil tracker reports taint")
	}
	if got := tr.Sources(KindDocument); got != nil {
		t.Fatalf("a nil tracker names sources: %v", got)
	}
	if got := tr.Kinds(); got != nil {
		t.Fatalf("a nil tracker names kinds: %v", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if Any(ctx) || Has(ctx, KindDocument) {
		t.Fatal("a bare context reports taint")
	}
	if got := Sources(ctx, KindDocument); len(got) != 0 {
		t.Fatalf("a bare context names sources: %v", got)
	}
	// With(nil) must not wrap: a caller that has no tracker should hand the
	// context through unchanged rather than install a key holding nil.
	if With(ctx, nil) != ctx {
		t.Fatal("With(nil) replaced the context")
	}

	ctx = With(ctx, New())
	Mark(ctx, KindDocument, "a.pdf")
	if !Has(ctx, KindDocument) {
		t.Fatal("Mark through the context did not taint it")
	}
	if got := Sources(ctx, KindDocument); len(got) != 1 || got[0] != "a.pdf" {
		t.Fatalf("Sources through the context = %v", got)
	}
	if got := Join(Kinds(ctx)); got != "document" {
		t.Fatalf("Kinds through the context = %q", got)
	}
}

func TestJoinOfNothingIsEmpty(t *testing.T) {
	if got := Join(nil); got != "" {
		t.Fatalf("Join(nil) = %q, want empty — a turn that read nothing must be distinguishable", got)
	}
}

// A provider can run several tool calls from one iteration and any of them can
// be the one that reads a document — the package comment says so, and this is
// the assertion behind it. Run with -race.
func TestConcurrentMarksAreSafe(t *testing.T) {
	tr := New()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kind := KindDocument
			if i%2 == 0 {
				kind = KindData
			}
			tr.Mark(kind, string(rune('a'+i%4))+".pdf")
			_ = tr.Has(kind)
			_ = tr.Any()
			_ = tr.Sources(kind)
			_ = tr.Kinds()
		}(i)
	}
	wg.Wait()
	// Two names per kind: the kind alternates on i%2 and the name on i%4, so
	// the even iterations write a.pdf/c.pdf as data and the odd ones b.pdf/d.pdf
	// as document.
	if got := len(tr.Sources(KindData)); got != 2 {
		t.Fatalf("data sources = %d, want 2", got)
	}
	if got := len(tr.Sources(KindDocument)); got != 2 {
		t.Fatalf("document sources = %d, want 2", got)
	}
}
