package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/freshness"
	"github.com/fauzanebd/argentum/internal/guardrails"
)

// turnWithResult installs a compute-capable turn, runs one query result through
// the same path run_sql uses, and returns the context plus the payload the model
// would have been handed.
func turnWithResult(t *testing.T, res *db.QueryResult) (context.Context, map[string]interface{}) {
	t.Helper()
	ctx := WithTurnValues(context.Background())
	out := marshalSQLResult(ctx, "src-1", "postgres", res, 0, nil, nil, freshness.Report{})
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("run_sql payload is not JSON: %v", err)
	}
	if payload["result_id"] != "r1" {
		t.Fatalf("a compute-capable turn got result_id %v, want r1", payload["result_id"])
	}
	return ctx, payload
}

func computeCall(t *testing.T, ctx context.Context, args map[string]interface{}) (string, map[string]interface{}, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("bad test arguments: %v", err)
	}
	out, execErr := (&ComputeTool{}).Execute(ctx, string(raw))
	if execErr != nil {
		return string(raw), nil, execErr
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("compute payload is not JSON: %v", err)
	}
	return string(raw), payload, nil
}

func marginTurn(t *testing.T) context.Context {
	t.Helper()
	ctx, _ := turnWithResult(t, &db.QueryResult{
		Columns: []string{"revenue", "cost"},
		Rows:    []map[string]interface{}{{"revenue": "3863405700", "cost": "2100000000"}},
		Count:   1,
	})
	return ctx
}

// The margin case, end to end: two figures run_sql returned, one ratio nobody
// typed.
func TestComputeBindsFiguresThisTurnReturned(t *testing.T) {
	ctx := marginTurn(t)
	_, payload, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "round((revenue - cost) / revenue * 100, 2)",
		"inputs":     map[string]string{"revenue": "r1.revenue", "cost": "r1.cost"},
		"unit":       "percent",
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if got := payload["computed"]; got != "45.64" {
		t.Errorf("computed = %v, want 45.64", got)
	}
	if got := payload["unit"]; got != "percent" {
		t.Errorf("unit = %v, want percent", got)
	}
	// The working names where each figure came from AND what it was, because a
	// reference alone is not checkable once the turn is over.
	working, _ := payload["working"].(map[string]interface{})
	bindings, _ := working["bindings"].([]interface{})
	if len(bindings) != 2 {
		t.Fatalf("working carries %d binding(s), want 2: %v", len(bindings), working)
	}
	got := map[string]string{}
	for _, b := range bindings {
		m := b.(map[string]interface{})
		got[m["name"].(string)] = m["from"].(string) + "=" + m["value"].(string)
	}
	if got["revenue"] != "r1.revenue=3863405700" || got["cost"] != "r1.cost=2100000000" {
		t.Errorf("bindings = %v", got)
	}
}

// The transcription half of grounding. A number the model typed into `inputs`
// is not a number this product returned, and the refusal says what it should
// have written instead.
func TestComputeRefusesANumberTheModelTyped(t *testing.T) {
	ctx := marginTurn(t)
	_, _, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "revenue - cost",
		"inputs":     map[string]string{"revenue": "3863405700", "cost": "r1.cost"},
	})
	if err == nil {
		t.Fatal("compute accepted a literal where a reference was required")
	}
	if !strings.Contains(err.Error(), "r1.revenue") {
		t.Errorf("the refusal does not offer the reference that would work: %v", err)
	}
}

func TestComputeRefusesAnUnboundName(t *testing.T) {
	ctx := marginTurn(t)
	_, _, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "revenue - tax",
		"inputs":     map[string]string{"revenue": "r1.revenue"},
	})
	if err == nil {
		t.Fatal("compute accepted an expression with an unbound name")
	}
	for _, want := range []string{`"tax"`, "r1 (run_sql, 1 row(s)): cost, revenue"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is missing %q: %v", want, err)
		}
	}
}

func TestComputeRefusesAResultFromAnotherTurn(t *testing.T) {
	ctx := marginTurn(t)
	_, _, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "revenue * 2",
		"inputs":     map[string]string{"revenue": "r9.revenue"},
	})
	if err == nil {
		t.Fatal("compute accepted a result id this turn never produced")
	}
	if !strings.Contains(err.Error(), `no result "r9"`) {
		t.Errorf("the refusal does not name the missing result: %v", err)
	}
}

// No number reaches the reply: the tool errors, so there is no payload for the
// grounding collector to read and the audit row records a failure.
func TestComputeDivisionByZeroProducesNoFigure(t *testing.T) {
	ctx, _ := turnWithResult(t, &db.QueryResult{
		Columns: []string{"revenue", "orders"},
		Rows:    []map[string]interface{}{{"revenue": "100", "orders": "0"}},
		Count:   1,
	})
	_, payload, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "revenue / orders",
		"inputs":     map[string]string{"revenue": "r1.revenue", "orders": "r1.orders"},
	})
	if err == nil {
		t.Fatalf("dividing by zero returned a payload: %v", payload)
	}
	if payload != nil {
		t.Errorf("a failed computation still produced a payload: %v", payload)
	}
}

func TestComputeSumsAColumn(t *testing.T) {
	ctx, _ := turnWithResult(t, &db.QueryResult{
		Columns: []string{"month", "amount"},
		Rows: []map[string]interface{}{
			{"month": "2026-01", "amount": "10.05"},
			{"month": "2026-02", "amount": "20.10"},
			{"month": "2026-03", "amount": "30.15"},
		},
		Count: 3,
	})
	_, payload, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "sum(amount) / 3",
		"inputs":     map[string]string{"amount": "r1.amount"},
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if got := payload["computed"]; got != "20.1" {
		t.Errorf("mean of the column = %v, want 20.1", got)
	}
	// A text column is not bindable, and the message says which columns are.
	_, _, err = computeCall(t, ctx, map[string]interface{}{
		"expression": "month * 2",
		"inputs":     map[string]string{"month": "r1.month"},
	})
	if err == nil || !strings.Contains(err.Error(), "no numeric column") {
		t.Errorf("binding a text column = %v, want a refusal naming the numeric ones", err)
	}
}

