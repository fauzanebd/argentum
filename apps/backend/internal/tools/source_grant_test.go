package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// T-Z6's second acceptance line: "An agent's ability to query that source is
// **unchanged** — the assertion that proves the two concepts did not get
// conflated."
//
// A grant on a source gates what a person is shown; what an agent may query is
// its own source list (agent_sources, T-H12). So this is a pin rather than a
// behaviour this ticket added: ResolveSource — the one resolver run_sql,
// get_schema and create_dashboard go through — has no grant store to ask, and a
// turn carrying a person refused the HR warehouse on every surface still
// resolves it for the agent scoped to it. If a later change wires a person's
// grants into source resolution, this fails, and whoever made it has to decide
// out loud that a user grant has become a data boundary.
func TestASourceGrantDoesNotNarrowWhatAnAgentMayQuery(t *testing.T) {
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{
		"co-1": {conn("src-main", "co-1", "Toko Maju warehouse", "postgres"), conn("src-hr", "co-1", "HR warehouse", "postgres")},
	}}
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	// The member the source list hid it from, on their own dashboard turn.
	ctx = tenantctx.WithUserID(ctx, "u-refused")
	ctx = agentscope.WithScope(ctx, agentscope.Scope{AgentID: "ag-hr", SourceIDs: []string{"src-hr"}})

	got, err := ResolveSource(ctx, repo, "co-1", "src-hr")
	if err != nil {
		t.Fatalf("ResolveSource = %v, want the agent's own source resolved whoever is asking", err)
	}
	if got.ID != "src-hr" {
		t.Errorf("resolved %q, want src-hr", got.ID)
	}
}

// RIZ-27 drew the same line for metrics. A member not granted the HR warehouse
// no longer sees `payroll_cost` in Settings → Metrics or by id — that is the
// dashboard's /api/metrics, narrowed in the handler — but the agent on their
// turn still lists it and still evaluates it, because a grant gates what a
// person is shown and never what an agent queries (T-Z6), and run_sql on the
// same agent reaches the same source anyway. Refusing query_metric alone would
// close nothing and reverse that rule for one tool.
//
// So this is a pin, like the one above: neither tool is built with anything to
// ask about a person's grants — the store is their only dependency — and a turn
// carrying the refused member gets the metric and its value. If a later change
// gives either tool a grant check, this fails, and that change owes the prompt
// a paired `make eval` and access-grants §17c a rewritten row.
func TestAMetricOnARestrictedSourceStillAnswersAnAgent(t *testing.T) {
	res := oneMetricResult(false)
	res.Metric.Key, res.Metric.Label, res.Metric.SourceID = "payroll_cost", "Payroll cost", "src-hr"
	store := &fakeMetricStore{result: res}

	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	// The member Settings → Metrics now hides payroll_cost from.
	ctx = tenantctx.WithUserID(ctx, "u-refused")
	ctx = agentscope.WithScope(ctx, agentscope.Scope{AgentID: "ag-hr", SourceIDs: []string{"src-hr"}})

	listed, err := NewListMetricsTool(store).Execute(ctx, `{}`)
	if err != nil || !strings.Contains(listed, `"payroll_cost"`) {
		t.Fatalf("list_metrics = %s, %v; want the restricted source's metric listed for the agent", listed, err)
	}

	out, err := NewQueryMetricTool(store, nil).Execute(ctx, `{"metric_key":"payroll_cost","from":"2024-12-01","to":"2024-12-31"}`)
	if err != nil {
		t.Fatalf("query_metric = %v, want the metric evaluated whoever is asking", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("result is not JSON: %v: %s", err, out)
	}
	if payload["row_count"] != float64(1) || !strings.Contains(out, "3863405700") {
		t.Errorf("query_metric = %s; want payroll_cost's value, evaluated as before RIZ-27", out)
	}
}
