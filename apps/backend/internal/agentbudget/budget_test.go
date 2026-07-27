package agentbudget

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
)

// --- fake tool ---------------------------------------------------------

type fakeTool struct {
	name   string
	result string
	err    error
	calls  int
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return "fake" }
func (f *fakeTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{}
}
func (f *fakeTool) Run(ctx context.Context, input string) (string, error) {
	return f.Execute(ctx, input)
}
func (f *fakeTool) Execute(context.Context, string) (string, error) {
	f.calls++
	return f.result, f.err
}

func rows(n int) string {
	out, _ := json.Marshal(map[string]interface{}{"row_count": n, "rows": []string{}})
	return string(out)
}

// --- tests -------------------------------------------------------------

func TestNormalizeFillsDisabledDimensions(t *testing.T) {
	got := Budget{MaxToolCalls: 3}.Normalize()
	d := Default()
	if got.MaxToolCalls != 3 {
		t.Errorf("MaxToolCalls = %d, want 3 (explicit value must survive)", got.MaxToolCalls)
	}
	if got.MaxIterations != d.MaxIterations || got.MaxTokens != d.MaxTokens || got.Wall != d.Wall {
		t.Errorf("unset dimensions not defaulted: %+v", got)
	}
}

func TestGuardStopsAtToolCallBudget(t *testing.T) {
	tool := &fakeTool{name: "run_sql", result: rows(1)}
	guarded := Guard(tool)
	ctx := WithTracker(context.Background(), New(Budget{MaxToolCalls: 2}))

	for i := 0; i < 2; i++ {
		if _, err := guarded.Execute(ctx, "{}"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	out, err := guarded.Execute(ctx, "{}")
	if err != nil {
		t.Fatalf("blocked call returned an error: %v — the model never sees errors, "+
			"so the refusal must come back as a normal tool result", err)
	}
	if tool.calls != 2 {
		t.Errorf("tool executed %d times, want 2 — the third call must not reach it", tool.calls)
	}

	var payload struct {
		BudgetExhausted bool   `json:"budget_exhausted"`
		Reason          string `json:"reason"`
		Instruction     string `json:"instruction"`
		Retrieved       string `json:"retrieved_so_far"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("refusal is not JSON: %v (%q)", err, out)
	}
	if !payload.BudgetExhausted {
		t.Error("refusal does not announce the exhausted budget")
	}
	if !strings.Contains(payload.Reason, "tool-call budget") {
		t.Errorf("reason = %q, want it to name the tool-call dimension", payload.Reason)
	}
	if payload.Instruction != FinalInstruction {
		t.Error("refusal does not carry the final-turn instruction")
	}
	if !strings.Contains(payload.Retrieved, "run_sql") {
		t.Errorf("retrieved_so_far = %q, want it to list what did run", payload.Retrieved)
	}
}

func TestGuardStopsAtWallClock(t *testing.T) {
	tool := &fakeTool{name: "get_schema", result: "{}"}
	guarded := Guard(tool)
	ctx := WithTracker(context.Background(), New(Budget{Wall: time.Nanosecond}))

	time.Sleep(time.Millisecond)
	out, err := guarded.Execute(ctx, "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.calls != 0 {
		t.Errorf("tool ran %d times past the wall clock, want 0", tool.calls)
	}
	if !strings.Contains(out, "time budget") {
		t.Errorf("refusal = %q, want it to name the time dimension", out)
	}
}

// A turn that has run out stays out. Re-deciding per call would let a model
// that ignores the instruction slip one more tool through whenever a
// dimension happens to read under its ceiling.
func TestExhaustionIsSticky(t *testing.T) {
	tool := &fakeTool{name: "run_sql", result: rows(1)}
	guarded := Guard(tool)
	tr := New(Budget{MaxToolCalls: 1})
	ctx := WithTracker(context.Background(), tr)

	_, _ = guarded.Execute(ctx, "{}") // spends the budget
	_, _ = guarded.Execute(ctx, "{}") // trips it
	out, _ := guarded.Execute(ctx, "{}")

	if tool.calls != 1 {
		t.Errorf("tool ran %d times, want 1", tool.calls)
	}
	if !strings.Contains(out, "budget_exhausted") {
		t.Errorf("third call = %q, want a refusal", out)
	}
	if snap := tr.Snapshot(); !snap.Exhausted {
		t.Error("tracker reports the turn as not exhausted")
	}
}

func TestObserveRecordsEvidence(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		result    string
		err       error
		wantRows  int
		wantEmpty int
		wantErrs  int
	}{
		{name: "rows returned", tool: "run_sql", result: rows(7), wantRows: 7},
		{name: "zero rows", tool: "run_sql", result: rows(0), wantEmpty: 1},
		{name: "tool error", tool: "run_sql", err: errors.New("boom"), wantErrs: 1},
		{name: "schema is not evidence", tool: "get_schema", result: rows(9)},
		// An unparseable result is not proof of zero rows. Counting it as one
		// would block honest replies whenever a tool changes its output shape.
		{name: "non-JSON result", tool: "run_sql", result: "not json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &fakeTool{name: tt.tool, result: tt.result, err: tt.err}
			ctx := WithTracker(context.Background(), New(Default()))
			tr := FromContext(ctx)
			_, _ = Guard(tool).Execute(ctx, "{}")

			snap := tr.Snapshot()
			if snap.DataRows != tt.wantRows {
				t.Errorf("DataRows = %d, want %d", snap.DataRows, tt.wantRows)
			}
			if snap.EmptyResults != tt.wantEmpty {
				t.Errorf("EmptyResults = %d, want %d", snap.EmptyResults, tt.wantEmpty)
			}
			if snap.ToolErrors != tt.wantErrs {
				t.Errorf("ToolErrors = %d, want %d", snap.ToolErrors, tt.wantErrs)
			}
		})
	}
}

// Tools called outside a chat turn (the connection describer, reindex) get no
// tracker. They must still run.
func TestGuardWithoutTrackerIsTransparent(t *testing.T) {
	tool := &fakeTool{name: "run_sql", result: rows(3)}
	out, err := Guard(tool).Execute(context.Background(), "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.calls != 1 || out != rows(3) {
		t.Errorf("guard interfered without a tracker: calls=%d out=%q", tool.calls, out)
	}
}

func TestRetrievedSummaryDistinguishesEmptyFromUnqueried(t *testing.T) {
	empty := New(Budget{MaxToolCalls: 1})
	_, _ = Guard(&fakeTool{name: "run_sql", result: rows(0)}).
		Execute(WithTracker(context.Background(), empty), "{}")
	if got := empty.Snapshot(); got.EmptyResults != 1 {
		t.Fatalf("setup: EmptyResults = %d", got.EmptyResults)
	}
	empty.mu.Lock()
	msg := empty.retrievedLocked()
	empty.mu.Unlock()
	if !strings.Contains(msg, "zero rows") {
		t.Errorf("summary = %q, want it to say the query matched nothing", msg)
	}

	unqueried := New(Budget{MaxToolCalls: 1})
	_, _ = Guard(&fakeTool{name: "get_schema", result: "{}"}).
		Execute(WithTracker(context.Background(), unqueried), "{}")
	unqueried.mu.Lock()
	msg = unqueried.retrievedLocked()
	unqueried.mu.Unlock()
	if !strings.Contains(msg, "no query was run") {
		t.Errorf("summary = %q, want it to say no query ran", msg)
	}
}
