package agentbudget

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The failure this exists for, restated as an arithmetic check: the run that
// produced no document used six tool calls across eight iterations and was
// still exploring. A headroom of one would have moved where it ran out, not
// whether it ran out.
func TestForDocumentLeavesRoomToWriteTheFile(t *testing.T) {
	base := Default()
	got := base.ForDocument()

	if got.MaxIterations <= base.MaxIterations {
		t.Errorf("MaxIterations = %d, want more than the chat budget's %d", got.MaxIterations, base.MaxIterations)
	}
	if got.MaxToolCalls <= base.MaxToolCalls {
		t.Errorf("MaxToolCalls = %d, want more than the chat budget's %d", got.MaxToolCalls, base.MaxToolCalls)
	}
	// The observed failure: 8 iterations, 6 tool calls, still mid-exploration.
	// The document budget has to clear both with room for the generate call.
	if got.MaxIterations < base.MaxIterations+2 || got.MaxToolCalls < base.MaxToolCalls+2 {
		t.Errorf("headroom is %d iterations / %d tool calls; one more of each only moves where the turn runs out",
			got.MaxIterations-base.MaxIterations, got.MaxToolCalls-base.MaxToolCalls)
	}

	// Tokens and wall clock are untouched: neither was the binding constraint,
	// and a document turn that runs away still has to stop.
	if got.MaxTokens != base.MaxTokens || got.Wall != base.Wall {
		t.Errorf("ForDocument widened tokens or wall clock: %+v", got)
	}
}

// A tenant who tuned their budget down still has it respected — this raises
// what they set rather than replacing it with ours.
func TestForDocumentRaisesTheConfiguredBudgetRatherThanReplacingIt(t *testing.T) {
	tight := Budget{MaxIterations: 3, MaxToolCalls: 4, MaxTokens: 50_000, Wall: 30_000_000_000}
	got := tight.ForDocument()

	if got.MaxIterations != tight.MaxIterations+documentHeadroomIterations {
		t.Errorf("MaxIterations = %d, want %d", got.MaxIterations, tight.MaxIterations+documentHeadroomIterations)
	}
	if got.MaxToolCalls != tight.MaxToolCalls+documentHeadroomToolCalls {
		t.Errorf("MaxToolCalls = %d, want %d", got.MaxToolCalls, tight.MaxToolCalls+documentHeadroomToolCalls)
	}
	if got.MaxTokens != tight.MaxTokens {
		t.Errorf("MaxTokens = %d, want the tenant's %d", got.MaxTokens, tight.MaxTokens)
	}
}

// A half-filled budget normalises first, so a zero dimension gets the default
// plus headroom rather than the headroom alone.
func TestForDocumentNormalizesBeforeAdding(t *testing.T) {
	got := Budget{}.ForDocument()
	want := Default()
	if got.MaxIterations != want.MaxIterations+documentHeadroomIterations {
		t.Errorf("MaxIterations = %d, want %d", got.MaxIterations, want.MaxIterations+documentHeadroomIterations)
	}
}

// --- the reserved deliverable call -------------------------------------
//
// The failure these exist for, in the user's words: "I attempted to generate
// the PDF report, but the turn's tool budget was exhausted before the document
// could be produced." That was a chat turn, which never sees ForDocument above
// — its deliverable was refused by the guard that was meant to keep it honest.

// spend runs n data-tool calls on ctx's tracker, which is how a turn gets to
// the point these tests are about.
func spend(t *testing.T, ctx context.Context, n int) {
	t.Helper()
	tool := Guard(&fakeTool{name: "run_sql", result: rows(1)})
	for i := 0; i < n; i++ {
		if _, err := tool.Execute(ctx, "{}"); err != nil {
			t.Fatalf("setup call %d: %v", i, err)
		}
	}
}

func TestExhaustedTurnStillWritesTheFile(t *testing.T) {
	tr := New(Budget{MaxToolCalls: 2})
	ctx := WithTracker(context.Background(), tr)
	spend(t, ctx, 2)

	// Everything else is refused, including another query.
	query := &fakeTool{name: "run_sql", result: rows(1)}
	if out, _ := Guard(query).Execute(ctx, "{}"); !strings.Contains(out, "budget_exhausted") {
		t.Fatalf("run_sql past the budget = %q, want a refusal", out)
	}
	if query.calls != 0 {
		t.Errorf("run_sql ran %d times past the budget, want 0 — the reserve is for the file alone", query.calls)
	}

	doc := &fakeTool{name: "generate_document", result: `{"url":"https://example/report.pdf"}`}
	out, err := Guard(doc).Execute(ctx, "{}")
	if err != nil {
		t.Fatalf("reserved call errored: %v", err)
	}
	if doc.calls != 1 {
		t.Fatalf("generate_document ran %d times, want 1 — the turn owed a file and had one call held back", doc.calls)
	}
	if !strings.Contains(out, "report.pdf") {
		t.Errorf("reserved call returned %q, want the tool's own output", out)
	}
	if snap := tr.Snapshot(); snap.DeliverableCalls != 1 {
		t.Errorf("DeliverableCalls = %d, want 1", snap.DeliverableCalls)
	}
}

