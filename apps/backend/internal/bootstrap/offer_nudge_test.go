package bootstrap

import (
	"slices"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/app"
)

// T-N6 on the composition side: nudge_agent is the one tool the allowlist does
// not decide, and a turn that may not nudge must get exactly the tools — and so
// exactly the prompt — it got before the tool existed.

func registryWithNudge() []interfaces.Tool {
	return append(registry(), stubTool{"nudge_agent"})
}

func TestAnAgentThatMayNotNudgeGetsExactlyTheToolsItHadBefore(t *testing.T) {
	for _, allowed := range [][]string{nil, {"get_schema", "run_sql"}} {
		got := offerNudge(registryWithNudge(), allowed, false)
		want := filterTools(registry(), allowed)
		if !slices.Equal(toolNames(got), toolNames(want)) {
			t.Errorf("allowlist %v: tools = %v, want %v", allowed, toolNames(got), toolNames(want))
		}
	}
}

// Whatever the allowlist says, in both directions: an unrestricted agent is not
// handed the tool by its empty list, and a restricted one is not denied it by a
// list that has no way to name it.
func TestTheFlagOffersNudgeWhateverTheAllowlistSays(t *testing.T) {
	for _, tc := range []struct {
		allowed []string
		want    []string
	}{
		{nil, append(toolNames(registry()), "nudge_agent")},
		{[]string{"run_sql"}, []string{"run_sql", "nudge_agent"}},
	} {
		if got := toolNames(offerNudge(registryWithNudge(), tc.allowed, true)); !slices.Equal(got, tc.want) {
			t.Errorf("allowlist %v: tools = %v, want %v", tc.allowed, got, tc.want)
		}
	}
}

func TestAnAllowlistNamingNudgeGrantsNothing(t *testing.T) {
	got := toolNames(offerNudge(registryWithNudge(), []string{"run_sql", "nudge_agent"}, false))
	if !slices.Equal(got, []string{"run_sql"}) {
		t.Errorf("tools = %v, want [run_sql] — a name in the allowlist must not stand in for can_nudge", got)
	}
}

// The acceptance line, at the prompt: a turn not offered the tool composes the
// byte-identical prompt a registry without it composes, and a turn offered it
// reads the catalog line and the guideline.
func TestTheNudgeToolAndItsGuidelineReachOnlyATurnOfferedThem(t *testing.T) {
	factory := newAgentFactory(agentFactoryDeps{
		systemPrompt: SystemPromptForTurn, tools: registryWithNudge(), maxIterations: 3,
	})
	llm := &recordingLLM{reply: "ok"}

	single, err := factory(app.AgentSpec{Primary: llm, Light: llm})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if single.GetSystemPrompt() != registryPrompt() {
		t.Error("a turn not offered nudge_agent composed a different prompt from a registry without it")
	}
	if slices.Contains(toolNames(single.GetTools()), "nudge_agent") {
		t.Error("a turn not offered nudge_agent holds it")
	}

	room, err := factory(app.AgentSpec{Primary: llm, Light: llm, Nudge: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	prompt := room.GetSystemPrompt()
	for _, want := range []string{"- nudge_agent:", "A COLLEAGUE IS ASKED, NOT WAITED FOR"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("a turn offered nudge_agent was not told %q", want)
		}
	}
	if !slices.Contains(toolNames(room.GetTools()), "nudge_agent") {
		t.Error("a turn offered nudge_agent does not hold it")
	}
}
