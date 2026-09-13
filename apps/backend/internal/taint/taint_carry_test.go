package taint

import (
	"encoding/json"
	"testing"
)

// T-N5's half of this package: a peer agent's words arrive with what its turn
// had read. These drive Carry and Inherit directly, with no nudge anywhere — a
// trust boundary whose only test is an end-to-end nudge would be a boundary
// tested by the thing it exists to constrain.

// The laundering path, closed. Finance reads a supplier's invoice and writes to
// Ops; Ops read no document, and must gate as if it had.
func TestADocumentCrossesAHandOff(t *testing.T) {
	author := New()
	author.Mark(KindDocument, "invoice-4471.pdf")
	author.Mark(KindData, "run_sql")

	// Across the queue, the way it will actually travel.
	wire, err := json.Marshal(author.Carry())
	if err != nil {
		t.Fatal(err)
	}
	var carried map[Kind][]string
	if err := json.Unmarshal(wire, &carried); err != nil {
		t.Fatal(err)
	}

	recipient := New()
	recipient.Inherit(carried)
	recipient.Mark(KindAgent, "Finance")

	if !recipient.Has(KindDocument) {
		t.Fatal("the recipient of a document-tainted peer is not document-tainted — T-H9 would not gate it")
	}
	if got := recipient.Sources(KindDocument); len(got) != 1 || got[0] != "invoice-4471.pdf" {
		t.Fatalf("inherited document sources = %v, want the author's file", got)
	}
	// What the audit row will say, and the half of "a message from the Finance
	// agent, which had read invoice-4471.pdf" this package owns.
	if got := Join(recipient.Kinds()); got != "agent,data,document" {
		t.Fatalf("Kinds joined = %q, want \"agent,data,document\"", got)
	}
	if got := recipient.Sources(KindAgent); len(got) != 1 || got[0] != "Finance" {
		t.Fatalf("agent sources = %v, want [Finance]", got)
	}
}

// A peer's message is not a document. The gate follows what the author read,
// not the fact that there was an author — every hand-off needing an approval
// would be an off switch.
func TestAPeerMessageAloneIsNotDocumentTaint(t *testing.T) {
	tr := New()
	tr.Inherit(New().Carry()) // an author that read nothing
	tr.Mark(KindAgent, "Finance")
	if tr.Has(KindDocument) {
		t.Fatal("a message from an untainted peer reports document taint — T-H9 would gate every hand-off")
	}
	if got := Join(tr.Kinds()); got != "agent" {
		t.Fatalf("Kinds joined = %q, want \"agent\"", got)
	}
}

// The unnamed read is the case Sources drops and Has does not, and the carrier
// has to side with Has — or a document with no filename crosses as no read.
func TestAnUnnamedReadSurvivesTheCarry(t *testing.T) {
	author := New()
	author.Mark(KindDocument, "")
	carried := author.Carry()
	if len(carried[KindDocument]) != 1 {
		t.Fatalf("carried = %v, want the unnamed document read kept", carried)
	}
	recipient := New()
	recipient.Inherit(carried)
	if !recipient.Has(KindDocument) {
		t.Fatal("an unnamed document read did not survive the hand-off")
	}
}

// Nothing this package writes has this shape. A truncated or hand-written
// payload does, and it fails tainted.
func TestAKindCarriedWithNoSourcesStillTaints(t *testing.T) {
	tr := New()
	tr.Inherit(map[Kind][]string{KindDocument: nil})
	if !tr.Has(KindDocument) {
		t.Fatal("a carried kind with no sources did not taint")
	}
}

// Nil so a payload under `omitempty` is byte-identical when the author read
// nothing — and nil-safe both ways, because FromContext returns nil on every
// turn that installed no tracker.
func TestCarryOfNothingIsNil(t *testing.T) {
	if got := New().Carry(); got != nil {
		t.Fatalf("Carry of a clean tracker = %v, want nil", got)
	}
	var none *Tracker
	if got := none.Carry(); got != nil {
		t.Fatalf("Carry of a nil tracker = %v, want nil", got)
	}
	none.Inherit(map[Kind][]string{KindDocument: {"a.pdf"}}) // must not panic
	New().Inherit(nil)
}

// Copy, not share. What the recipient reads next is its own, and a carried map
// is a snapshot nobody can reach back through.
func TestInheritCopiesRatherThanShares(t *testing.T) {
	author := New()
	author.Mark(KindData, "run_sql")
	recipient := New()
	recipient.Inherit(author.Carry())
	recipient.Mark(KindDocument, "recipients-own.pdf")

	if author.Has(KindDocument) {
		t.Fatal("the recipient's read tainted the author — the trackers are shared, not copied")
	}
	carried := author.Carry()
	carried[KindData][0] = "tampered"
	if got := author.Sources(KindData); len(got) != 1 || got[0] != "run_sql" {
		t.Fatalf("mutating a carried map changed the author's tracker: %v", got)
	}
}