// A figure compute returned is evidence. The same figure typed into a reply by
// a turn that never called it is not.
func TestAComputedFigureGroundsAReply(t *testing.T) {
	ctx := marginTurn(t)
	_, payload, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "revenue - cost",
		"inputs":     map[string]string{"revenue": "r1.revenue", "cost": "r1.cost"},
		"unit":       "money",
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	raw, _ := json.Marshal(payload)

	if !agentbudget.IsDataTool("compute") {
		t.Fatal("compute is not in the evidence list, so nothing it returns can ground a reply")
	}
	tracker := agentbudget.New(agentbudget.Budget{}.Normalize())
	tracker.Observe("compute", string(raw), nil)
	if got := tracker.Snapshot().DataRows; got != 1 {
		t.Errorf("a computed figure counted as %d row(s) of evidence, want 1", got)
	}

	// And the figure itself is collectable, which is what CheckGrounding
	// matches a stated figure against.
	found := false
	for _, n := range guardrails.CollectNumbers(payload, 20) {
		if n == 1763405700 {
			found = true
		}
	}
	if !found {
		t.Errorf("the computed figure is not in the numbers this result offers as evidence: %v",
			guardrails.CollectNumbers(payload, 20))
	}

	// The counterfactual: a turn that computed nothing offers no evidence, so
	// the same figure in a reply is ungrounded.
	bare := agentbudget.New(agentbudget.Budget{}.Normalize())
	if got := bare.Snapshot().DataRows; got != 0 {
		t.Errorf("a turn that ran nothing has %d rows of evidence, want 0", got)
	}
}

// The working reaches the audit row, with the values the references resolved
// to — not just the references, which nobody can check once the turn is over.
func TestTheWorkingReachesTheAuditRow(t *testing.T) {
	ctx := marginTurn(t)
	args, payload, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "revenue - cost",
		"inputs":     map[string]string{"revenue": "r1.revenue", "cost": "r1.cost"},
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	out, _ := json.Marshal(payload)
	redacted, rows, _ := digestArgs(args, string(out))
	// rows_returned stays nil: a computed figure is not a row, and the column
	// means "this tool returns rows and got none" when it is 0. agentbudget
	// counts the figure as evidence separately, which is a different question.
	if rows != nil {
		t.Errorf("rows_returned = %v, want nil — compute returns a figure, not rows", *rows)
	}
	row := string(redacted)
	for _, want := range []string{
		`"expression":"revenue - cost"`, // what was asked
		`"_working"`,                    // and how it was answered
		`"3863405700"`,
		`"2100000000"`,
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the audit row is missing %s:\n%s", want, row)
		}
	}
}

// The T-F2 arm, applied to this ticket: a turn that cannot compute gets exactly
// the payload it got before compute existed — no result_id, no new field, the
// same bytes.
func TestAPayloadIsUnchangedForATurnThatCannotCompute(t *testing.T) {
	res := func() *db.QueryResult {
		return &db.QueryResult{
			Columns: []string{"revenue"},
			Rows:    []map[string]interface{}{{"revenue": "3863405700"}},
			Count:   1,
		}
	}
	without := marshalSQLResult(context.Background(), "src-1", "postgres", res(), 0, nil, nil, freshness.Report{})
	again := marshalSQLResult(context.Background(), "src-1", "postgres", res(), 0, nil, nil, freshness.Report{})
	if string(without) != string(again) {
		t.Fatalf("two marshals of the same result differ:\n%s\n%s", without, again)
	}
	if strings.Contains(string(without), "result_id") {
		t.Errorf("a turn with no compute memory was given a result_id:\n%s", without)
	}
	with := marshalSQLResult(WithTurnValues(context.Background()), "src-1", "postgres", res(), 0, nil, nil, freshness.Report{})
	if !strings.Contains(string(with), `"result_id":"r1"`) {
		t.Errorf("a compute-capable turn was given no result_id:\n%s", with)
	}
}

// What the model is shown and what it can bind are the same figures. A result
// trimmed to fit the context budget binds the rows that survived, not the ones
// that were dropped.
func TestBindingFollowsTheTrimmedPayload(t *testing.T) {
	rows := make([]map[string]interface{}, 0, 50)
	for i := 0; i < 50; i++ {
		rows = append(rows, map[string]interface{}{"amount": "1"})
	}
	res := &db.QueryResult{Columns: []string{"amount"}, Rows: rows, Count: len(rows)}
	ctx := WithTurnValues(context.Background())
	out := marshalSQLResult(ctx, "src-1", "postgres", res, 400, nil, nil, freshness.Report{})
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	shown := len(payload["rows"].([]interface{}))
	if shown == 0 || shown == 50 {
		t.Fatalf("the byte cap did not trim this result (%d rows shown); the test proves nothing", shown)
	}
	_, computed, err := computeCall(t, ctx, map[string]interface{}{
		"expression": "sum(amount)",
		"inputs":     map[string]string{"amount": "r1.amount"},
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if got := computed["computed"]; got != strconv.Itoa(shown) {
		t.Errorf("sum over the bound column = %v, want %d — the rows the model was shown", got, shown)
	}
}
