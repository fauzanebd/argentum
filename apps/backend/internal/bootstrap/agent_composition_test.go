package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/app"
)

// T-S2, on the composition side: an agent is persona + tools, and this is
// where both reach the turn. The source allowlist is enforced elsewhere (in
// the tools, where it has to be) and tested there.

type stubTool struct{ name string }

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "stub" }
func (s stubTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{}
}
func (s stubTool) Run(context.Context, string) (string, error)     { return "{}", nil }
func (s stubTool) Execute(context.Context, string) (string, error) { return "{}", nil }

func registry() []interfaces.Tool {
	return []interfaces.Tool{
		stubTool{"list_sources"}, stubTool{"get_schema"}, stubTool{"run_sql"},
		stubTool{"create_dashboard"}, stubTool{"generate_document"},
	}
}

func toolNames(ts []interfaces.Tool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

func compositionFactory() app.AgentFactory {
	return newAgentFactory(agentFactoryDeps{
		systemPrompt:  SystemPromptForTurn,
		tools:         registry(),
		maxIterations: 3,
	})
}

// registryPrompt is what "the shared prompt" means to these tests now that the
// catalog is composed from the turn's own tools: the prompt for an agent
// holding everything registry() has. An unrestricted agent gets exactly this,
// and it is still the prefix every addendum is appended to.
func registryPrompt() string { return SystemPromptFor(toolNames(registry())) }

// fileTurnPrompt is the same prefix for a turn carrying a directive. The two
// differ by the chart guidelines, which a file turn drops on purpose — so a
// test asserting "the shared prompt is still the prefix" for an agent that
// holds create_dashboard has to compare against this one, or it is asserting
// that T-A2b's fix does not happen.
func fileTurnPrompt() string {
	return SystemPromptForTurn(toolNames(registry()), PromptTurn{FileDeliverable: true})
}

const persona = "You serve the finance team. Prefer margin over revenue."

// Locked decision 3: the persona is an addendum. The shared prompt stays the
// prefix — it carries the SQL rules, the anti-fabrication language T-16 fought
// for and the formatting contract, and on Anthropic it is also every turn's
// cache key.
func TestThePersonaIsAppendedAndNeverReplaces(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	agent, err := compositionFactory()(app.AgentSpec{Primary: llm, Light: llm, Persona: persona})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	prompt := agent.GetSystemPrompt()
	if !strings.HasPrefix(prompt, registryPrompt()) {
		t.Error("the shared system prompt is no longer the prefix")
	}
	if !strings.Contains(prompt, persona) {
		t.Error("the persona did not reach the system prompt")
	}
}

// The persona is customer input landing in the most privileged part of the
// request. The frame is what keeps "ignore the rules above" from reading as
// something we wrote.
func TestThePersonaIsFramedAsSubordinate(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	agent, err := compositionFactory()(app.AgentSpec{Primary: llm, Light: llm, Persona: persona})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	prompt := agent.GetSystemPrompt()
	frame := strings.Index(prompt, "cannot override")
	if frame < 0 {
		t.Fatal("the persona is not framed as subordinate to the shared prompt")
	}
	if at := strings.Index(prompt, persona); at < frame {
		t.Error("the persona text appears before its frame; the frame has to be read first")
	}
}

// Order, and the reason for it: the report directive is Argentum's own
// instruction for this one turn, so it is the last thing the model reads.
func TestPersonaThenDirective(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	agent, err := compositionFactory()(app.AgentSpec{
		Primary: llm, Light: llm, Persona: persona, SystemAddendum: reportDirective(),
	})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	prompt := agent.GetSystemPrompt()
	personaAt := strings.Index(prompt, persona)
	directiveAt := strings.Index(prompt, "[REPORT REQUEST")
	switch {
	case personaAt < 0 || directiveAt < 0:
		t.Fatalf("persona at %d, directive at %d; both should be present", personaAt, directiveAt)
	case directiveAt < personaAt:
		t.Error("the turn directive landed before the persona; shared prompt → persona → directive")
	}
}

// An agent with an empty allowlist gets the whole registry — the backfilled
// default every existing tenant received, behaving exactly as it did before
// this ticket.
func TestAnEmptyToolAllowlistIsTheWholeRegistry(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	agent, err := compositionFactory()(app.AgentSpec{Primary: llm, Light: llm})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	if got := len(agent.GetTools()); got != len(registry()) {
		t.Errorf("tools = %d, want the whole registry (%d)", got, len(registry()))
	}
}

// The fourth acceptance item, at the only layer that can make it true: an
// agent without create_dashboard is not *asked* not to use it, it is not given
// it. Three direct requests cannot produce a dashboard from a tool the model
// was never handed.
func TestAnAllowlistedAgentIsGivenOnlyItsTools(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	agent, err := compositionFactory()(app.AgentSpec{
		Primary: llm, Light: llm, ToolNames: []string{"run_sql", "get_schema"},
	})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	got := toolNames(agent.GetTools())
	if len(got) != 2 {
		t.Fatalf("tools = %v, want exactly run_sql and get_schema", got)
	}
	for _, banned := range []string{"create_dashboard", "generate_document", "list_sources"} {
		if strings.Contains(strings.Join(got, ","), banned) {
			t.Errorf("tools = %v, want %q excluded", got, banned)
		}
	}
}

// The filter runs over the slice the factory holds, which the stack has
// already wrapped in the budget guard and the audit recorder. Identity, not
// equality: a filter that rebuilt tools would silently drop both wrappers,
// which is the failure T-05's decorator-over-the-registry exists to prevent.
func TestTheFilterPreservesTheWrappedInstances(t *testing.T) {
	all := registry()
	got := filterTools(all, []string{"run_sql"})
	if len(got) != 1 {
		t.Fatalf("filterTools = %v, want one tool", toolNames(got))
	}
	if got[0] != all[2] {
		t.Error("filterTools returned a different instance than the registry's; wrappers would be lost")
	}
}

// An allowlist naming tools this deployment does not have leaves the turn with
// none. The safe reading of "may use exactly these" is never "may use all".
func TestAnAllowlistMatchingNothingLeavesNoTools(t *testing.T) {
	if got := filterTools(registry(), []string{"query_metric"}); len(got) != 0 {
		t.Errorf("filterTools = %v, want nothing", toolNames(got))
	}
}
