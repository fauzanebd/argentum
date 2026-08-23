package agentbudget

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
)

// stubTool returns a scripted sequence of results, and records the arguments
// it was called with so a test can assert a call did *not* reach it.
type stubTool struct {
	name    string
	results []string
	errs    []error
	calls   []string
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub" }
func (s *stubTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{}
}
func (s *stubTool) Run(ctx context.Context, input string) (string, error) {
	return s.Execute(ctx, input)
}
func (s *stubTool) Execute(ctx context.Context, args string) (string, error) {
	i := len(s.calls)
	s.calls = append(s.calls, args)
	var out string
	if i < len(s.results) {
		out = s.results[i]
	}
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	return out, err
}

func turnCtx(t *testing.T, b Budget) (context.Context, *Tracker) {
	t.Helper()
	tr := New(b)
	return WithTracker(context.Background(), tr), tr
}

// This is T-Q11's finding as a regression test: deepseek answered a refusal by
// re-sending the identical call six times until the iteration budget ended the
// turn, and eleven of its fifteen failures on the 2026-08-23 baseline were that
// one loop.
func TestRepeatedIdenticalFailureEndsTheLoop(t *testing.T) {
	const bad = `{"error":"query_metric needs both window bounds"}`
	tool := &stubTool{name: "query_metric", results: []string{bad, bad, bad, bad}}
	ctx, tr := turnCtx(t, Budget{MaxToolCalls: 20, MaxIterations: 20})
	g := Guard(tool)

	args := `{"metric":"revenue","from":"2024-12-01"}`

	// First failure is allowed through untouched: one refusal is information,
	// not a loop.
	out1, err := g.Execute(ctx, args)
	if err != nil || out1 != bad {
		t.Fatalf("first call should pass through: out=%q err=%v", out1, err)
	}
	if tr.Snapshot().Exhausted {
		t.Fatal("one failure must not end the turn")
	}

	// The retry is executed — a transient failure deserves its second chance —
	// and it fails identically, which is the signature this guard exists for.
	out2, err := g.Execute(ctx, args)
	if err != nil {
		t.Fatalf("second call must be a result, not a Go error: %v", err)
	}
	if len(tool.calls) != 2 {
		t.Fatalf("the retry must reach the tool; calls=%d", len(tool.calls))
	}
	if !IsRefusal(out2) {
		t.Errorf("the second identical failure should come back as this package's refusal payload, got %q", out2)
	}
	if !tr.Snapshot().Exhausted {
		t.Error("two identical failures must end the tool loop")
	}

	// A third attempt must not reach the tool at all.
	if _, err := g.Execute(ctx, args); err != nil {
		t.Fatalf("third call: %v", err)
	}
	if len(tool.calls) != 2 {
		t.Errorf("after the loop ended the tool must not be called again; calls=%d", len(tool.calls))
	}
}

// Different arguments are a different question, however many of them fail.
func TestDifferentArgumentsAreNotALoop(t *testing.T) {
	const bad = `{"error":"no rows"}`
	tool := &stubTool{name: "run_sql", results: []string{bad, bad, bad}}
	ctx, tr := turnCtx(t, Budget{MaxToolCalls: 20, MaxIterations: 20})
	g := Guard(tool)

	for i, args := range []string{`{"sql":"select 1"}`, `{"sql":"select 2"}`, `{"sql":"select 3"}`} {
		if _, err := g.Execute(ctx, args); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if tr.Snapshot().Exhausted {
		t.Error("three different failing calls are exploration, not a loop")
	}
	if len(tool.calls) != 3 {
		t.Errorf("every distinct call must reach the tool; calls=%d", len(tool.calls))
	}
}

// The same arguments failing two *different* ways is progress, not a loop:
// the second error says something the first did not.
func TestDifferentFailuresAreNotALoop(t *testing.T) {
	tool := &stubTool{name: "run_sql", results: []string{
		`{"error":"column widget_id does not exist"}`,
		`{"error":"relation widgets does not exist"}`,
	}}
	ctx, tr := turnCtx(t, Budget{MaxToolCalls: 20, MaxIterations: 20})
	g := Guard(tool)

	args := `{"sql":"select widget_id from widgets"}`
	for i := 0; i < 2; i++ {
		if _, err := g.Execute(ctx, args); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if tr.Snapshot().Exhausted {
		t.Error("two different errors are not the same refusal twice")
	}
}

// A repeat that succeeds is the retry working, which is the case that makes
// executing the second call rather than refusing it worth the cost.
func TestSuccessfulRetryDoesNotEndTheLoop(t *testing.T) {
	tool := &stubTool{
		name:    "run_sql",
		results: []string{"", `{"rows":3}`},
		errs:    []error{errors.New("connection reset"), nil},
	}
	ctx, tr := turnCtx(t, Budget{MaxToolCalls: 20, MaxIterations: 20})
	g := Guard(tool)

	args := `{"sql":"select count(*) from fact_sales"}`
	if _, err := g.Execute(ctx, args); err == nil {
		t.Fatal("first call should surface its error")
	}
	out, err := g.Execute(ctx, args)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !strings.Contains(out, "rows") {
		t.Errorf("a successful retry must return the tool's own result, got %q", out)
	}
	if tr.Snapshot().Exhausted {
		t.Error("a retry that worked must not end the turn")
	}
}

// A Go error repeating identically is the same loop wearing the SDK's costume.
func TestRepeatedGoErrorEndsTheLoop(t *testing.T) {
	boom := errors.New("resolve tenant connection: no such source")
	tool := &stubTool{name: "get_schema", errs: []error{boom, boom, boom}}
	ctx, tr := turnCtx(t, Budget{MaxToolCalls: 20, MaxIterations: 20})
	g := Guard(tool)

	args := `{"source_id":"nope"}`
	if _, err := g.Execute(ctx, args); err == nil {
		t.Fatal("first call should surface its error")
	}
	out, err := g.Execute(ctx, args)
	if err != nil {
		t.Fatalf("the second identical failure must be a result the model can act on, got err=%v", err)
	}
	if !IsRefusal(out) {
		t.Errorf("expected the refusal payload, got %q", out)
	}
	if !tr.Snapshot().Exhausted {
		t.Error("two identical Go errors must end the tool loop")
	}
}
