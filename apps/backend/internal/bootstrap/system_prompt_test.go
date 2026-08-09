package bootstrap

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/tools"
)

// The prompt and the tool array have to agree.
//
// The live case: an agent created from the Sales template holds get_schema,
// run_sql and create_visualization, because that is what the template's
// suggested_tools said and the dashboard copies them into allowed_tools
// verbatim. The prompt was a constant naming all nine tools, so the model was
// told it could generate documents. Asked for "a report in PDF" it produced a
// wall of markdown, said it could not export a file "from this interface", and
// told the user to press Ctrl+P — which is what a model does with a capability
// it has been promised and not given.

// promptFor is the composed prompt for an agent holding exactly these tools.
func promptFor(t *testing.T, names ...string) string {
	t.Helper()
	llm := &recordingLLM{reply: "ok"}
	all := make([]interfaces.Tool, 0, len(names))
	for _, n := range names {
		all = append(all, stubTool{n})
	}
	agent, err := newAgentFactory(agentFactoryDeps{
		systemPrompt: SystemPromptForTurn, tools: all, maxIterations: 3,
	})(app.AgentSpec{Primary: llm, Light: llm})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	return agent.GetSystemPrompt()
}

// The regression, at the layer that produced it: the prompt an agent gets is
// composed from the tools that agent was handed.
func TestThePromptDescribesOnlyTheToolsTheTurnGot(t *testing.T) {
	prompt := promptFor(t, "get_schema", "run_sql", "create_visualization")

	for _, absent := range []string{"generate_document", "create_dashboard", "query_metric", "schedule_task", "propose_action"} {
		if strings.Contains(prompt, absent) {
			t.Errorf("the prompt describes %q, which this agent was not given", absent)
		}
	}
	for _, present := range []string{"get_schema", "run_sql", "create_visualization"} {
		if !strings.Contains(prompt, present) {
			t.Errorf("the prompt does not describe %q, which this agent holds", present)
		}
	}
}

// The user-visible half of the same bug, stated as the thing that must not
// happen: an agent with no document tool is never told it can hand back a file.
func TestAnAgentWithoutTheDocumentToolIsNotPromisedFiles(t *testing.T) {
	if prompt := promptFor(t, "get_schema", "run_sql"); strings.Contains(prompt, "generate_document") {
		t.Error("an agent that cannot produce a document was told it could")
	}

	// And the agent that does hold it is told to use it, because the tool being
	// present was never the whole problem: `POST /v1/reports` needs a directive
	// to stop the model answering a report request in prose (T-A2), and a chat
	// turn asking for the same thing has no directive at all.
	prompt := promptFor(t, "run_sql", "generate_document")
	for _, want := range []string{
		"WHEN THE USER ASKS FOR A FILE, PRODUCE THE FILE",
		"never tell the user to print the reply or save it as a PDF themselves",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not carry %q", want)
		}
	}
}

// A guideline that depends on a tool travels with it. The pair this exists for
// is charts: guideline 7 says to wrap cards in a dashboard and guideline 8 says
// never to return a card id — and an agent holding create_visualization without
// create_dashboard cannot satisfy both, because a card id is not something a
// user can open.
func TestTheDashboardRulesFollowTheDashboardTool(t *testing.T) {
	paired := promptFor(t, "run_sql", "create_visualization", "create_dashboard")
	if !strings.Contains(paired, "then create_dashboard ONCE") {
		t.Error("an agent that can build a dashboard was not told to")
	}

	lone := promptFor(t, "run_sql", "create_visualization")
	if strings.Contains(lone, "always wrap with a dashboard") {
		t.Error("an agent without create_dashboard was told to wrap its cards in one")
	}
	if !strings.Contains(lone, "CHARTS WITHOUT A DASHBOARD") {
		t.Error("an agent that can make cards it cannot wrap was left without a rule for that")
	}
}

// Numbering is generated, so a filtered prompt has no holes in it. A prompt
// that jumps from 5 to 7 tells the model something was removed and invites it
// to guess what.
func TestTheGuidelinesAreNumberedWithoutGaps(t *testing.T) {
	for _, prompt := range []string{
		SystemPrompt(),
		promptFor(t, "run_sql"),
		promptFor(t, "get_schema", "run_sql", "create_visualization"),
	} {
		n := 0
		for line := range strings.SplitSeq(prompt, "\n") {
			dot := strings.Index(line, ". ")
			if dot <= 0 {
				continue
			}
			got, err := strconv.Atoi(line[:dot])
			if err != nil {
				continue
			}
			n++
			if got != n {
				t.Fatalf("guideline numbered %d where %d was expected:\n%s", got, n, line)
			}
		}
		if n < 4 {
			t.Fatalf("only %d guidelines rendered; the scan is not finding them", n)
		}
	}
}

// Every tool this release can register has a line in the catalog, and every
// line describes a tool that exists.
//
// The drift this prevents already happened once: propose_action shipped with no
// line in the prompt at all, so the agent learned about the one write-capable
// tool it has only from the tool definition. A tool added to the registry now
// fails here until somebody writes the sentence that says when to reach for it.
func TestEveryRegisteredToolHasAPromptLine(t *testing.T) {
	described, registered := PromptToolNames(), tools.AllNames()
	for _, name := range registered {
		if !slices.Contains(described, name) {
			t.Errorf("tool %q is registered and has no line in the system prompt", name)
		}
	}
	for _, name := range described {
		if !slices.Contains(registered, name) {
			t.Errorf("the system prompt describes %q, which no longer exists in the registry", name)
		}
	}
}

// A deployment whose agent is scoped to tools it does not have leaves the turn
// with none (filterTools warns about it). The prompt then has to say so: a
// model told it has nine tools and given zero answers from memory, which is the
// failure T-16 exists to prevent.
func TestATurnWithNoToolsIsToldItHasNone(t *testing.T) {
	prompt := SystemPromptFor(nil)
	if !strings.Contains(prompt, "NO data tools available") {
		t.Error("a turn with no tools was not told it has none")
	}
	if strings.Contains(prompt, "You have access to these tools") {
		t.Error("a turn with no tools was still shown a tool catalog")
	}
	// The rules that owe nothing to a tool survive: it still may not invent a
	// figure, and it still answers in the user's language.
	for _, want := range []string{"NEVER STATE A FIGURE YOU DID NOT RETRIEVE", "LANGUAGE IS THE TOP PRIORITY"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the toolless prompt lost %q", want)
		}
	}
}

// A tenant's MCP tools are not described line by line — they carry their own
// descriptions — but the catalog must not read as exhaustive when they are
// there.
func TestTenantToolsAreAcknowledgedWithoutBeingDescribed(t *testing.T) {
	prompt := SystemPromptFor([]string{"run_sql", "mcp__acme__list_orders"})
	if !strings.Contains(prompt, "connected its own tools") {
		t.Error("a turn holding tenant MCP tools was not told they exist")
	}
	if strings.Contains(prompt, "mcp__acme__list_orders") {
		t.Error("the catalog named an MCP tool it has no line for")
	}
}
