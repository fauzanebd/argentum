package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

type fakeMetricStore struct {
	result *metric.Result
}

func (f *fakeMetricStore) ListEnabled(context.Context, string) ([]*domain.MetricDefinition, error) {
	return []*domain.MetricDefinition{f.result.Metric}, nil
}

func (f *fakeMetricStore) Query(context.Context, string, string, time.Time, time.Time, metric.Comparison) (*metric.Result, error) {
	return f.result, nil
}

func oneMetricResult(compared bool) *metric.Result {
	from := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	res := &metric.Result{
		Metric: &domain.MetricDefinition{
			Key: "revenue", Label: "Revenue", Unit: domain.MetricUnitCurrency, Currency: "IDR",
		},
		Primary: metric.Evaluation{Value: 3863405700, From: from, To: to},
	}
	if compared {
		prev := metric.Evaluation{Value: 3000000000, From: from.AddDate(0, -1, 0), To: to.AddDate(0, -1, 0)}
		res.Comparison = &prev
	}
	return res
}

// A metric value is evidence for a figure in the reply, and the only way it
// says so is row_count on the tool result: agentbudget.Tracker.Observe reads
// that key to ground T-16's fabrication check, and T-05's audit decorator reads
// it as rows_returned. Omitting it made every metric-only answer read as
// ungrounded, so a correct figure was replaced with "my query returned no
// data" — observed live on 2026-08-02 against the demo warehouse.
func TestQueryMetricResultCarriesRowCount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compared bool
		want     float64
	}{
		{"single window", false, 1},
		{"with comparison", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewQueryMetricTool(&fakeMetricStore{result: oneMetricResult(tc.compared)}, nil)
			ctx := tenantctx.WithCompanyID(context.Background(), "c1")

			out, err := tool.Execute(ctx, `{"metric_key":"revenue","from":"2024-12-01","to":"2024-12-31"}`)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}
			if got := payload["row_count"]; got != tc.want {
				t.Fatalf("row_count = %v, want %v (result: %s)", got, tc.want, out)
			}
		})
	}
}

