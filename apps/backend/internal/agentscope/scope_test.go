package agentscope

import (
	"context"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

func srcs(ids ...string) []*domain.DBConnection {
	out := make([]*domain.DBConnection, 0, len(ids))
	for _, id := range ids {
		out = append(out, &domain.DBConnection{ID: id})
	}
	return out
}

func idsOf(conns []*domain.DBConnection) []string {
	out := make([]string, 0, len(conns))
	for _, c := range conns {
		out = append(out, c.ID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A context with no scope is the ordinary case for anything that is not a chat
// turn — the connection describer, a reindex, the eval harness — and it must
// behave exactly as the product did before this package existed.
func TestAnAbsentScopeRestrictsNothing(t *testing.T) {
	ctx := context.Background()
	s := FromContext(ctx)

	if s.AgentID != "" || AgentID(ctx) != "" {
		t.Errorf("AgentID = %q, want empty", s.AgentID)
	}
	if !s.AllowsSource("anything") {
		t.Error("an unscoped turn refused a source")
	}
	all := srcs("a", "b", "c")
	if got := idsOf(s.FilterSources(all)); !equal(got, []string{"a", "b", "c"}) {
		t.Errorf("FilterSources = %v, want every source", got)
	}
}

// Locked decision 2, on the enforcement side: an empty allowlist is every
// source the company owns. The opposite reading — empty means nothing — would
// make a newly connected database invisible to every existing agent.
func TestAnEmptyAllowlistIsEverySource(t *testing.T) {
	ctx := WithScope(context.Background(), Scope{AgentID: "ag-1"})
	s := FromContext(ctx)

	if AgentID(ctx) != "ag-1" {
		t.Errorf("AgentID = %q, want ag-1", AgentID(ctx))
	}
	if !s.AllowsSource("src-new") {
		t.Error("a source connected after the agent was created was refused")
	}
	if got := idsOf(s.FilterSources(srcs("a", "b"))); !equal(got, []string{"a", "b"}) {
		t.Errorf("FilterSources = %v, want every source", got)
	}
}

func TestAScopedAgentSeesOnlyItsOwnSources(t *testing.T) {
	ctx := WithScope(context.Background(), Scope{AgentID: "ag-fin", SourceIDs: []string{"src-fin"}})
	s := FromContext(ctx)

	if !s.AllowsSource("src-fin") {
		t.Error("the agent was refused its own source")
	}
	if s.AllowsSource("src-hr") {
		t.Error("the Finance agent was allowed the HR source")
	}
	if got := idsOf(s.FilterSources(srcs("src-fin", "src-hr"))); !equal(got, []string{"src-fin"}) {
		t.Errorf("FilterSources = %v, want only src-fin", got)
	}
}

// An agent scoped to a source that no longer exists filters to nothing rather
// than to everything. agent_sources cascades on connection delete, so the
// live version of this is transient — but the direction of the failure is the
// point: a scope that cannot be satisfied refuses, it does not widen.
func TestAScopeNamingNothingPresentFiltersToNothing(t *testing.T) {
	s := Scope{AgentID: "ag-1", SourceIDs: []string{"src-gone"}}
	if got := s.FilterSources(srcs("src-a", "src-b")); len(got) != 0 {
		t.Errorf("FilterSources = %v, want nothing", idsOf(got))
	}
}

// MCP bindings are the inverse of sources: empty means NONE (T-M2, locked
// decision 5). A turn with no binding — the eval harness, an unscoped company,
// an agent nobody bound a server to — reaches no MCP server, which is what makes
// the company-tools path a no-op and today's tool list unchanged.
func TestEmptyMCPBindingMeansNone(t *testing.T) {
	if (Scope{}).AllowsMCPServer("srv-1") {
		t.Error("an empty binding must allow no MCP server")
	}
	if (Scope{MCPServerIDs: []string{"srv-1"}}).AllowsMCPServer("srv-2") {
		t.Error("a binding to one server must not allow another")
	}
	if !(Scope{MCPServerIDs: []string{"srv-1"}}).AllowsMCPServer("srv-1") {
		t.Error("a bound server must be allowed")
	}
}
