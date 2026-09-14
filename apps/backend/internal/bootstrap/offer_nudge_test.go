package bootstrap

import (
	"slices"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/app"
)

// T-N6 and T-N7 on the composition side: nudge_agent and hand_off_to_agent are
// the two tools the allowlist does not decide, and a turn that may use neither
// must get exactly the tools — and so exactly the prompt — it got before they
// existed.

func registryWithNudge() []interfaces.Tool {
	return append(registry(), stubTool{"nudge_agent"}, stubTool{"hand_off_to_agent"})
}

func TestAnAgentThatMayNotNudgeGetsExactlyTheToolsItHadBefore(t *testing.T) {
	for _, allowed := range [][]string{nil, {"get_schema", "run_sql"}} {
		got := offerRoomTools(registryWithNudge(), allowed, false, false)
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
		if got := toolNames(offerRoomTools(registryWithNudge(), tc.allowed, true, false)); !slices.Equal(got, tc.want) {
			t.Errorf("allowlist %v: tools = %v, want %v", tc.allowed, got, tc.want)
		}
	}
}

func TestAnAllowlistNamingNudgeGrantsNothing(t *testing.T) {
	got := toolNames(offerRoomTools(registryWithNudge(),
		[]string{"run_sql", "nudge_agent", "hand_off_to_agent"}, false, false))
	if !slices.Equal(got, []string{"run_sql"}) {
		t.Errorf("tools = %v, want [run_sql] — a name in the allowlist must not stand in for can_nudge", got)
	}
}

// T-N7: the hand-off rides with nudge_agent and never alone. Its description and
// its guideline both send the model to nudge_agent for the case that is not a
// hand-off, so a turn holding one without the other would be told to call a tool
// it does not have.
func TestTheHandOffIsOfferedOnlyBesideNudge(t *testing.T) {
	for _, tc := range []struct {
		name           string
		nudge, handOff bool
		want           []string
	}{
		{"both", true, true, []string{"run_sql", "nudge_agent", "hand_off_to_agent"}},
		{"a colleague's question: nudge only", true, false, []string{"run_sql", "nudge_agent"}},
		{"hand-off without nudge", false, true, []string{"run_sql"}},
	} {
		got := toolNames(offerRoomTools(registryWithNudge(), []string{"run_sql"}, tc.nudge, tc.handOff))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: tools = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The acceptance line, at the prompt: a turn not offered the tools composes the
// byte-identical prompt a registry without them composes, and a turn offered
// them reads each catalog line and each guideline — the hand-off's only when it
// holds the hand-off.
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
		t.Error("a turn not offered the room's tools composed a different prompt from a registry without them")
	}
	for _, name := range []string{"nudge_agent", "hand_off_to_agent"} {
		if slices.Contains(toolNames(single.GetTools()), name) {
			t.Errorf("a turn not offered %s holds it", name)
		}
	}

	asked, err := factory(app.AgentSpec{Primary: llm, Light: llm, Nudge: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if p := asked.GetSystemPrompt(); strings.Contains(p, "hand_off_to_agent") || strings.Contains(p, "HANDED OFF") {
		t.Error("a turn offered nudge_agent alone was told about hand_off_to_agent")
	}

	room, err := factory(app.AgentSpec{Primary: llm, Light: llm, Nudge: true, HandOff: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	prompt := room.GetSystemPrompt()
	for _, want := range []string{
		"- nudge_agent:", "A COLLEAGUE IS ASKED, NOT WAITED FOR",
		"- hand_off_to_agent:", "A QUESTION THAT IS NOT YOURS IS HANDED OFF, NOT HALF-ANSWERED",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("a turn offered the room's tools was not told %q", want)
		}
	}
	for _, name := range []string{"nudge_agent", "hand_off_to_agent"} {
		if !slices.Contains(toolNames(room.GetTools()), name) {
			t.Errorf("a turn offered %s does not hold it", name)
		}
	}
}
