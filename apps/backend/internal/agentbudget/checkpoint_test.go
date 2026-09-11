package agentbudget

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/llmusage"
)

// withIterations returns a context whose usage collector reports n completed
// iterations, which is how iterationsUsed counts them.
func withIterations(ctx context.Context, n int) context.Context {
	ctx, c := llmusage.WithCollector(ctx)
	for i := 0; i < n; i++ {
		c.Add(llmusage.Usage{InputTokens: 1})
	}
	return ctx
}

func TestCheckpointFiresOnceAtTheRatio(t *testing.T) {
	tool := &fakeTool{name: "run_sql", result: rows(1)}
	guarded := Guard(tool)
	tracker := New(Budget{MaxToolCalls: 10, WarnRatio: 0.7})
	ctx := WithTracker(withIterations(context.Background(), 0), tracker)

	var notices []string
	for i := 0; i < 9; i++ {
		out, err := guarded.Execute(ctx, `{"sql":"select `+string(rune('a'+i))+`"}`)
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if n := checkpointOf(t, out); n != "" {
			notices = append(notices, n)
		}
	}

	if len(notices) != 1 {
		t.Fatalf("got %d notices, want exactly 1: %v", len(notices), notices)
	}
	// 7 of 10 tool calls is the first that reaches 0.7.
	if !strings.Contains(notices[0], "7 of 10 tool calls") {
		t.Errorf("notice does not name the crossing dimension: %q", notices[0])
	}
	// The clause that keeps a warned turn from becoming an abandoned one.
	if !strings.Contains(notices[0], "do NOT stop") {
		t.Errorf("notice is missing the keep-going clause: %q", notices[0])
	}
}

