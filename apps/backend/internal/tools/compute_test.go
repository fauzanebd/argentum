package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/freshness"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/tenantctx"
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

// ---------------------------------------------------------------- T-W2

type fakeCompany struct {
	co  *domain.Company
	err error
}

func (f fakeCompany) GetByID(context.Context, string) (*domain.Company, error) {
	return f.co, f.err
}

type fakeProfile struct{ month int }

func (f fakeProfile) GetByCompany(context.Context, string) (*domain.CompanyProfile, error) {
	return &domain.CompanyProfile{FiscalYearStartMonth: f.month}, nil
}

// moneyTurn is a turn holding one result and one tenant's conventions.
func moneyTurn(t *testing.T, code string, rounding domain.RoundingMode, fyStart int) (context.Context, *ComputeTool) {
	t.Helper()
	ctx := tenantctx.WithCompanyID(WithTurnValues(context.Background()), "co-1")
	marshalSQLResult(ctx, "src-1", "postgres", &db.QueryResult{
		Columns: []string{"gross", "units", "rate"},
		Rows:    []map[string]interface{}{{"gross": "1234567.894", "units": "3", "rate": "0.5"}},
		Count:   1,
	}, 0, nil, nil, freshness.Report{})
	tool := NewComputeTool().
		WithConventions(
			fakeCompany{co: &domain.Company{DefaultCurrency: code, CurrencyRounding: rounding}},
			fakeProfile{month: fyStart},
		).
		WithClock(func() time.Time { return time.Date(2026, time.May, 15, 0, 0, 0, 0, time.UTC) })
	return ctx, tool
}

