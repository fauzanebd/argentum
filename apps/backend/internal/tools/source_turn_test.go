package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The retry loop these exist for: a two-source tenant, get_schema resolves one,
// and the create_visualization two calls later omits source_id and is sent back
// to the menu it cannot act on. Recorded as reproducible defect 2 in
// coverage/eval-sprint1.md §4, on two different models.

func TestTurnSourceIsReusedByALaterCall(t *testing.T) {
	repo, fin, _ := twoSourceRepo()
	ctx := WithTurnSource(context.Background())

	// Call one names the source, the way get_schema does after reading the menu.
	if _, err := ResolveSource(ctx, repo, "co-1", "src-fin"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Call two omits it. Before the fix this was the "specify source_id" error.
	got, err := ResolveSource(ctx, repo, "co-1", "")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got != fin {
		t.Errorf("got %+v, want the source the turn already resolved", got)
	}
}

// The memory is per turn. Two turns on one tenant must not leak into each
// other, or a question about People answers from Finance.
func TestTurnSourceDoesNotCrossTurns(t *testing.T) {
	repo, _, _ := twoSourceRepo()

	first := WithTurnSource(context.Background())
	if _, err := ResolveSource(first, repo, "co-1", "src-fin"); err != nil {
		t.Fatalf("first turn: %v", err)
	}

	second := WithTurnSource(context.Background())
	if _, err := ResolveSource(second, repo, "co-1", ""); err == nil {
		t.Error("a fresh turn inherited the previous turn's source")
	}
}

// A context with no memory installed behaves exactly as it did before this
// existed. Every caller that is not the chat runner — the report service, the
// MCP server, the older tests — is on this path.
func TestWithoutTheMemoryTheMenuIsUnchanged(t *testing.T) {
	repo, _, _ := twoSourceRepo()

	if _, err := ResolveSource(context.Background(), repo, "co-1", "src-fin"); err != nil {
		t.Fatalf("explicit resolve: %v", err)
	}
	_, err := ResolveSource(context.Background(), repo, "co-1", "")
	if err == nil || !strings.Contains(err.Error(), "specify source_id") {
		t.Errorf("err = %v, want the unchanged multi-source menu", err)
	}
}

// The security property. Reuse reads from the post-filter catalog, so a
// remembered id the roster stops allowing is simply not found — the memory can
// never widen what the scope permits, only save a round trip inside it.
//
// Three sources, deliberately: the turn remembers the one it is later not
// allowed, and the scope still leaves *two* behind. A two-source scope is what
// makes this test mean anything — narrow it to one and the single-connection
// branch answers before reuse is ever consulted, so the test would pass with
// the check removed.
func TestTurnSourceCannotWidenTheScope(t *testing.T) {
	fin := conn("src-fin", "co-1", "Finance DW", "postgres")
	hr := conn("src-hr", "co-1", "People", "postgres")
	ops := conn("src-ops", "co-1", "Ops", "postgres")
	repo := &fakeConnRepo{byCompany: map[string][]*domain.DBConnection{
		"co-1": {fin, hr, ops},
	}}

	ctx := WithTurnSource(context.Background())
	if _, err := ResolveSource(ctx, repo, "co-1", "src-hr"); err != nil {
		t.Fatalf("unscoped resolve: %v", err)
	}

	// Same turn, now scoped to two sources — neither of them the remembered one.
	scopedCtx := agentscope.WithScope(ctx, agentscope.Scope{
		AgentID: "ag-fin", Name: "Finance", SourceIDs: []string{"src-fin", "src-ops"},
	})

	got, err := ResolveSource(scopedCtx, repo, "co-1", "")
	if err == nil {
		t.Fatalf("the remembered out-of-scope source was reused: %+v — reuse must not widen the allowlist", got)
	}
	// And the refusal is the ordinary menu, naming only what this agent may
	// reach. A turn that cannot reuse is a turn that has not chosen yet.
	if !strings.Contains(err.Error(), "specify source_id") {
		t.Errorf("err = %q, want the multi-source menu", err)
	}
	if strings.Contains(err.Error(), "People") {
		t.Errorf("err = %q names the out-of-scope source", err)
	}
}

// A single-source resolve is remembered too. It costs nothing and it means a
// tenant that adds a second source mid-turn does not strand the calls that
// follow.
func TestASingleSourceResolveIsRemembered(t *testing.T) {
	repo, fin, _ := twoSourceRepo()
	ctx := WithTurnSource(agentscope.WithScope(context.Background(),
		agentscope.Scope{AgentID: "ag-fin", SourceIDs: []string{"src-fin"}}))

	if _, err := ResolveSource(ctx, repo, "co-1", ""); err != nil {
		t.Fatalf("scoped single resolve: %v", err)
	}
	if got := recalledSource(ctx); got != fin.ID {
		t.Errorf("recalled %q, want %q", got, fin.ID)
	}
}
