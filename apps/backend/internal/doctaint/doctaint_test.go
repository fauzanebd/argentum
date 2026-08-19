package doctaint

import (
	"context"
	"sync"
	"testing"
)

// This file exists because T-H9 made this package load-bearing and it had no
// test file at all — the same shape §1k recorded in internal/docchunk, where a
// regex that could never match survived review for exactly that reason. Every
// property T-H9's gate depends on is pinned here.

func TestATrackerStartsUntainted(t *testing.T) {
	if New().Tainted() {
		t.Fatal("a fresh tracker reports taint")
	}
	if got := New().Sources(); len(got) != 0 {
		t.Fatalf("a fresh tracker names sources: %v", got)
	}
}

// The distinction T-H9's gate turns on: a turn where search_documents ran and
// matched nothing is NOT tainted, and a turn where it matched IS.
func TestMarkIsWhatTaints(t *testing.T) {
	tr := New()
	if tr.Tainted() {
		t.Fatal("untainted precondition failed")
	}
	tr.Mark("09-scan-invoice.pdf")
	if !tr.Tainted() {
		t.Fatal("a marked tracker does not report taint")
	}
	if got := tr.Sources(); len(got) != 1 || got[0] != "09-scan-invoice.pdf" {
		t.Fatalf("sources = %v", got)
	}
}

// A read with no nameable source still taints. T-H9's reason builder reads
// Tainted first and the names second precisely because of this case, so if the
// flag ever stopped being set here the gate would open silently.
func TestAnUnnamedReadTaintsAndNamesNothing(t *testing.T) {
	tr := New()
	tr.Mark("   ") // whitespace only: a read whose source could not be named
	if !tr.Tainted() {
		t.Fatal("an unnamed read did not taint")
	}
	if got := tr.Sources(); len(got) != 0 {
		t.Fatalf("an unnamed read produced a source name: %v", got)
	}
}

func TestSourcesAreDedupedAndSorted(t *testing.T) {
	tr := New()
	for _, s := range []string{"b.pdf", "a.pdf", "b.pdf", ""} {
		tr.Mark(s)
	}
	got := tr.Sources()
	if len(got) != 2 || got[0] != "a.pdf" || got[1] != "b.pdf" {
		t.Fatalf("sources = %v, want [a.pdf b.pdf] — sorted, deduped, and without the unnamed read", got)
	}
}

// Every method is documented as nil-safe so callers do not branch. A nil
// tracker is what FromContext returns on every turn that never installed one —
// which is every eval run and every MCP call.
func TestNilTrackerIsSafeAndUntainted(t *testing.T) {
	var tr *Tracker
	tr.Mark("a.pdf")
	if tr.Tainted() {
		t.Fatal("a nil tracker reports taint")
	}
	if got := tr.Sources(); got != nil {
		t.Fatalf("a nil tracker names sources: %v", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if Tainted(ctx) {
		t.Fatal("a bare context reports taint")
	}
	if got := Sources(ctx); len(got) != 0 {
		t.Fatalf("a bare context names sources: %v", got)
	}
	// With(nil) must not wrap: a caller that has no tracker should hand the
	// context through unchanged rather than install a key holding nil.
	if With(ctx, nil) != ctx {
		t.Fatal("With(nil) replaced the context")
	}

	ctx = With(ctx, New())
	Mark(ctx, "a.pdf")
	if !Tainted(ctx) {
		t.Fatal("Mark through the context did not taint it")
	}
	if got := Sources(ctx); len(got) != 1 || got[0] != "a.pdf" {
		t.Fatalf("Sources through the context = %v", got)
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
			tr.Mark(string(rune('a'+i%4)) + ".pdf")
			_ = tr.Tainted()
			_ = tr.Sources()
		}(i)
	}
	wg.Wait()
	if got := tr.Sources(); len(got) != 4 {
		t.Fatalf("sources = %v, want 4 distinct", got)
	}
}