// One call, not a second budget. A model that keeps calling generate_document
// after the file exists is refused like anything else.
func TestReserveIsSpentOnce(t *testing.T) {
	tr := New(Budget{MaxToolCalls: 1})
	ctx := WithTracker(context.Background(), tr)
	spend(t, ctx, 1)

	doc := &fakeTool{name: "generate_document", result: "{}"}
	guarded := Guard(doc)
	_, _ = guarded.Execute(ctx, "{}") // the reserve
	out, _ := guarded.Execute(ctx, "{}")

	if doc.calls != 1 {
		t.Errorf("generate_document ran %d times, want 1", doc.calls)
	}
	if !strings.Contains(out, "budget_exhausted") {
		t.Errorf("second document call = %q, want a refusal", out)
	}
}

// A file written while the budget still held leaves nothing in reserve: the
// deliverable exists, and a second one is a runaway, not a rescue.
func TestReserveIsGoneOnceTheFileIsWritten(t *testing.T) {
	tr := New(Budget{MaxToolCalls: 2})
	ctx := WithTracker(context.Background(), tr)

	doc := &fakeTool{name: "generate_document", result: "{}"}
	guarded := Guard(doc)
	_, _ = guarded.Execute(ctx, "{}") // within budget
	spend(t, ctx, 1)                  // spends the rest

	out, _ := guarded.Execute(ctx, "{}")
	if doc.calls != 1 {
		t.Errorf("generate_document ran %d times, want 1", doc.calls)
	}
	if !strings.Contains(out, "budget_exhausted") {
		t.Errorf("document call after the file was written = %q, want a refusal", out)
	}
}

// The reserve is not a licence to run past the turn's deadline. A render
// started on a dead context fails anyway; refusing it keeps the honest
// incomplete answer instead of an error the user has to read.
func TestReserveDoesNotOutliveTheTurnsContext(t *testing.T) {
	tr := New(Budget{MaxToolCalls: 1})
	ctx, cancel := context.WithCancel(WithTracker(context.Background(), tr))
	spend(t, ctx, 1)
	cancel()

	doc := &fakeTool{name: "generate_document", result: "{}"}
	out, _ := Guard(doc).Execute(ctx, "{}")
	if doc.calls != 0 {
		t.Errorf("generate_document ran %d times on a cancelled turn, want 0", doc.calls)
	}
	if !strings.Contains(out, "budget_exhausted") {
		t.Errorf("refusal = %q, want the ordinary exhaustion payload", out)
	}
}

// The instruction has to say the reserve exists, or the model does what it is
// told and stops — a file unwritten for a new reason is still a file unwritten.
func TestRefusalTellsTheModelTheFileCallRemains(t *testing.T) {
	tr := New(Budget{MaxToolCalls: 1})
	ctx := WithTracker(context.Background(), tr)
	spend(t, ctx, 1)

	read := func() (instruction string, remaining bool) {
		t.Helper()
		out, _ := Guard(&fakeTool{name: "run_sql", result: rows(1)}).Execute(ctx, "{}")
		var payload struct {
			Instruction string `json:"instruction"`
			Remaining   bool   `json:"document_call_remaining"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("refusal is not JSON: %v (%q)", err, out)
		}
		return payload.Instruction, payload.Remaining
	}

	instruction, remaining := read()
	if !remaining {
		t.Error("document_call_remaining = false while the reserve is unspent")
	}
	if instruction != DeliverableInstruction {
		t.Errorf("instruction = %q, want the one that permits the document call", instruction)
	}
	if !strings.Contains(instruction, "generate_document") {
		t.Errorf("instruction never names the tool it is permitting: %q", instruction)
	}

	_, _ = Guard(&fakeTool{name: "generate_document", result: "{}"}).Execute(ctx, "{}")

	instruction, remaining = read()
	if remaining {
		t.Error("document_call_remaining = true after the reserve was spent")
	}
	if instruction != FinalInstruction {
		t.Errorf("instruction = %q, want the plain final-answer instruction", instruction)
	}
}

// Both instructions carry the clause the package exists for. A reserved call
// that lets the model invent the numbers it puts in the file would trade one
// failure for a worse one.
func TestBothInstructionsForbidUnretrievedFigures(t *testing.T) {
	for name, instruction := range map[string]string{
		"final":       FinalInstruction,
		"deliverable": DeliverableInstruction,
	} {
		if !strings.Contains(instruction, "did not come from a tool result in this turn") {
			t.Errorf("%s instruction has no anti-fabrication clause: %q", name, instruction)
		}
	}
	if !strings.Contains(DeliverableInstruction, "invent no figures") {
		t.Error("the deliverable instruction does not carry the clause into the spec it permits")
	}
}