func run(t *testing.T, tool *ComputeTool, ctx context.Context, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := tool.Execute(ctx, string(raw))
	if err != nil {
		t.Fatalf("compute(%v): %v", args, err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return payload
}

// An IDR money result carries no decimal places; a USD one carries two.
func TestMoneyIsQuantisedToItsCurrency(t *testing.T) {
	args := map[string]interface{}{
		"expression": "gross",
		"inputs":     map[string]string{"gross": "r1.gross"},
		"unit":       "money",
	}

	ctx, idr := moneyTurn(t, "IDR", "", 1)
	if got := run(t, idr, ctx, args)["computed"]; got != "1234568" {
		t.Errorf("a rupiah figure = %v, want 1234568 — rupiah has no decimal places", got)
	}

	ctx, usd := moneyTurn(t, "USD", "", 1)
	payload := run(t, usd, ctx, args)
	if got := payload["computed"]; got != "1234567.89" {
		t.Errorf("a dollar figure = %v, want 1234567.89", got)
	}
	if got := payload["currency"]; got != "USD" {
		t.Errorf("the payload does not name the currency it rounded to: %v", got)
	}
	// The pre-rounding figure is on the working, because "why is this one
	// rupiah off the invoice" is exactly what a quantised figure gets asked.
	working, _ := payload["working"].(map[string]interface{})
	if got := working["before_rounding"]; got != "1234567.894" {
		t.Errorf("the working does not record what was rounded: %v", working)
	}
}

// A ratio is not quantised to the currency's scale. Under rupiah's zero places
// it would be destroyed outright — which is why `unit` is on the call.
func TestARatioIsNotQuantised(t *testing.T) {
	ctx, idr := moneyTurn(t, "IDR", "", 1)
	for _, unit := range []string{"ratio", "percent", "count", ""} {
		args := map[string]interface{}{
			"expression": "rate",
			"inputs":     map[string]string{"rate": "r1.rate"},
		}
		if unit != "" {
			args["unit"] = unit
		}
		if got := run(t, idr, ctx, args)["computed"]; got != "0.5" {
			t.Errorf("unit %q was quantised to %v; only money may be", unit, got)
		}
	}
	// And the counterfactual, so the test above cannot pass because nothing
	// quantises at all.
	args := map[string]interface{}{
		"expression": "rate",
		"inputs":     map[string]string{"rate": "r1.rate"},
		"unit":       "money",
	}
	if got := run(t, idr, ctx, args)["computed"]; got != "1" {
		t.Errorf("the same figure marked money = %v, want 1 — the instrument is not firing", got)
	}
}

func TestRoundingConventionIsTheTenants(t *testing.T) {
	// 2.5 at zero places: half-up gives 3, half-even gives 2.
	res := &db.QueryResult{
		Columns: []string{"half"},
		Rows:    []map[string]interface{}{{"half": "2.5"}},
		Count:   1,
	}
	for _, tc := range []struct {
		mode domain.RoundingMode
		want string
	}{
		{domain.RoundingHalfUp, "3"},
		{domain.RoundingHalfEven, "2"},
		{"", "3"}, // unstated resolves to half-up, which is what every answer already did
	} {
		ctx := tenantctx.WithCompanyID(WithTurnValues(context.Background()), "co-1")
		marshalSQLResult(ctx, "src-1", "postgres", res, 0, nil, nil, freshness.Report{})
		tool := NewComputeTool().WithConventions(
			fakeCompany{co: &domain.Company{DefaultCurrency: "IDR", CurrencyRounding: tc.mode}},
			fakeProfile{month: 1},
		)
		got := run(t, tool, ctx, map[string]interface{}{
			"expression": "half",
			"inputs":     map[string]string{"half": "r1.half"},
			"unit":       "money",
		})["computed"]
		if got != tc.want {
			t.Errorf("rounding %q turned 2.5 into %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// A company with no currency behaves exactly as it did before T-W2: nothing is
// quantised and nothing is added to the payload.
func TestNoCurrencyQuantisesNothing(t *testing.T) {
	for _, name := range []string{"unset", "unknown", "no lookup"} {
		ctx := tenantctx.WithCompanyID(WithTurnValues(context.Background()), "co-1")
		marshalSQLResult(ctx, "src-1", "postgres", &db.QueryResult{
			Columns: []string{"gross"},
			Rows:    []map[string]interface{}{{"gross": "1234567.894"}},
			Count:   1,
		}, 0, nil, nil, freshness.Report{})
		tool := NewComputeTool()
		switch name {
		case "unset":
			tool = tool.WithConventions(fakeCompany{co: &domain.Company{}}, nil)
		case "unknown":
			tool = tool.WithConventions(fakeCompany{co: &domain.Company{DefaultCurrency: "XYZ"}}, nil)
		}
		payload := run(t, tool, ctx, map[string]interface{}{
			"expression": "gross",
			"inputs":     map[string]string{"gross": "r1.gross"},
			"unit":       "money",
		})
		if got := payload["computed"]; got != "1234567.894" {
			t.Errorf("%s: a money figure was quantised to %v with no currency to quantise it to", name, got)
		}
		if _, ok := payload["currency"]; ok {
			t.Errorf("%s: the payload claims a currency it does not have", name)
		}
		working, _ := payload["working"].(map[string]interface{})
		if _, ok := working["before_rounding"]; ok {
			t.Errorf("%s: the working records a rounding that never happened", name)
		}
	}
}

// The period namespace: a run rate divides by a day count that belongs to the
// tenant's fiscal calendar, not to the model's arithmetic.
func TestPeriodDaysAreBindable(t *testing.T) {
	// 2026-05-15. On a calendar year, last quarter is Jan-Mar = 90 days. On a
	// May fiscal year it is Feb-Apr = 89 days, and that difference is the whole
	// point of resolving it here.
	for _, tc := range []struct {
		fyStart int
		days    string
	}{{1, "90"}, {5, "89"}} {
		ctx, tool := moneyTurn(t, "IDR", "", tc.fyStart)
		got := run(t, tool, ctx, map[string]interface{}{
			"expression": "days",
			"inputs":     map[string]string{"days": "period.last_quarter.days"},
			"unit":       "count",
		})["computed"]
		if got != tc.days {
			t.Errorf("last quarter under a fiscal year starting month %d = %v days, want %s", tc.fyStart, got, tc.days)
		}
	}

	// And the thing it is for: a per-day figure neither half of which the
	// model supplied.
	ctx, tool := moneyTurn(t, "USD", "", 1)
	got := run(t, tool, ctx, map[string]interface{}{
		"expression": "gross / days",
		"inputs":     map[string]string{"gross": "r1.gross", "days": "period.last_quarter.days"},
		"unit":       "money",
	})["computed"]
	if got != "13717.42" {
		t.Errorf("a per-day run rate = %v, want 13717.42", got)
	}
}

func TestPeriodRefusals(t *testing.T) {
	ctx, tool := moneyTurn(t, "IDR", "", 1)
	for _, tc := range []struct{ ref, wantIn string }{
		{"period.next_quarter.days", "not a period this workspace resolves"},
		{"period.last_quarter.weeks", "only figure a period offers is `days`"},
		{"period.days", "not a period reference"},
		{"period.", "not a period reference"},
	} {
		raw, _ := json.Marshal(map[string]interface{}{
			"expression": "d",
			"inputs":     map[string]string{"d": tc.ref},
		})
		_, err := tool.Execute(ctx, string(raw))
		if err == nil {
			t.Errorf("compute accepted %q", tc.ref)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantIn) {
			t.Errorf("binding %q = %v, want a message containing %q", tc.ref, err, tc.wantIn)
		}
	}
}
