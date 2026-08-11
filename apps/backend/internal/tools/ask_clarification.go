package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
)

// AskClarificationTool lets the agent stop and ask, as an action rather than
// as an instruction (T-Q4).
//
// **Why a tool and not a guideline.** The system prompt has told the agent to
// ask first since T-16 — *"if the question doesn't clearly map to one source,
// ASK the user which source they mean BEFORE running SQL"* — and the eval has
// measured what that instruction is worth. `ambiguous-headcount` passed under
// a three-iteration cap and failed on all three runs once T-16 gave the turn
// room, with the baseline recording the mechanism plainly: *"under a
// 3-iteration cap the agent could not afford to survey two sources, so 'ask
// first' was being enforced by poverty rather than by judgement. Given room,
// it prefers to act."* T-16 then sharpened the guideline to say which rule
// wins, and the model ignored it.
//
// The reason is structural rather than a matter of wording. Every other option
// the model has at that moment is a tool call — a named, callable thing sitting
// in the same list — and asking was a sentence in a prompt competing against
// them. This puts asking in the list.
//
// **What it does not do.** It performs nothing and reaches nothing. Its whole
// effect is the result it hands back, which tells the model the question has
// been accepted and that the turn ends by putting it to the user. That is the
// same mechanism the budget guard uses to stop a turn mid-loop, and for the
// reason agentbudget.Guard gives: a tool result is the only message that
// reaches the model from inside the provider's tool-calling loop.
//
// It is deliberately not a data tool (agentbudget.dataTools). A turn that ends
// in a question states no figure, so it has nothing to ground — and counting a
// question as evidence would be the one way this tool could make fabrication
// easier rather than harder.
type AskClarificationTool struct{}

func NewAskClarificationTool() *AskClarificationTool { return &AskClarificationTool{} }

func (t *AskClarificationTool) Name() string { return "ask_clarification" }

func (t *AskClarificationTool) Description() string {
	return "Ask the user a question and end the turn, when their request is ambiguous enough that " +
		"guessing would produce a confidently wrong answer. Use this INSTEAD of picking a reading and " +
		"running with it. Typical cases: the question could be answered from two sources holding " +
		"different subjects (a CRM and an HRIS both have 'records'); a column name means two things " +
		"(a catalogue price and a charged price); a date range is genuinely unclear; the user named a " +
		"metric this workspace has not defined. Do NOT use it for a question you can answer, for a " +
		"detail you can look up with get_schema, or to avoid work — an unnecessary question is as " +
		"unhelpful as a wrong answer. Ask ONE question, and offer the concrete options when you know them."
}

func (t *AskClarificationTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"question": {
			Type:        "string",
			Description: "The single question to put to the user, in their own language. Be specific about what you need to know and why it changes the answer.",
			Required:    true,
		},
		"options": {
			Type:        "array",
			Description: "The concrete choices, when you know them (e.g. the two source labels, or the two readings of a column). Offering options is what turns a clarifying question into one the user can answer in a word.",
			Required:    false,
			Items:       &interfaces.ParameterSpec{Type: "string"},
		},
	}
}

func (t *AskClarificationTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

type askClarificationArgs struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

func (t *AskClarificationTool) Execute(_ context.Context, args string) (string, error) {
	var in askClarificationArgs
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		// A bare string is what the model sends when its JSON goes wrong, and
		// run_sql already accepts one for the same reason. Refusing here would
		// turn a formatting slip into a turn that guesses instead of asking,
		// which is the exact behaviour this tool exists to prevent.
		in.Question = strings.TrimSpace(args)
	}
	in.Question = strings.TrimSpace(in.Question)
	if in.Question == "" {
		return "", fmt.Errorf("question is required: state what you need the user to tell you")
	}

	// The result is an instruction, not data. It is phrased at the model
	// because the model is its only reader, and it is explicit about ending the
	// turn: without that, the ordinary shape of a tool-calling loop is to take
	// the result and keep going, which would produce a question followed by the
	// guess it was supposed to replace.
	out, _ := json.Marshal(map[string]interface{}{
		"status":   "awaiting_user",
		"question": in.Question,
		"options":  in.Options,
		"instruction": "Your turn ends here. Reply with this question to the user, in their language, " +
			"and nothing else — no partial answer, no figures, and no further tool calls. " +
			"Offer the options if any are listed. Wait for their reply before doing any work.",
	})
	return string(out), nil
}
