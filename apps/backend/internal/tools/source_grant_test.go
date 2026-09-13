package tools

import (
	"context"
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
