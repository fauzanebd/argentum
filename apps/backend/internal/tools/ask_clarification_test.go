package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentbudget"
)

func askResult(t *testing.T, args string) map[string]interface{} {
	t.Helper()
	out, err := NewAskClarificationTool().Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute(%s): %v", args, err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("result is not JSON: %v (%s)", err, out)
	}
	return got
}

func TestAskClarificationReturnsTheQuestionAndEndsTheTurn(t *testing.T) {
	got := askResult(t, `{"question":"Which source do you mean — the CRM or the HRIS?","options":["CRM","HRIS"]}`)

	if got["status"] != "awaiting_user" {
		t.Errorf("status = %v, want awaiting_user", got["status"])
	}
	if q, _ := got["question"].(string); !strings.Contains(q, "CRM") {
		t.Errorf("question did not survive: %v", got["question"])
	}
	// The instruction is the whole mechanism. Without it the ordinary shape of
	// a tool-calling loop is to take the result and keep going, producing the
	// question followed by the guess it was meant to replace.
	instr, _ := got["instruction"].(string)
	if !strings.Contains(instr, "turn ends here") {
		t.Errorf("result does not tell the model to stop: %q", instr)
	}
	if !strings.Contains(instr, "no further tool calls") {
		t.Errorf("result does not forbid further tool calls: %q", instr)
	}
}

// The model sends a bare string when its JSON goes wrong. run_sql already
// accepts one; refusing here would turn a formatting slip into a turn that
// guesses instead of asking, which is the behaviour this tool exists to stop.
func TestAskClarificationAcceptsABareString(t *testing.T) {
	got := askResult(t, "Which quarter did you mean?")
	if q, _ := got["question"].(string); q != "Which quarter did you mean?" {
		t.Errorf("question = %q, want the bare string", q)
	}
}

func TestAskClarificationRefusesAnEmptyQuestion(t *testing.T) {
	for _, args := range []string{`{"question":"   "}`, `{}`, "", "   "} {
		if _, err := NewAskClarificationTool().Execute(context.Background(), args); err == nil {
			t.Errorf("Execute(%q) accepted a question with no question in it", args)
		}
	}
}

// Not a data tool, and this is load-bearing rather than incidental: a turn
// that ends in a question states no figure, so it has nothing to ground.
// Counting a question as evidence is the one way this tool could make
// fabrication easier instead of harder.
func TestAskClarificationIsNotEvidenceForAFigure(t *testing.T) {
	if agentbudget.IsDataTool("ask_clarification") {
		t.Error("ask_clarification counts as evidence for a stated figure; it must not")
	}
	if agentbudget.IsDeliverableTool("ask_clarification") {
		t.Error("ask_clarification holds a reserved call past exhaustion; it must not")
	}
}

func TestAskClarificationIsRegistered(t *testing.T) {
	names := AllNames()
	found := false
	for _, n := range names {
		if n == "ask_clarification" {
			found = true
		}
	}
	if !found {
		t.Errorf("ask_clarification is not in the registry: %v", names)
	}
}

// A tool with a dependency can be left out of a deployment. This one has none,
// deliberately — the alternative to asking is always a tool call, so a
// deployment where the agent cannot ask is a deployment where it guesses.
func TestAskClarificationRegistersWithNoDependencies(t *testing.T) {
	names := Names(Registry(RegistryDeps{}))
	for _, n := range names {
		if n == "ask_clarification" {
			return
		}
	}
	t.Errorf("ask_clarification absent from a registry built with no dependencies: %v", names)
}
