package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// HandOffAgentName is nudge_agent's sibling (T-N7), and the second registered
// tool the allowlist does not decide. See GatedByFlag.
const HandOffAgentName = "hand_off_to_agent"

// HandOffReasonMax caps the reason, in characters. A reason says whose question
// this is, in a sentence; the recipient reads the person's own words for the
// rest, and a reason long enough to need more is a paraphrase of the question —
// the retelling this tool exists to avoid.
const HandOffReasonMax = 300

// HandOffer is what hand_off_to_agent asks of the application (T-N7).
// *app.NudgeService is the production one, for the reason Nudger is declared
// here: the tool package cannot import app.
type HandOffer interface {
	HandOff(ctx context.Context, agent, reason string) string
}

// HandOffAgentTool lets an agent in a room pass the person's question to the
// colleague it belongs to (T-N7): Ops, asked what was written off for SKU 4471
// last quarter, handing the question to Finance, whose ledger holds write-offs.
//
// **A sibling, deliberately not a flag on nudge_agent.** A nudge says *I am
// answering, and I need one fact from you*; the asker keeps the question. A
// hand-off says *this is not mine, you take it*; the asker is done. One tool with
// `handoff: true` is one parameter and two behaviours, and a model gets that
// wrong in the direction that costs a turn — answering half a question it should
// have passed on, or passing on one it could have answered with a lookup. Two
// names, and a description each that carries the difference with an example, is
// the only place the model reads it.
//
// **The model supplies a name and a reason, never the question.** The colleague
// receives the person's words from the handing turn's own payload, so no model
// retells them on the way — a game of telephone between two models is one
// retelling too many.
//
// Everything that decides whether the question may be handed over lives behind
// HandOffer, beside nudge_agent's rules: the same flag, the same room, the same
// conversation budget.
type HandOffAgentTool struct {
	handOffs HandOffer
}

// NewHandOffAgentTool builds the tool. A nil HandOffer is legal, for
// NewNudgeAgentTool's reason.
func NewHandOffAgentTool(h HandOffer) *HandOffAgentTool { return &HandOffAgentTool{handOffs: h} }

func (t *HandOffAgentTool) Name() string { return HandOffAgentName }

func (t *HandOffAgentTool) Description() string {
	return "Pass the person's WHOLE question to ONE other agent in this conversation, because it is not yours " +
		"to answer: its subject belongs to them and their data sources, and nothing in yours answers any real " +
		"part of it. For example: you are Ops, the person asks what was written off for SKU 4471 last quarter, " +
		"and write-offs are booked in Finance's ledger — hand it to Finance. They receive the person's own " +
		"words and your reason, and answer in a turn of their own. Your turn ends with the hand-off: do not " +
		"answer the question yourself, and call no more tools.\n\n" +
		"This is NOT nudge_agent. If you can answer the question and need one fact from a colleague to do it, " +
		"keep the question and ask for the fact with nudge_agent. For example: you are Ops explaining a stock " +
		"discrepancy, and you need to know whether Finance posted a goods-in — that is a nudge, not a hand-off."
}

func (t *HandOffAgentTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"agent": {
			Type:        "string",
			Description: `The colleague's name, exactly as this conversation lists it (e.g. "Finance").`,
			Required:    true,
		},
		"reason": {
			Type: "string",
			Description: fmt.Sprintf("One sentence, in the person's language and under %d characters, saying why "+
				"the question is theirs (e.g. \"Write-offs are booked in Finance's ledger.\"). Do not restate the "+
				"question: they receive the person's own words.", HandOffReasonMax),
			Required: true,
		},
	}
}

func (t *HandOffAgentTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *HandOffAgentTool) Execute(ctx context.Context, args string) (string, error) {
	var in struct {
		Agent  string `json:"agent"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		// No fallback to the raw string, for nudge_agent's reason: a guessed name
		// is the person's question handed to the wrong colleague, and this turn
		// ends believing it was handed to the right one.
		return NudgeRefusal("malformed_arguments",
			`Send {"agent": "<colleague's name>", "reason": "<why the question is theirs>"}.`), nil
	}
	if tenantctx.CompanyID(ctx) == "" {
		return "", fmt.Errorf("no tenant in context: cannot hand a question to another agent")
	}
	if t.handOffs == nil {
		return NudgeRefusal("not_available",
			"Handing a question to another agent is not available here. Answer from your own tools."), nil
	}
	return t.handOffs.HandOff(ctx, in.Agent, in.Reason), nil
}
