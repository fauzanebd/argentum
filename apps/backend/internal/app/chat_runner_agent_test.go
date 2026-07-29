package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// T-S2's resolution rules, which decide what every other part of the ticket
// then enforces. They are stated here rather than inferred from a live turn
// because two of the three are failure paths: an agent deleted mid-conversation
// and a roster that cannot be read.

// fakeRoster is the two reads a turn makes. It counts them so a test can say
// which lookup happened, not just what came back.
type fakeRoster struct {
	byID    map[string]*domain.Agent
	def     *domain.Agent
	err     error
	getByID int
	getDef  int
}

func (f *fakeRoster) GetByID(_ context.Context, companyID, id string) (*domain.Agent, error) {
	f.getByID++
	if f.err != nil {
		return nil, f.err
	}
	a, ok := f.byID[id]
	if !ok || a.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return a, nil
}

func (f *fakeRoster) GetDefault(_ context.Context, companyID string) (*domain.Agent, error) {
	f.getDef++
	if f.err != nil {
		return nil, f.err
	}
	if f.def == nil || f.def.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return f.def, nil
}

func agentRow(id, name string, sources ...string) *domain.Agent {
	return &domain.Agent{
		ID: id, CompanyID: "co-1", Name: name,
		PersonaPrompt: "persona of " + name,
		SourceIDs:     sources,
		Enabled:       true,
	}
}

func rosterFixture() (*ChatRunner, *fakeRoster) {
	fin := agentRow("ag-fin", "Finance", "src-fin")
	def := agentRow("ag-def", "Analyst")
	def.IsDefault = true
	r := &fakeRoster{byID: map[string]*domain.Agent{"ag-fin": fin, "ag-def": def}, def: def}
	return (&ChatRunner{}).WithRoster(r), r
}

func TestTheTurnRunsAsTheAgentThePayloadNames(t *testing.T) {
	runner, roster := rosterFixture()

	got := runner.resolveAgent(context.Background(),
		queue.ChatRunPayload{CompanyID: "co-1", AgentID: "ag-fin"})

	if got == nil || got.ID != "ag-fin" {
		t.Fatalf("resolveAgent = %+v, want the finance agent", got)
	}
	if roster.getDef != 0 {
		t.Error("the default was looked up even though the payload named an agent")
	}
}

func TestATurnNamingNoAgentRunsAsTheCompanyDefault(t *testing.T) {
	runner, _ := rosterFixture()

	got := runner.resolveAgent(context.Background(), queue.ChatRunPayload{CompanyID: "co-1"})

	if got == nil || got.ID != "ag-def" {
		t.Fatalf("resolveAgent = %+v, want the default agent", got)
	}
}

// The ticket's last acceptance item. A conversation must not become
// unanswerable because an admin tidied the roster between the question being
// asked and the worker picking it up.
func TestAnAgentDeletedMidConversationFallsBackToTheDefault(t *testing.T) {
	runner, roster := rosterFixture()

	got := runner.resolveAgent(context.Background(),
		queue.ChatRunPayload{CompanyID: "co-1", AgentID: "ag-gone"})

	if got == nil || got.ID != "ag-def" {
		t.Fatalf("resolveAgent = %+v, want the default agent", got)
	}
	if roster.getByID != 1 || roster.getDef != 1 {
		t.Errorf("lookups = %d by id / %d default, want one of each", roster.getByID, roster.getDef)
	}
}

// Another company's agent id is the same answer as a deleted one: not found,
// then the caller's own default. The tenant boundary is inside the repository
// query, which is why the runner needs no check of its own.
func TestAnotherCompanysAgentIDResolvesToOwnDefault(t *testing.T) {
	runner, _ := rosterFixture()
	theirs := &domain.Agent{ID: "ag-theirs", CompanyID: "co-2", Name: "Theirs"}
	runner.roster.(*fakeRoster).byID["ag-theirs"] = theirs

	got := runner.resolveAgent(context.Background(),
		queue.ChatRunPayload{CompanyID: "co-1", AgentID: "ag-theirs"})

	if got == nil || got.ID != "ag-def" {
		t.Fatalf("resolveAgent = %+v, want this company's default", got)
	}
}

// Three ways to have no agent, one behaviour: run the turn. A tenant who
// cannot ask a question because a settings table is empty or unreachable is a
// worse outcome than a turn that runs unrestricted, which is what this product
// did for its whole life before the roster.
func TestNoAgentIsNotAFailure(t *testing.T) {
	cases := map[string]*ChatRunner{
		"no roster wired":       {},
		"company has no roster": (&ChatRunner{}).WithRoster(&fakeRoster{}),
		"the roster cannot be read": (&ChatRunner{}).WithRoster(
			&fakeRoster{err: errors.New("control DB down")}),
	}
	for name, runner := range cases {
		t.Run(name, func(t *testing.T) {
			got := runner.resolveAgent(context.Background(),
				queue.ChatRunPayload{CompanyID: "co-1", AgentID: "ag-fin"})
			if got != nil {
				t.Fatalf("resolveAgent = %+v, want nil", got)
			}
			if scope := scopeOf(got); scope.AgentID != "" || !scope.AllowsSource("anything") {
				t.Errorf("scope = %+v, want an unrestricted one", scope)
			}
			if personaOf(got) != "" || toolNamesOf(got) != nil {
				t.Error("a nil agent produced a persona or a tool allowlist")
			}
		})
	}
}

// What the three extractors hand the rest of the turn: the scope the tools
// enforce, the persona the factory appends, the allowlist it filters by.
func TestWhatATurnTakesFromItsAgent(t *testing.T) {
	a := agentRow("ag-fin", "Finance", "src-fin")
	a.AllowedTools = []string{"run_sql"}

	scope := scopeOf(a)
	if scope.AgentID != "ag-fin" || scope.Name != "Finance" {
		t.Errorf("scope = %+v, want it to identify the finance agent", scope)
	}
	if scope.AllowsSource("src-hr") {
		t.Error("the scope admits a source the agent is not allowed")
	}
	if personaOf(a) != "persona of Finance" {
		t.Errorf("persona = %q", personaOf(a))
	}
	if got := toolNamesOf(a); len(got) != 1 || got[0] != "run_sql" {
		t.Errorf("tool names = %v, want [run_sql]", got)
	}
}

// A guardrail stops a turn before any tool runs, so this row is written by the
// runner rather than by the audit decorator — and it has to carry the agent id
// the same way. Off the context, because the turn may be running as the
// default after the payload's agent went away.
func TestTheBlockedTurnRowCarriesTheAgent(t *testing.T) {
	repo := &fakeActionRepo{}
	r := (&ChatRunner{}).WithActionLog(repo)
	ctx := agentscope.WithScope(context.Background(), agentscope.Scope{AgentID: "ag-def"})

	r.recordBlockedTurn(ctx, queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", UserMsgID: "msg-1", AgentID: "ag-gone",
	}, "guardrail", "off-topic")

	if len(repo.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(repo.rows))
	}
	if got := repo.rows[0].AgentID; got != "ag-def" {
		t.Errorf("agent_id = %q, want the agent that actually ran", got)
	}
}
