package app

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tracing"
)

// Next-step suggestions: after the agent answers, it says what is worth asking
// next (T-Q10).
//
// Every turn in this product ends at a full stop. The person reading an answer
// is the one least equipped to know what this product can be asked next, and the
// agent is the one that has just discovered what the data supports.
// docs/coverage/feature-coverage.md records the same failure from the other
// side — an agent told it held nine tools recommended work it could not do; the
// inverse is an agent that holds ten and recommends nothing.
//
// It is also the first instrument this product has for what customers actually
// want next, as opposed to what forty synthetic eval questions ask. That is what
// the pick-rate in T-U13's `suggestion_picks` measures, and it is the number
// that decides whether this feature keeps its space under every answer.

const (
	// UsageFeatureNextSteps labels this pass's spend, beside
	// UsageFeatureAgentGenerate and UsageFeatureBusinessInference.
	//
	// **This is the metering, and it is a deviation from the ticket worth
	// stating.** T-Q10 asks for "one usage_event per pass, kind next_steps".
	// There already is one: the call goes through the tenant's MeteredLLM, which
	// records an `llm_call` with the real token counts, and UsageService.append
	// stamps this label on it. Adding a second `next_steps` event beside it would
	// either double-count the cost or record a zero-cost row next to the real
	// one — and a free-looking pass in the usage table is the shape of exactly
	// the mistake C-2 was. One event, correctly priced, separable by feature.
	UsageFeatureNextSteps = "next_steps"

	// nextStepsMax is how many chips an answer may carry. Three, because the
	// surface is a row under a message and a fourth wraps.
	nextStepsMax = 3
	// nextStepsLabelMax is the chip's text budget.
	nextStepsLabelMax = 48
	// nextStepsTimeout bounds the pass. It sits in front of the `final` event,
	// so this is latency the browser waits through — see the trade in
	// suggestNextSteps' comment.
	nextStepsTimeout = 5 * time.Second
)

// numericToken matches a run of digits with the separators a figure is written
// with. restatesAFigure decides what it means.
var numericToken = regexp.MustCompile(`[0-9][0-9.,]*`)

// restatesAFigure reports whether a suggestion contains a number that reads as a
// RESULT rather than as a period.
//
// **This is a deliberate narrowing of T-Q10's rule, and the reason is a test
// case rather than a preference.** The ticket says to drop any step containing a
// digit run of 4 or more. A year is a digit run of four, so that rule deletes
// "compare with 2024" and "how did Q4 2025 finish" — which are among the most
// useful suggestions this feature can make, and the ones a business user is most
// likely to click. The rule's actual job is to stop the agent restating a figure
// it computed, so it draws the line where a figure is distinguishable from a
// period:
//
//   - a grouped or decimal number (1,234 / 1.234 / 3.5) is a figure. Nothing
//     writes a period that way.
//   - five or more digits is a figure. No period is written with five.
//   - exactly four digits inside 1900–2099 is a year. Anything else with four
//     digits — an id, an amount, a count — is a figure.
//
// Cheap enforcement of the grounding rule, as the ticket says; the eval category
// is the real one.
func restatesAFigure(s string) bool {
	for _, tok := range numericToken.FindAllString(s, -1) {
		tok = strings.TrimRight(tok, ".,")
		if strings.ContainsAny(tok, ".,") {
			return true // grouped or decimal: a figure, never a period
		}
		switch n := len(tok); {
		case n >= 5:
			return true
		case n == 4:
			// A plausible year is a period. Everything else four digits long is a
			// number the agent got from somewhere.
			if tok < "1900" || tok > "2099" {
				return true
			}
		}
	}
	return false
}

// BudgetChecker is redeclared nowhere: ChatRunner borrows chat_enqueuer.go's.
// The suggester needs it for one branch — an answer must never be delayed by a
// suggestion when the tenant is nearly out of credit.

// WithNextSteps switches the suggestion pass on and gives it the credit check it
// defers to (T-Q10).
//
// Off is the default for a runner nobody configures, which is every test and the
// eval harness: the pass changes what a turn costs and how long it takes, so a
// caller that has not said yes gets the turn it had before this ticket, byte for
// byte. bootstrap says yes unless NEXT_STEPS_ENABLED=false.
func (r *ChatRunner) WithNextSteps(enabled bool, budget BudgetChecker) *ChatRunner {
	r.nextSteps = enabled
	r.nextStepsBudget = budget
	return r
}

