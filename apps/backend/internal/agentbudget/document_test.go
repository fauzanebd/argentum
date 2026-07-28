package agentbudget

import "testing"

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
