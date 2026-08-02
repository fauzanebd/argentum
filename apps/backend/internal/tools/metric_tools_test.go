package tools

import (
	"context"
	"encoding/json"
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