// The end the defect was actually felt at: the tracker must count a metric read
// as evidence, because grounded() is DataRows > 0 and nothing else feeds it on
// a turn that never touched run_sql.
func TestQueryMetricResultGroundsTheTurn(t *testing.T) {
	tool := NewQueryMetricTool(&fakeMetricStore{result: oneMetricResult(false)}, nil)
	ctx := tenantctx.WithCompanyID(context.Background(), "c1")

	out, err := tool.Execute(ctx, `{"metric_key":"revenue","from":"2024-12-01","to":"2024-12-31"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	tracker := agentbudget.New(agentbudget.Budget{MaxIterations: 8})
	tracker.Observe("query_metric", out, nil)
	if rows := tracker.Snapshot().DataRows; rows == 0 {
		t.Fatalf("query_metric result left the turn ungrounded (DataRows=%d): a reply stating the "+
			"metric's own figure would be replaced with an incomplete-answer message", rows)
	}
}

// The Q3-2025 case from the 2026-08-14 run: a window the warehouse holds no
// data for came back as "Rp 0", which is a different sentence from "we have no
// data for that period" and the only one of the two that is false.
func TestAnEmptyWindowIsNotAZero(t *testing.T) {
	res := oneMetricResult(false)
	res.Primary = metric.Evaluation{Empty: true, From: res.Primary.From, To: res.Primary.To}
	tool := NewQueryMetricTool(&fakeMetricStore{result: res}, nil)
	ctx := tenantctx.WithCompanyID(context.Background(), "c1")

	out, err := tool.Execute(ctx, `{"metric_key":"revenue","from":"2025-07-01","to":"2025-09-30"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if payload["value"] != nil {
		t.Errorf("value = %v, want null — a zero in that field is the claim being denied", payload["value"])
	}
	if payload["row_count"] != float64(0) {
		t.Errorf("row_count = %v, want 0", payload["row_count"])
	}
	if payload["note"] == nil {
		t.Error("the result carries no note: the empty set alone was not enough of a signal for run_sql either")
	}

	// And the end it is felt at: no evidence, so a reply that states a figure
	// anyway is replaced rather than sent.
	tracker := agentbudget.New(agentbudget.Budget{MaxIterations: 8})
	tracker.Observe("query_metric", out, nil)
	snap := tracker.Snapshot()
	if snap.DataRows != 0 || snap.EmptyResults != 1 {
		t.Errorf("tracker read the empty window as evidence: rows=%d empty=%d", snap.DataRows, snap.EmptyResults)
	}
}

// A comparison window past the start of the warehouse is the commoner half,
// and reading it as zero produces the growth figure that is hardest to unsay.
func TestAnEmptyComparisonWindowCarriesNoValue(t *testing.T) {
	res := oneMetricResult(true)
	res.Comparison = &metric.Evaluation{Empty: true, From: res.Comparison.From, To: res.Comparison.To}
	res.Delta, res.DeltaPct = nil, nil
	tool := NewQueryMetricTool(&fakeMetricStore{result: res}, nil)
	ctx := tenantctx.WithCompanyID(context.Background(), "c1")

	out, err := tool.Execute(ctx, `{"metric_key":"revenue","from":"2024-12-01","to":"2024-12-31","compare_to":"same_period_last_year"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	cmp, ok := payload["comparison"].(map[string]any)
	if !ok {
		t.Fatalf("comparison missing from the payload: %s", out)
	}
	if cmp["value"] != nil {
		t.Errorf("comparison value = %v, want null", cmp["value"])
	}
	if _, has := payload["delta_pct"]; has {
		t.Error("a percentage change was reported against a window with no data")
	}
	// The primary window has a real figure, and it is still evidence.
	if payload["row_count"] != float64(1) {
		t.Errorf("row_count = %v, want 1: the primary window returned a number", payload["row_count"])
	}
}

// A genuine zero keeps its zero. What it gains is a sentence telling the model
// not to guess which kind of zero it is — a COALESCE(SUM(x), 0) template
// answers an empty window with a real 0 and this tool cannot tell them apart.
func TestARealZeroIsStillZeroWithACaveat(t *testing.T) {
	res := oneMetricResult(false)
	res.Primary.Value = 0
	tool := NewQueryMetricTool(&fakeMetricStore{result: res}, nil)
	ctx := tenantctx.WithCompanyID(context.Background(), "c1")

	out, err := tool.Execute(ctx, `{"metric_key":"revenue","from":"2024-12-01","to":"2024-12-31"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if payload["value"] != float64(0) {
		t.Errorf("value = %v, want 0", payload["value"])
	}
	if payload["row_count"] != float64(1) {
		t.Errorf("row_count = %v, want 1: the metric did evaluate", payload["row_count"])
	}
	if payload["note"] == nil {
		t.Error("a zero carries no caveat, so the model cannot tell it from an empty window")
	}
}

// And what the caveat becomes once the service has actually checked (2026-08-14).
//
// The hedge above is what shipped "Rp 0" for Q3 2025 against a warehouse ending
// in December 2024, on both models, in the same eval run. A verdict turns the
// same branch into a statement — and for the two verdicts that mean "this
// window is outside the data" it also empties `row_count`, so T-16's grounding
// check treats a stated total as ungrounded rather than trusting the model to
// follow advice.
func TestAZeroOutsideTheDataIsNotEvidence(t *testing.T) {
	before := 1000.0
	after := 1000.0

	cases := []struct {
		name        string
		coverage    *metric.ZeroCoverage
		wantRows    float64
		wantValue   any
		wantPhrases []string
	}{
		{
			name:        "after the data ends",
			coverage:    &metric.ZeroCoverage{Verdict: metric.ZeroAfterCoverage, Before: &before},
			wantRows:    0,
			wantValue:   nil,
			wantPhrases: []string{"NOT an answer", "AFTER the end of the data", "Do NOT state 0"},
		},
		{
			name:        "before the data begins",
			coverage:    &metric.ZeroCoverage{Verdict: metric.ZeroBeforeCoverage, After: &after},
			wantRows:    0,
			wantValue:   nil,
			wantPhrases: []string{"BEFORE the data begins", "does not go back that far"},
		},
		{
			// The other half: a checked zero is reported plainly, with no
			// caveat at all. A tool that hedges on every zero teaches the model
			// to ignore the hedge.
			name:        "inside the data",
			coverage:    &metric.ZeroCoverage{Verdict: metric.ZeroInsideCoverage, Before: &before, After: &after},
			wantRows:    1,
			wantValue:   float64(0),
			wantPhrases: []string{"genuinely 0", "no coverage caveat is needed"},
		},
		{
			name:        "zero everywhere",
			coverage:    &metric.ZeroCoverage{Verdict: metric.ZeroEverywhere},
			wantRows:    1,
			wantValue:   float64(0),
			wantPhrases: []string{"every other period", "broken metric definition"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := oneMetricResult(false)
			res.Primary.Value = 0
			res.Primary.Zero = tc.coverage
			tool := NewQueryMetricTool(&fakeMetricStore{result: res}, nil)
			ctx := tenantctx.WithCompanyID(context.Background(), "c1")

			out, err := tool.Execute(ctx, `{"metric_key":"revenue","from":"2025-07-01","to":"2025-10-01"}`)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}

			if payload["row_count"] != tc.wantRows {
				t.Errorf("row_count = %v, want %v", payload["row_count"], tc.wantRows)
			}
			if payload["value"] != tc.wantValue {
				t.Errorf("value = %v, want %v", payload["value"], tc.wantValue)
			}
			note, _ := payload["note"].(string)
			for _, phrase := range tc.wantPhrases {
				if !strings.Contains(note, phrase) {
					t.Errorf("note = %q, want it to contain %q", note, phrase)
				}
			}
			cov, ok := payload["zero_coverage"].(map[string]any)
			if !ok {
				t.Fatalf("zero_coverage missing from the payload: %v", payload["zero_coverage"])
			}
			if cov["verdict"] != string(tc.coverage.Verdict) {
				t.Errorf("verdict = %v, want %q", cov["verdict"], tc.coverage.Verdict)
			}
		})
	}
}

// windowCapturingStore records the window it was asked for, which is the only
// way to prove what an omitted one resolves to.
type windowCapturingStore struct {
	result   *metric.Result
	gotFrom  time.Time
	gotTo    time.Time
	numCalls int
}

func (s *windowCapturingStore) ListEnabled(context.Context, string) ([]*domain.MetricDefinition, error) {
	return []*domain.MetricDefinition{s.result.Metric}, nil
}

func (s *windowCapturingStore) Query(_ context.Context, _, _ string, from, to time.Time, _ metric.Comparison) (*metric.Result, error) {
	s.gotFrom, s.gotTo, s.numCalls = from, to, s.numCalls+1
	return s.result, nil
}

// A question that names no period is answered over all of the data, not by
// asking the user which period they meant.
//
// from and to were both required until 2026-08-14, so "what is our total
// revenue" left the model three bad options — invent a range, abandon the
// authoritative metric for run_sql, or ask — and two eval runs took the third
// on four cases across two models, against a guideline that already told them
// not to. A guideline loses to a missing affordance.
func TestAnOmittedWindowCoversAllAvailableData(t *testing.T) {
	store := &windowCapturingStore{result: oneMetricResult(false)}
	tool := NewQueryMetricTool(store, nil)
	ctx := tenantctx.WithCompanyID(context.Background(), "c1")

	out, err := tool.Execute(ctx, `{"metric_key":"revenue"}`)
	if err != nil {
		t.Fatalf("a call with no window was refused: %v", err)
	}
	if store.numCalls != 1 {
		t.Fatalf("store called %d times, want 1", store.numCalls)
	}
	if store.gotFrom.Year() != 1900 {
		t.Errorf("window start = %s, want the 1900 floor", store.gotFrom)
	}
	// One year out, per the MySQL TIMESTAMP ceiling argument in allTimeWindow.
	if !store.gotTo.After(time.Now().UTC()) || store.gotTo.After(time.Now().UTC().AddDate(1, 0, 1)) {
		t.Errorf("window end = %s, want roughly one year from now", store.gotTo)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if payload["window_scope"] != "all_available_data" {
		t.Errorf("window_scope = %v, so the model cannot tell this was not the caller's window", payload["window_scope"])
	}
	if payload["window_note"] == nil {
		t.Error("no window_note, so the model may quote 1900 at a user who asked for a total")
	}
}

// A caller who named one bound half-specified a window, and guessing the other
// half is how a metric answers a question nobody asked.
func TestHalfAWindowIsAnError(t *testing.T) {
	tool := NewQueryMetricTool(&windowCapturingStore{result: oneMetricResult(false)}, nil)
	ctx := tenantctx.WithCompanyID(context.Background(), "c1")

	for _, args := range []string{
		`{"metric_key":"revenue","from":"2024-07-01"}`,
		`{"metric_key":"revenue","to":"2024-12-31"}`,
	} {
		if _, err := tool.Execute(ctx, args); err == nil {
			t.Errorf("%s was accepted; half a window should be refused", args)
		}
	}
}

// The window a caller does name is still the window that runs.
func TestAnExplicitWindowIsUnchanged(t *testing.T) {
	store := &windowCapturingStore{result: oneMetricResult(false)}
	tool := NewQueryMetricTool(store, nil)
	ctx := tenantctx.WithCompanyID(context.Background(), "c1")

	out, err := tool.Execute(ctx, `{"metric_key":"revenue","from":"2024-12-01","to":"2024-12-31"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if store.gotFrom != time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC) ||
		store.gotTo != time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC) {
		t.Errorf("window = %s .. %s, want the dates the caller passed", store.gotFrom, store.gotTo)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if _, ok := payload["window_scope"]; ok {
		t.Error("an explicit window was labelled all-time")
	}
}
