package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// NudgeAgentName is the one registered tool an agent's allowlist does not decide
// (T-N6). See GatedByFlag.
const NudgeAgentName = "nudge_agent"

// NudgeQuestionMax caps one question, in characters. A nudge is a question, not
// a transcript: the colleague reads the room's history for context (T-N11), and
// a question that needs more than this is one the person should be asking.
const NudgeQuestionMax = 500

// GatedByFlag reports whether a registered tool is offered by something other
// than the agent's tool allowlist, and so must never be one of its checkboxes.
//
// One tool today. `agents.allowed_tools` is where "which tools may this agent
// call" lives, and an empty list there means *every* tool — so a capability that
// has to default closed (roadmap 09, decision 8) cannot be an entry in it without
// being on for every unrestricted agent. nudge_agent is offered at turn time by
// `agents.can_nudge` and a room of more than one participant, and by nothing
// else: an allowlist naming it grants nothing, and one omitting it takes nothing
// away.
func GatedByFlag(name string) bool { return name == NudgeAgentName }

// Nudger is what nudge_agent asks of the application (T-N6). *app.NudgeService
// is the production one; it is declared here, beside the tool, for the
// import-cycle reason UsageRecorder is.
//
// It returns the tool result itself, refusals included, because every outcome is
// something the asking model reads and acts on — "not in this conversation",
// "already asked", "nobody else can be asked from this message" — and none of
// them is a failure the turn should end on.
type Nudger interface {
	Nudge(ctx context.Context, agent, question string) string
}

// NudgeAgentTool lets an agent in a room ask one of its colleagues a question
// (T-N6): Ops, asked about a stock discrepancy, asking Finance whether a goods-in
// was posted — rather than the person opening a second conversation, asking
// Finance, and carrying the answer back by hand.
//
// **It is an enqueue, not a call.** The question is written into the room and
// queued as an ordinary turn for the colleague, and the tool returns at once. A
// call that waited would spend this turn's wall clock on another agent's whole
// turn — generate_document's video made the same choice for the same reason —
// and the answer arrives as the colleague's own message, in the room, where the
// person reads both.
//
// Everything that decides whether a question may be asked lives behind Nudger:
// who is in the room, whether this agent may ask at all, the credit balance and
// the conversation budget. The tool parses arguments and nothing else, so those
// rules have one home and one set of tests.
type NudgeAgentTool struct {
	nudges Nudger
}

// NewNudgeAgentTool builds the tool. A nil Nudger is legal and is what the API's
// name-only registry passes: the tool still has a name, and answers "not
// available" if it is ever executed there.
func NewNudgeAgentTool(n Nudger) *NudgeAgentTool { return &NudgeAgentTool{nudges: n} }

func (t *NudgeAgentTool) Name() string { return NudgeAgentName }

func (t *NudgeAgentTool) Description() string {
	return "Ask ONE other agent in this conversation ONE short question, when answering needs a fact or " +
		"figure only their data sources hold. Name the agent exactly as this conversation lists them. " +
		"It does NOT wait for the answer: the question is posted to the conversation and their reply " +
		"appears after yours. So finish your own answer without it, say that you asked, and never state " +
		"the figure you asked them for. Check with your own tools first — do not ask a colleague something " +
		"you can look up — and never ask the same colleague the same thing twice."
}

func (t *NudgeAgentTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"agent": {
			Type:        "string",
			Description: `The colleague's name, exactly as this conversation lists it (e.g. "Finance").`,
			Required:    true,
		},
		"question": {
			Type: "string",
			Description: fmt.Sprintf("The one question, in the person's language, under %d characters. "+
				"Include what they need in order to answer it — the SKU, the date range — because they "+
				"cannot see your tool results.", NudgeQuestionMax),
			Required: true,
		},
	}
}

func (t *NudgeAgentTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *NudgeAgentTool) Execute(ctx context.Context, args string) (string, error) {
	var in struct {
		Agent    string `json:"agent"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		// No fallback to the raw string, unlike run_sql and ask_clarification.
		// This tool has two arguments and neither can be recovered from the
		// other, so a guess would be a question sent to nobody or to the wrong
		// colleague. A refusal costs one iteration; a misdirected question costs
		// a colleague's whole turn.
		return NudgeRefusal("malformed_arguments",
			`Send {"agent": "<colleague's name>", "question": "<one question>"}.`), nil
	}
	if tenantctx.CompanyID(ctx) == "" {
		return "", fmt.Errorf("no tenant in context: cannot ask another agent")
	}
	if t.nudges == nil {
		return NudgeRefusal("not_available",
			"Asking another agent is not available here. Answer from your own tools."), nil
	}
	return t.nudges.Nudge(ctx, in.Agent, in.Question), nil
}

// NudgeRefusal is the shape every nudge_agent refusal takes, whichever side
// writes it.
//
// `error` names what went wrong, and it is load-bearing rather than descriptive:
// it keeps the call out of the ones that succeeded (agentbudget's
// resultCarriesError), so a reply saying "I asked Finance" after one of these is
// an unevidenced claim (T-Q13). `note` says what to do instead, naming the
// colleagues who can be asked when there are any — load_skill's refusal habit.
// `row_count: 0` for load_skill's reason too: nothing here is evidence for a
// figure.
//
// The conversation budget's refusals are not this shape. They are
// agentbudget.Verdict.ToolResult, which carries `budget_exhausted` so the three
// readers that already treat that key as a refusal need no change.
func NudgeRefusal(code, note string) string {
	return encodeUnescaped(map[string]any{
		"asked":     false,
		"error":     code,
		"note":      note,
		"row_count": 0,
	})
}