// suggestNextSteps asks the model what is worth asking next.
//
// **Where it runs, and the latency trade, stated so nobody re-litigates it
// silently.** It runs between the empty-reply rescue and completeWith — the same
// post-turn chain the fabrication gate, the grounding check and the output rules
// already run in — which delays the `final` event by the length of the pass. The
// alternative is to publish `final`, run the pass, then a second event and an
// UPDATE of the message row; that needs a MessageRepository.Update which does
// not exist and a second event type every consumer (dashboard, widget, /v1, MCP)
// would have to learn. Take the latency. Revisit if the measured p95 of the pass
// exceeds 1s.
//
// Every failure is the same outcome: no suggestions, and a turn otherwise
// unchanged. A parse error, a timeout, an empty array, an unreachable model —
// none of them is worth failing an answer the user already has.
func (r *ChatRunner) suggestNextSteps(
	ctx context.Context, p queue.ChatRunPayload,
	answer string, held []string, tools []string, replaced bool,
) []domain.NextStep {
	if !r.nextSteps || r.llmCache == nil {
		return nil
	}
	if reason := skipSuggestions(p, answer, tools, replaced); reason != "" {
		logrus.WithFields(logrus.Fields{
			"company_id": p.CompanyID,
			"thread_id":  p.ThreadID,
			"reason":     reason,
		}).Debug("next-step suggestions skipped")
		return nil
	}
	// An answer must never be delayed by a suggestion when the tenant is nearly
	// out of credit. Fail-open on a lookup error, matching CheckBudget's own rule
	// everywhere else: a billing check that cannot be made is not a refusal.
	if r.nextStepsBudget != nil {
		if st, err := r.nextStepsBudget.CheckBudget(ctx, p.CompanyID); err != nil {
			logrus.WithError(err).Debug("next-steps budget check failed; suggesting anyway")
		} else if st.Verdict != BudgetOK {
			return nil
		}
	}

	ctx, span := tracing.Step(ctx, "next_steps")
	defer span.End()

	// Labelled before the call, so the LLM event MeteredLLM records carries it.
	ctx = WithUsageFeature(ctx, UsageFeatureNextSteps)
	ctx, cancel := context.WithTimeout(ctx, nextStepsTimeout)
	defer cancel()

	llm, _, err := r.llmCache.For(ctx, p.CompanyID, domain.LLMTierLight)
	if err != nil {
		logrus.WithError(err).Debug("next-steps: no light LLM; skipping")
		return nil
	}

	raw, err := llm.Generate(ctx, nextStepsPrompt(p.Message, answer, held, tools),
		interfaces.WithSystemMessage(nextStepsSystem),
		interfaces.WithTemperature(0.3),
	)
	if err != nil {
		logrus.WithError(err).WithField("thread_id", p.ThreadID).
			Info("next-steps pass failed; the answer is unchanged")
		return nil
	}

	steps := narrowSteps(parseNextSteps(raw), held)
	if len(steps) == 0 {
		return nil
	}
	logrus.WithFields(logrus.Fields{
		"company_id": p.CompanyID,
		"thread_id":  p.ThreadID,
		"steps":      len(steps),
	}).Debug("next-step suggestions attached to the answer")
	return steps
}

// skipSuggestions names the turns that get no chips, and returns why.
//
// Each of these is a turn where a row of buttons is worse than an empty space,
// not merely unnecessary.
func skipSuggestions(p queue.ChatRunPayload, answer string, tools []string, replaced bool) string {
	if strings.TrimSpace(answer) == "" {
		return "empty answer"
	}
	// The agent has already asked the user a question (T-Q4). Chips would compete
	// with it — three alternatives beside "which source did you mean?" is a
	// question and three ways to ignore it.
	if slices.Contains(tools, "ask_clarification") {
		return "the turn asked a clarifying question"
	}
	// The reply is the fabrication gate's or the empty-reply rescue's, not the
	// agent's. Suggesting what to do next on top of "I could not complete that"
	// is the product being cheerful about its own failure.
	if replaced {
		return "the reply was replaced by a guardrail"
	}
	// A file was the deliverable (T-A2). The caller is a script holding an HTTP
	// response, and there is no composer to fill.
	if p.Directive != "" || p.APIReportID != "" {
		return "report turn"
	}
	// Nobody is reading this when it arrives, so there is nothing to click and
	// the spend buys a row in a table.
	if p.ScheduledTaskID != "" || p.WatcherEventID != "" {
		return "unattended turn"
	}
	return ""
}

const nextStepsSystem = `You suggest what a business user might usefully ask next, immediately after they have been given an answer by a data analyst.
You return STRICT JSON and nothing else. No prose, no markdown, no code fence.`

// nextStepsPrompt is deliberately given no rows and no figures.
//
// A suggestion is text this product puts in the user's mouth, and a confident
// wrong one is a worse failure than a missing one. The prompt gets the question,
// the answer text, what the turn did and what it *could* do — enough to suggest a
// direction, and not enough to assert a result.
func nextStepsPrompt(question, answer string, held, called []string) string {
	var b strings.Builder
	b.WriteString("The user asked:\n")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\nThe analyst answered:\n")
	b.WriteString(clip(strings.TrimSpace(answer), 4000))
	b.WriteString("\n\nThe analyst CAN do these things: ")
	b.WriteString(strings.Join(held, ", "))
	if len(called) > 0 {
		b.WriteString("\nThis turn used: ")
		b.WriteString(strings.Join(called, ", "))
	}
	b.WriteString(`

Suggest at most 3 things worth asking next. Rules:
- Each must be answerable with the capabilities listed above. Do not suggest anything needing a capability the analyst does not have.
- Write the prompt as the user would type it, in the SAME LANGUAGE the user wrote in.
- Never state or repeat a figure, a total or a date range's result. A suggestion names a dimension, a period or a metric; it never asserts a number.
- Mark exactly one as recommended: the one you would do next, with a short reason.
- Suggest nothing rather than something generic. An empty list is a correct answer.

Return JSON of this exact shape:
{"steps":[{"label":"Break down by region","prompt":"Break that down by region","recommended":true,"why":"the totals hide where the growth came from"}]}`)
	return b.String()
}

