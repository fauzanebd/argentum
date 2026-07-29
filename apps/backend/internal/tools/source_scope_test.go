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

// T-S2's security property, tested where it is enforced. "The Finance agent
// cannot read the HR source" has to mean a tool error, not a paragraph in a
// persona — so these run against ResolveSource and ListSourcesTool, the two
// places the scope is applied, rather than against a prompt.

func scoped(ids ...string) context.Context {
	return agentscope.WithScope(context.Background(),
		agentscope.Scope{AgentID: "ag-fin", Name: "Finance", SourceIDs: ids})
}

func twoSourceRepo() (*fakeConnRepo, *domain.DBConnection, *domain.DBConnection) {
	fin := conn("src-fin", "co-1", "Finance DW", "postgres")
	hr := conn("src-hr", "co-1", "People", "postgres")
	return &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{
		"co-1": {fin, hr},
	}}, fin, hr
}

// A scoped agent lives in a one-source world: no menu, no ambiguity, and no
// need to name the id it is the only one allowed to use.
func TestResolveSourceScopedToOneNeedsNoSourceID(t *testing.T) {
	repo, fin, _ := twoSourceRepo()

	got, err := ResolveSource(scoped("src-fin"), repo, "co-1", "")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if got != fin {
		t.Errorf("got %+v, want the finance source", got)
	}
}

func TestResolveSourceRefusesAnOutOfScopeSource(t *testing.T) {
	repo, _, _ := twoSourceRepo()

	got, err := ResolveSource(scoped("src-fin"), repo, "co-1", "src-hr")
	if err == nil {
		t.Fatalf("the Finance agent resolved the HR source: %+v", got)
	}
	// The refusal must not describe the source it refused. The id came from the
	// model, and a message naming "People" hands a prompt-injected turn the
	// label of the database it is not allowed to read.
	if strings.Contains(err.Error(), "People") {
		t.Errorf("err = %q names the out-of-scope source", err)
	}
}

// The ticket's third acceptance item, and the reason there is no distinct
// "not allowed for this agent" error: byte-identical text for a source that is
// out of scope and one that belongs to another tenant. A different string is a
// probe oracle — ask for an id, read which of the two errors comes back, learn
// whether the id exists.
func TestOutOfScopeAndForeignSourcesFailIdentically(t *testing.T) {
	fin := conn("src-fin", "co-1", "Finance DW", "postgres")
	hr := conn("src-hr", "co-1", "People", "postgres")
	theirs := conn("src-theirs", "co-2", "Another Tenant", "postgres")
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{
		"co-1": {fin, hr},
		"co-2": {theirs},
	}}
	ctx := scoped("src-fin")

	_, outOfScope := ResolveSource(ctx, repo, "co-1", "src-hr")
	_, foreign := ResolveSource(ctx, repo, "co-1", "src-theirs")
	_, absent := ResolveSource(ctx, repo, "co-1", "src-nope")
	if outOfScope == nil || foreign == nil || absent == nil {
		t.Fatal("one of the three refusals did not happen")
	}

	wantShape := strings.Replace(outOfScope.Error(), "src-hr", "%ID%", 1)
	for _, tc := range []struct {
		name string
		err  error
		id   string
	}{
		{"another tenant's source", foreign, "src-theirs"},
		{"a source that does not exist", absent, "src-nope"},
	} {
		got := strings.Replace(tc.err.Error(), tc.id, "%ID%", 1)
		if got != wantShape {
			t.Errorf("%s produced a different error than an out-of-scope source:\n got: %s\nwant: %s",
				tc.name, got, wantShape)
		}
	}
}

// The menu inside the error is the agent's only route out of it, so it must
// list what this agent may actually use — and nothing else.
func TestTheSourceMenuIsScoped(t *testing.T) {
	fin := conn("src-fin", "co-1", "Finance DW", "postgres")
	hr := conn("src-hr", "co-1", "People", "postgres")
	ops := conn("src-ops", "co-1", "Ops", "mysql")
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{"co-1": {fin, hr, ops}}}

	_, err := ResolveSource(scoped("src-fin", "src-ops"), repo, "co-1", "")
	if err == nil {
		t.Fatal("two in-scope sources and no source_id resolved without asking")
	}
	for _, want := range []string{"src-fin", "src-ops"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("menu = %q, want it to offer %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "src-hr") || strings.Contains(err.Error(), "People") {
		t.Errorf("menu = %q offers the out-of-scope source", err)
	}
}

// list_sources is the catalog the model orders from. If it names a database
// every later call is refused against, the turn spends its budget discovering
// that — so the tool is filtered by the same scope ResolveSource enforces.
func TestListSourcesIsScoped(t *testing.T) {
	repo, _, _ := twoSourceRepo()
	ctx := tenantctx.WithCompanyID(scoped("src-fin"), "co-1")

	out, err := NewListSourcesTool(repo).Execute(ctx, "")
	if err != nil {
		t.Fatalf("list_sources: %v", err)
	}
	var payload struct {
		Sources []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal list_sources result: %v", err)
	}
	if len(payload.Sources) != 1 || payload.Sources[0].ID != "src-fin" {
		t.Fatalf("sources = %+v, want only src-fin", payload.Sources)
	}
	if strings.Contains(out, "People") {
		t.Errorf("list_sources = %s, which names the out-of-scope source", out)
	}
}

// An unrestricted agent — the backfilled default every existing tenant got —
// sees exactly what the product showed before the roster existed.
func TestAnUnscopedTurnSeesEverySource(t *testing.T) {
	repo, _, _ := twoSourceRepo()
	ctx := tenantctx.WithCompanyID(agentscope.WithScope(
		context.Background(), agentscope.Scope{AgentID: "ag-default"}), "co-1")

	out, err := NewListSourcesTool(repo).Execute(ctx, "")
	if err != nil {
		t.Fatalf("list_sources: %v", err)
	}
	for _, want := range []string{"src-fin", "src-hr"} {
		if !strings.Contains(out, want) {
			t.Errorf("list_sources = %s, want it to list %q", out, want)
		}
	}
}