func TestCheckpointNamesTheTightestDimension(t *testing.T) {
	tool := &fakeTool{name: "run_sql", result: rows(1)}
	guarded := Guard(tool)
	// One tool call of twelve, but six iterations of eight: iterations bind.
	tracker := New(Budget{WarnRatio: 0.7})
	ctx := WithTracker(withIterations(context.Background(), 6), tracker)

	out, err := guarded.Execute(ctx, `{"sql":"select 1"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if n := checkpointOf(t, out); !strings.Contains(n, "6 of 8 tool-calling iterations") {
		t.Errorf("notice = %q, want the iteration dimension", n)
	}
}

// The notice may not hand the model a number the grounding check would then
// accept as a retrieved figure — see tightestLocked's comment.
func TestCheckpointStatesNoFigureThatCouldGroundAFabrication(t *testing.T) {
	tool := &fakeTool{name: "run_sql", result: rows(1)}
	guarded := Guard(tool)
	tracker := New(Budget{MaxTokens: 1000, WarnRatio: 0.7})
	ctx, collector := llmusage.WithCollector(context.Background())
	collector.Add(llmusage.Usage{InputTokens: 900})
	ctx = WithTracker(ctx, tracker)

	notice := checkpointOf(t, mustExecute(t, guarded, ctx, `{"sql":"select 1"}`))
	if notice == "" {
		t.Fatal("no notice on a turn 90% through its token budget")
	}
	if !strings.Contains(notice, "most of this turn's token budget") {
		t.Errorf("token dimension should be described without figures: %q", notice)
	}
	for _, digits := range []string{"900", "1000"} {
		if strings.Contains(notice, digits) {
			t.Errorf("notice states %s, which CollectNumbersInProse would treat as evidence: %q", digits, notice)
		}
	}
}

func TestCheckpointSilentBelowTheRatioAndWhenDisabled(t *testing.T) {
	tool := &fakeTool{name: "run_sql", result: rows(1)}

	below := WithTracker(withIterations(context.Background(), 0), New(Budget{MaxToolCalls: 10, WarnRatio: 0.7}))
	if n := checkpointOf(t, mustExecute(t, Guard(tool), below, `{"sql":"select 1"}`)); n != "" {
		t.Errorf("notice at 1 of 10 tool calls: %q", n)
	}

	// WarnRatio >= 1 is the documented way to switch the notice off; a ratio
	// that cannot be crossed has to stay uncrossed even at the ceiling.
	off := WithTracker(withIterations(context.Background(), 7), New(Budget{WarnRatio: 1}))
	if n := checkpointOf(t, mustExecute(t, Guard(tool), off, `{"sql":"select 1"}`)); n != "" {
		t.Errorf("notice with the checkpoint disabled: %q", n)
	}
}

// An exhausted turn gets the refusal, which says everything the notice would
// and more. The two must not arrive together.
func TestCheckpointSilentOnAnExhaustedTurn(t *testing.T) {
	tracker := New(Budget{MaxToolCalls: 10, WarnRatio: 0.7})
	tracker.Exhaust("test")
	if n := tracker.Checkpoint(withIterations(context.Background(), 7)); n != "" {
		t.Errorf("notice on an exhausted turn: %q", n)
	}
}

// A failed call carries no result for the notice to ride on, and the notice
// has to survive to the next call that works.
func TestCheckpointWaitsForACallThatSucceeds(t *testing.T) {
	working := Guard(&fakeTool{name: "run_sql", result: rows(1)})
	failing := Guard(&fakeTool{name: "run_sql", err: context.DeadlineExceeded})
	// Four tool calls, so the third crosses 0.7 — and the third one fails.
	ctx := WithTracker(withIterations(context.Background(), 0), New(Budget{MaxToolCalls: 4, WarnRatio: 0.7}))

	for i, args := range []string{`{"sql":"select 1"}`, `{"sql":"select 2"}`} {
		if n := checkpointOf(t, mustExecute(t, working, ctx, args)); n != "" {
			t.Fatalf("notice at call %d of 4: %q", i+1, n)
		}
	}
	if _, err := failing.Execute(ctx, `{"sql":"select 3"}`); err == nil {
		t.Fatal("expected the tool's error to propagate")
	}
	if n := checkpointOf(t, mustExecute(t, working, ctx, `{"sql":"select 4"}`)); n == "" {
		t.Error("notice was consumed by the failed call")
	}
}

func TestWithCheckpointPreservesTheToolsOwnBytes(t *testing.T) {
	// The number is the point: a round trip through map[string]any renders
	// this as 1e+06 and the order id is corrupted by its own budget warning.
	result := `{"row_count":1,"rows":[{"order_id":1000000,"total":"3863405700.00"}]}`
	out := WithCheckpoint(result, "notice")

	if !strings.Contains(out, `"order_id":1000000`) || !strings.Contains(out, `"total":"3863405700.00"`) {
		t.Fatalf("tool output was rewritten: %s", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("spliced result is not valid JSON: %v (%s)", err, out)
	}
	if parsed[checkpointField] != "notice" {
		t.Errorf("notice missing from %s", out)
	}
}

func TestWithCheckpointHandlesTheOtherResultShapes(t *testing.T) {
	if got := WithCheckpoint("{}", "notice"); got != `{"budget_checkpoint":"notice"}` {
		t.Errorf("empty object: %s", got)
	}
	if got := WithCheckpoint("no rows found", "notice"); !strings.HasSuffix(got, "\n\nnotice") {
		t.Errorf("bare string: %q", got)
	}
	if got := WithCheckpoint(`{"a":1}`, ""); got != `{"a":1}` {
		t.Errorf("empty notice must not touch the result: %s", got)
	}
}

// The wall clock is a dimension like any other, and the one most likely to
// bind first at production latencies.
func TestCheckpointFiresOnTheWallClock(t *testing.T) {
	tracker := New(Budget{Wall: 100 * time.Second, WarnRatio: 0.7})
	// Backdated rather than slept through: the dimension is measured in whole
	// seconds, so a test that waits for it honestly waits seventy of them.
	tracker.start = time.Now().Add(-80 * time.Second)

	n := tracker.Checkpoint(withIterations(context.Background(), 0))
	if !strings.Contains(n, "80 of this turn's 100 seconds") {
		t.Errorf("notice = %q, want the wall-clock dimension", n)
	}
}

func mustExecute(t *testing.T, tool interface {
	Execute(context.Context, string) (string, error)
}, ctx context.Context, args string) string {
	t.Helper()
	out, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out
}

// checkpointOf returns the notice a result carries, or "".
func checkpointOf(t *testing.T, result string) string {
	t.Helper()
	var parsed struct {
		Checkpoint string `json:"budget_checkpoint"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v (%s)", err, result)
	}
	return parsed.Checkpoint
}
