package agentbudget

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The mid-turn checkpoint notice.
//
// Everything else in this package speaks to the model once: at exhaustion,
// through a refusal. That is one message too late to change how the turn is
// spent. The model plans as though the budget were infinite, spends its
// iterations exploring, and finds out it has run out at the moment it is told
// it may not run the query that would have answered the question — which is
// finding Q-5 with a larger number in it. Raising the ceiling moves that cliff
// rather than removing it.
//
// So the budget says something while there is still budget left to spend
// differently. The wording is load-bearing in one specific way: it must not
// read as "stop". A notice that sounds like an instruction to wrap up makes
// the model end the turn early, which trades a truncated answer for an
// abandoned one — worse, because the budget was not actually spent. Hence the
// final clause, which stays in every variant of this text.

// CheckpointInstruction is the half of the notice that does not change.
const CheckpointInstruction = "Spend what is left on the call that actually answers the question, " +
	"not on more exploration. Keep going: do NOT stop, and do NOT write your final reply, " +
	"solely because of this notice — when the budget is actually spent you will be told so, " +
	"and asked for your final reply then."

// checkpointField is the key the notice is delivered under. A field on the
// result rather than prose appended after it, because every parser downstream
// of a tool — the digest, the row counter, the audit log — reads the result as
// JSON and an unparseable one is worse than an unwarned model.
const checkpointField = "budget_checkpoint"

// Checkpoint returns the one-shot notice for this turn, or "" for every call
// that should not carry one.
//
// Called after the tool has run, so the call that crosses the line carries the
// notice on its own result — the earliest message the model can read it in.
func (t *Tracker) Checkpoint(ctx context.Context) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// A turn that is already exhausted has been told more than this notice
	// would tell it, and its tool loop is over either way.
	if t.warned || t.exhausted {
		return ""
	}
	ratio := t.budget.WarnRatio
	if ratio <= 0 || ratio >= 1 {
		return ""
	}
	spent, crossed := t.tightestLocked(ctx, ratio)
	if !crossed {
		return ""
	}
	t.warned = true
	return "You have used " + spent + ". " + CheckpointInstruction
}

// tightestLocked names the dimension furthest through its ceiling, and reports
// whether it has reached ratio.
//
// **No figure here may reach 1000, and the token dimension is described
// without figures for that reason alone.** A notice rides out on a tool result,
// and for `search_documents` that result is read for figures the reply is then
// allowed to quote (guardrails.CollectNumbersInProse). That collection ignores
// anything under 1000 — which every iteration, tool-call and second count here
// is, and which a token total is not. Writing "140000 of 200000 tokens" would
// hand the model two numbers this package's own fabrication guard would then
// accept as retrieved evidence.
func (t *Tracker) tightestLocked(ctx context.Context, ratio float64) (string, bool) {
	type dimension struct {
		used, max float64
		phrase    string
	}

	iterations := iterationsUsed(ctx)
	elapsed := int(time.Since(t.start).Seconds())
	wall := int(t.budget.Wall.Seconds())

	dimensions := []dimension{{
		used: float64(iterations), max: float64(t.budget.MaxIterations),
		phrase: fmt.Sprintf("%d of %d tool-calling iterations", iterations, t.budget.MaxIterations),
	}, {
		used: float64(t.toolCalls), max: float64(t.budget.MaxToolCalls),
		phrase: fmt.Sprintf("%d of %d tool calls", t.toolCalls, t.budget.MaxToolCalls),
	}, {
		used: float64(elapsed), max: float64(wall),
		phrase: fmt.Sprintf("%d of this turn's %d seconds", elapsed, wall),
	}, {
		used: float64(tokensUsed(ctx)), max: float64(t.budget.MaxTokens),
		phrase: "most of this turn's token budget",
	}}

	var phrase string
	best := ratio
	for _, d := range dimensions {
		if d.max <= 0 {
			continue
		}
		if frac := d.used / d.max; frac >= best {
			best, phrase = frac, d.phrase
		}
	}
	return phrase, phrase != ""
}

// WithCheckpoint returns result carrying notice, for the model to read.
//
// The notice is spliced into the existing JSON object rather than added by
// re-marshalling it: a round trip through map[string]any renders every integer
// as a float, and a turn whose order id came back as 1e+06 has been corrupted
// by its own budget warning. Splicing after the opening brace leaves every
// other byte of the tool's output exactly as the tool wrote it.
//
// A result that is not a JSON object gets the notice appended as text, which is
// the right fallback for the shape it implies — a bare string.
func WithCheckpoint(result, notice string) string {
	if notice == "" {
		return result
	}
	encoded, err := json.Marshal(notice)
	if err != nil {
		return result
	}
	field := `"` + checkpointField + `":` + string(encoded)

	trimmed := strings.TrimSpace(result)
	if !strings.HasPrefix(trimmed, "{") {
		return strings.TrimRight(result, "\n") + "\n\n" + notice
	}
	rest := trimmed[1:]
	if strings.TrimSpace(rest) == "}" {
		return "{" + field + "}"
	}
	return "{" + field + "," + rest
}