// parseNextSteps reads the model's JSON, tolerating the fence it was told not to
// write. Anything unreadable is an empty list, which is the same outcome as a
// timeout and is handled by the caller in one branch.
func parseNextSteps(raw string) []domain.NextStep {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexByte(s, '\n'); j >= 0 {
			s = s[j+1:]
		}
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	// A model that ignores "no prose" writes a sentence and then the object.
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:]
	}
	var payload struct {
		Steps []domain.NextStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &payload); err != nil {
		logrus.WithError(err).Debug("next-steps: unparseable reply; suggesting nothing")
		return nil
	}
	return payload.Steps
}

// narrowSteps applies the rules server-side, because the model is not trusted
// with them.
//
// Every rule here was asked for in the prompt as well. That duplication is the
// point: a prompt states an intention and this enforces it, and the failure the
// enforcement covers — a suggestion needing a tool this agent does not hold, a
// figure restated in a chip — is one the user sees and cannot tell is a bug.
func narrowSteps(in []domain.NextStep, held []string) []domain.NextStep {
	out := make([]domain.NextStep, 0, nextStepsMax)
	recommended := false
	for _, s := range in {
		s.Label = strings.TrimSpace(s.Label)
		s.Prompt = strings.TrimSpace(s.Prompt)
		s.Why = strings.TrimSpace(s.Why)
		if s.Label == "" || s.Prompt == "" {
			continue
		}
		// A suggestion is not a place to restate a figure. The cheap enforcement
		// of the grounding rule — the eval case is the real one.
		if restatesAFigure(s.Label) || restatesAFigure(s.Prompt) {
			continue
		}
		if needsMissingTool(s.Prompt, held) {
			continue
		}
		s.Label = clip(s.Label, nextStepsLabelMax)
		// At most one recommended, first wins. A model asked for "exactly one"
		// returns two often enough that this has to be applied rather than asked
		// for, and keeping the first is the only choice that does not need an
		// opinion about which is better.
		if s.Recommended {
			if recommended {
				s.Recommended = false
			} else {
				recommended = true
			}
		}
		if !s.Recommended {
			// A reason renders on the recommended chip only, so carrying one on the
			// others is bytes on the wire and a tooltip nobody sees.
			s.Why = ""
		}
		out = append(out, s)
		if len(out) == nextStepsMax {
			break
		}
	}
	return out
}

// suggestionTools maps a phrase a suggestion is likely to use onto the tool it
// would need. Deliberately small and deliberately about *deliverables*: those
// are the suggestions that fail visibly, because a user who clicks "export this
// to PDF" on an agent with no generate_document gets a refusal in place of the
// thing they were offered.
//
// A missed match costs nothing: the suggestion runs as an ordinary turn and the
// agent answers or says it cannot. This is a filter on the obvious cases, not a
// capability model, and it must not grow into one — the honest version of that
// is the eval category, not a longer table of verbs.
var suggestionTools = map[string][]string{
	"create_dashboard":  {"dashboard", "chart", "graph", "plot", "visuali", "grafik", "diagram"},
	"generate_document": {"pdf", "pptx", "deck", "slide", "excel", "xlsx", "csv", "export", "report file", "unduh", "laporan pdf"},
	"schedule_task":     {"every monday", "every week", "every month", "schedule", "recurring", "setiap minggu", "setiap bulan"},
	"propose_action":    {"send a message", "notify", "email them", "kirim pesan"},
}

func needsMissingTool(prompt string, held []string) bool {
	low := strings.ToLower(prompt)
	for tool, phrases := range suggestionTools {
		if slices.Contains(held, tool) {
			continue
		}
		for _, ph := range phrases {
			if strings.Contains(low, ph) {
				return true
			}
		}
	}
	return false
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Rune-safe: a label cut mid-rune renders as a replacement character, which
	// is worse-looking than the character it saved.
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// toolNames reads the names off the tools this turn was actually handed.
//
// From the built agent rather than from the roster row or a constant: the row's
// allowlist is empty for an unrestricted agent, the constant would be this
// release's list rather than this deployment's, and neither knows about the
// tenant's own MCP tools the factory appended (T-M2). What the agent holds is
// the only answer that is true for the turn.
func heldToolNames(tools []interfaces.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

// nextStepsMetadata is what completeWith persists and publishes. Nil steps
// produce a nil map, so a turn with no suggestions writes the column exactly as
// it wrote it before this ticket.
func nextStepsMetadata(steps []domain.NextStep) map[string]any {
	if len(steps) == 0 {
		return nil
	}
	return map[string]any{"next_steps": steps}
}
