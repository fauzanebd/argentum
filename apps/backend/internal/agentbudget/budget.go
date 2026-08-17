// Package agentbudget bounds what one agent turn may spend, and records what
// that turn actually retrieved.
//
// Why it exists (ticket T-16, findings C-1 and E-5). The agent used to run
// under a hard 3-iteration cap. Running out was not handled: agent-sdk-go's
// response to an exhausted cap is one more model call carrying the message
// "Please provide your final response based on the information available"
// — with nothing said about what to do when the information available is
// nothing. The model filled the gap. The T-00 smoke test caught it reporting
// "$1,234,567.89" against a true 3,863,405,700, and the first eval run caught
// it reporting "IDR 1,488,000" from a query that matched zero rows.
//
// Raising the cap alone moves the cliff without removing it, so this package
// does three things instead:
//
//  1. Spends a budget across four dimensions — iterations, tool calls,
//     cumulative tokens, wall clock — rather than one counter.
//  2. Tells the model in-band when the budget is gone and what to do next.
//     The message arrives as a tool result, which the model reads, rather
//     than as an error, which it never sees.
//  3. Records the turn's evidence — which data tools ran, how many rows came
//     back — which is what the reply is judged against before it is sent
//     (guardrails.CheckFabrication).
//  4. Holds one call back from the budget for the tool that writes the
//     deliverable, so a turn asked for a file cannot run out with the file
//     unwritten. See Tracker.reserveAppliesLocked.
//
// Enforcement points differ per dimension, and the difference matters:
//
//   - Tool calls and wall clock are checked in this package, on every tool
//     call, on every provider.
//   - Tokens and iterations are read from the internal/llmusage HTTP tap,
//     which exists only on the OpenAI-interface path (the default, and the
//     one C-1 was observed on). On the Anthropic path both are inert and the
//     SDK's own iteration cap is the backstop — see iterationsUsed.
package agentbudget

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fauzanebd/argentum/internal/llmusage"
)

// Budget is the per-turn ceiling. Zero on any field disables that dimension.
type Budget struct {
	// MaxIterations is the tool-calling round trip ceiling handed to the SDK.
	// This package refuses tools during the final permitted iteration so the
	// model writes its answer knowing it ran out, instead of the SDK asking
	// for one blind.
	MaxIterations int
	// MaxToolCalls caps tool executions across the whole turn. It is the
	// dimension that binds when a model loops several tools per iteration.
	MaxToolCalls int
	// MaxTokens caps cumulative provider-reported tokens for the turn
	// (input + output + cache). Runaway protection, not a cost target.
	MaxTokens int
	// Wall caps elapsed time from the first tool call.
	Wall time.Duration
}

// Default is the shipped budget. Eight iterations replaces the 3-iteration
// cap of finding Q-5; twelve tool calls allows a schema lookup, a probe and
// an aggregation per source across a three-source tenant without the model
// having to ration itself.
func Default() Budget {
	return Budget{
		MaxIterations: 8,
		MaxToolCalls:  12,
		MaxTokens:     200_000,
		Wall:          150 * time.Second,
	}
}

// ForDocument returns the budget for a turn whose deliverable is a file
// (T-A2's `POST /v1/reports`).
//
// The live gate found why this is needed and the shape of the failure is worth
// keeping: an agentic report spent all eight iterations on `get_schema` and
// five `run_sql` calls, hit the cap, and answered in prose — **without ever
// calling `generate_document`**. Nothing was broken. The budget was simply
// tuned for a chat turn, where the last iteration produces the answer, and on
// this door the last iteration produces the *file*. A report that explores
// exactly as much as a chat turn does still needs one more call after it, and
// the one it needs is the only one the caller asked for.
//
// Headroom on the exploration budget rather than a separate budget, so a
// tenant who has tuned theirs down still has it respected — this raises what
// they set, it does not replace it. Tokens and wall clock are untouched:
// neither was the binding constraint, and a document turn that runs away is
// still a document turn that has to stop.
//
// The headroom is room to explore, not a guarantee that the file gets written;
// the guarantee is the reserved deliverable call, which every turn has whether
// or not it came through this door. A chat turn asking for a PDF has no
// APIReportID and so never reaches here — it ran out mid-exploration with
// nothing to download, which is what the reserve now prevents.
func (b Budget) ForDocument() Budget {
	b = b.Normalize()
	b.MaxIterations += documentHeadroomIterations
	b.MaxToolCalls += documentHeadroomToolCalls
	return b
}

// Headroom for a document turn. Four and six rather than one: the failing run
// had six tool calls across eight iterations and was still mid-exploration, so
// one more of each would only have moved where it ran out. These are the
// numbers that let a turn finish exploring *and* write the file.
const (
	documentHeadroomIterations = 4
	documentHeadroomToolCalls  = 6
)

// Normalize replaces non-positive fields with the shipped defaults so a
// half-filled config cannot silently disable a dimension.
func (b Budget) Normalize() Budget {
	d := Default()
	if b.MaxIterations <= 0 {
		b.MaxIterations = d.MaxIterations
	}
	if b.MaxToolCalls <= 0 {
		b.MaxToolCalls = d.MaxToolCalls
	}
	if b.MaxTokens <= 0 {
		b.MaxTokens = d.MaxTokens
	}
	if b.Wall <= 0 {
		b.Wall = d.Wall
	}
	return b
}

// dataTools are the tools whose results are evidence for a stated figure.
// A reply containing a number that none of these produced in this turn is
// the failure mode this whole package exists to catch. query_metric joins
// the list when T-07 lands.
var dataTools = map[string]bool{
	"run_sql":      true,
	"query_metric": true,
}

// IsDataTool reports whether a tool's result can ground a figure in a reply.
func IsDataTool(name string) bool { return dataTools[name] }

// deliverableTools produce what the user asked for, rather than gather what it
// takes to produce it. Exactly one call to one of these is held in reserve past
// exhaustion — see Tracker.reserveAppliesLocked.
var deliverableTools = map[string]bool{
	"generate_document": true,
}

// IsDeliverableTool reports whether a tool's output is itself the thing the
// user asked for.
func IsDeliverableTool(name string) bool { return deliverableTools[name] }

// Tracker is the per-turn state. One is created per chat turn and carried in
// the context, so both the tool guard and the metering layer can reach it.
// Safe for concurrent use: a provider may execute several tool calls from one
// iteration.
type Tracker struct {
	budget Budget
	start  time.Time

	mu               sync.Mutex
	toolCalls        int
	dataCalls        int
	deliverableCalls int
	dataRows         int
	emptyResults     int
	toolErrors       int
	exhausted        bool
	reason           string
	tools            []string
}

// New returns a tracker for one turn. The wall clock starts now: a turn that
// spends 60s inside the table-picker embedding call has genuinely spent it.
func New(b Budget) *Tracker {
	return &Tracker{budget: b.Normalize(), start: time.Now()}
}

type trackerKey struct{}

// WithTracker carries t on ctx for the duration of one turn.
func WithTracker(ctx context.Context, t *Tracker) context.Context {
	return context.WithValue(ctx, trackerKey{}, t)
}

// FromContext returns the turn's tracker, or nil when none was installed.
// Every method below is nil-safe so callers outside a chat turn (the
// connection describer, the reindex path) need no special case.
func FromContext(ctx context.Context) *Tracker {
	t, _ := ctx.Value(trackerKey{}).(*Tracker)
	return t
}

// Snapshot is what the turn spent and what it retrieved. Copied out under
// the lock so callers can log or score it without holding one.
type Snapshot struct {
	ToolCalls int
	DataCalls int
	// DeliverableCalls counts calls to a tool whose output is the thing the
	// user asked for. Zero on a turn that was asked for a file and did not
	// write one is the failure the reserve exists to prevent, and it is the
	// only field that says so.
	DeliverableCalls int
	DataRows         int
	EmptyResults     int
	ToolErrors       int
	Exhausted        bool
	Reason           string
	Tools            []string
	Elapsed          time.Duration
}

// Snapshot returns the current state of the turn.
func (t *Tracker) Snapshot() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tools := make([]string, len(t.tools))
	copy(tools, t.tools)
	return Snapshot{
		ToolCalls:        t.toolCalls,
		DataCalls:        t.dataCalls,
		DeliverableCalls: t.deliverableCalls,
		DataRows:         t.dataRows,
		EmptyResults:     t.emptyResults,
		ToolErrors:       t.toolErrors,
		Exhausted:        t.exhausted,
		Reason:           t.reason,
		Tools:            tools,
		Elapsed:          time.Since(t.start),
	}
}

// Budget returns the ceiling this turn runs under.
func (t *Tracker) Budget() Budget {
	if t == nil {
		return Default()
	}
	return t.budget
}

// Begin is called before a tool executes. When it returns blocked, the guard
// must NOT run the tool: it returns refusal as the tool's result instead, and
// that text is the model's instruction for how to finish honestly.
//
// Once any dimension trips the turn stays exhausted. A model that keeps
// calling tools after being told to stop gets the same answer each time,
// which is cheaper and more legible than re-deciding per call.
//
// The single exception is the reserved deliverable call — see
// reserveAppliesLocked.
func (t *Tracker) Begin(ctx context.Context, tool string) (refusal string, blocked bool) {
	if t == nil {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.exhausted {
		switch {
		case t.toolCalls >= t.budget.MaxToolCalls:
			t.exhaustLocked(fmt.Sprintf("tool-call budget spent (%d of %d)",
				t.toolCalls, t.budget.MaxToolCalls))
		case time.Since(t.start) >= t.budget.Wall:
			t.exhaustLocked(fmt.Sprintf("time budget spent (%s of %s)",
				time.Since(t.start).Round(time.Second), t.budget.Wall))
		case tokensUsed(ctx) >= t.budget.MaxTokens && t.budget.MaxTokens > 0:
			t.exhaustLocked(fmt.Sprintf("token budget spent (%d of %d)",
				tokensUsed(ctx), t.budget.MaxTokens))
		case t.budget.MaxIterations > 1 && iterationsUsed(ctx) >= t.budget.MaxIterations-1:
			// The final permitted iteration is reserved for the answer. Letting
			// tools run here spends it, and the SDK then asks for a final
			// response with no instruction attached — the exact sequence that
			// produced C-1.
			t.exhaustLocked(fmt.Sprintf("iteration budget spent (%d of %d)",
				iterationsUsed(ctx)+1, t.budget.MaxIterations))
		}
	}
	if t.exhausted && !t.reserveAppliesLocked(ctx, tool) {
		return t.refusalLocked(), true
	}

	t.toolCalls++
	t.tools = append(t.tools, tool)
	if dataTools[tool] {
		t.dataCalls++
	}
	if deliverableTools[tool] {
		t.deliverableCalls++
	}
	return "", false
}

// reserveAppliesLocked reports whether tool may run even though the budget is
// spent. One call to one deliverable tool per turn is held back for this.
//
// Why a reserve rather than a bigger budget. The budget bounds *exploration*,
// and every dimension it bounds is spent finding out what to put in the answer.
// A turn asked for a file has one call that is not exploration: the one that
// writes the file. Refusing that call is the one refusal that cannot produce an
// honest partial answer — the user gets prose where they asked for a document,
// which is the failure agentbudget.ForDocument was written for and only fixed
// for `POST /v1/reports`. A chat turn asking for a PDF has the same shape and
// no APIReportID, so it ran out with the deliverable still unwritten.
//
// Bounded on purpose: one call, only if none has run yet (a file already
// written needs no second one), and never once the turn's context is done —
// that call would fail anyway, and the reserve is not a licence to run past a
// deadline. The spec it builds from is whatever the turn already retrieved, and
// the reply is still judged by guardrails.CheckFabrication.
func (t *Tracker) reserveAppliesLocked(ctx context.Context, tool string) bool {
	if !deliverableTools[tool] || t.deliverableCalls > 0 {
		return false
	}
	return ctx.Err() == nil
}

// Observe records what a tool returned. Only data-tool results carry
// evidence, but errors from any tool are counted so the incomplete-answer
// message can say what went wrong.
func (t *Tracker) Observe(tool, result string, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if err != nil {
		t.toolErrors++
		return
	}
	if !dataTools[tool] {
		return
	}
	n, ok := rowCount(result)
	if !ok {
		return
	}
	if n == 0 {
		t.emptyResults++
		return
	}
	t.dataRows += n
}

// Exhaust trips the budget from outside the tool path — the chat runner uses
// it when the turn's context deadline fires.
func (t *Tracker) Exhaust(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exhaustLocked(reason)
}

func (t *Tracker) exhaustLocked(reason string) {
	if t.exhausted {
		return
	}
	t.exhausted = true
	t.reason = reason
}

// FinalInstruction is what the model is told when the budget runs out. It is
// deliberately specific about the shape of an acceptable answer: the failure
// being prevented is not "the model kept going", it is "the model produced a
// confident figure it never retrieved".
const FinalInstruction = "Your budget for this turn is spent. Do not call any more tools. " + finalAnswer

// finalAnswer is the half both instructions share: what an acceptable final
// reply contains. Split out so the two cannot drift — the anti-fabrication
// clause has to hold whether or not a file is still owed.
const finalAnswer = "Write your final reply now using ONLY results you already received in this turn: " +
	"restate what the user asked, REPORT THE ACTUAL FIGURES any tool already returned to " +
	"you — those numbers are yours to give and the user is waiting for them, do not " +
	"withhold them or offer to present them later — then state plainly what you could not " +
	"retrieve and why, and ask whether to continue. " +
	"Do NOT state any monetary amount, total, count or metric value that did not come " +
	"from a tool result in this turn — say you could not complete that part instead."

// DeliverableInstruction replaces FinalInstruction while the reserved
// deliverable call is still unspent. FinalInstruction's "do not call any more
// tools" would talk the model out of the one call the reserve exists to permit
// — a model told to stop, stops, and the turn ends with the file unwritten for
// a different reason than before.
//
// Conditional on the model actually holding the tool, because this tracker
// cannot see the turn's registry: an agent whose allowlist omits
// generate_document reads the condition, finds it false, and falls through to
// the ordinary final answer.
const DeliverableInstruction = "Your exploration budget for this turn is spent. Run no more queries, " +
	"and create no more cards or dashboards. " +
	"If this turn was asked for a file (PDF, PPTX, XLSX or CSV), you have the generate_document " +
	"tool, and you have not yet called it: make that ONE call NOW — build the spec only from " +
	"results you already received in this turn, invent no figures to fill it — and reply with " +
	"the download link it returns. " +
	"Otherwise: " + finalAnswer

// refusalLocked is the tool result a blocked call returns. JSON because every
// other tool in the registry returns JSON, and a model that has been reading
// structured results all turn should not have to switch parsers at the end.
func (t *Tracker) refusalLocked() string {
	instruction := FinalInstruction
	if t.deliverableCalls == 0 {
		instruction = DeliverableInstruction
	}
	payload := map[string]interface{}{
		"budget_exhausted": true,
		"reason":           t.reason,
		"retrieved_so_far": t.retrievedLocked(),
		// The reserve is stated as data as well as prose: a model that reads
		// the payload structurally and skims the instruction still sees that
		// one call remains.
		"document_call_remaining": t.deliverableCalls == 0,
		"instruction":             instruction,
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return instruction
	}
	return string(out)
}

// IsRefusal reports whether a tool result is this package's refusal payload
// rather than a tool's own output. The audit log (T-05) needs it to tell a
// call that ran from one that was never allowed to: both come back as a
// string with a nil error, because that is how the model has to receive them.
func IsRefusal(result string) bool {
	var payload struct {
		BudgetExhausted bool `json:"budget_exhausted"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return false
	}
	return payload.BudgetExhausted
}

// IsRefusalPayload is IsRefusal for a caller that has already parsed the
// result, and RefusalReason is the sentence beside it.
//
// Two entry points rather than one because the two callers hold different
// things: the audit decorator has the raw string the tool returned, and the
// tool digest (T-Q12) has the map the stream event was unmarshalled into.
// Re-marshalling one to reach the other would make a memory row depend on a
// round trip that can fail. Both read the same key, so neither can drift.
func IsRefusalPayload(result map[string]interface{}) bool {
	if result == nil {
		return false
	}
	b, _ := result["budget_exhausted"].(bool)
	return b
}

// RefusalReason is why the call was refused ("iteration budget spent (8 of
// 8)"), or "" when the payload is not a refusal or carries no reason. It is
// the half a later turn needs: "refused" tells it the call did not run,
// and the reason tells it whether trying again is worth an iteration.
func RefusalReason(result map[string]interface{}) string {
	if !IsRefusalPayload(result) {
		return ""
	}
	s, _ := result["reason"].(string)
	return strings.TrimSpace(s)
}

// retrievedLocked summarises the turn's evidence in one line so the model can
// quote it back accurately rather than guessing at what it has.
func (t *Tracker) retrievedLocked() string {
	if t.toolCalls == 0 {
		return "nothing — no tool completed in this turn"
	}
	parts := []string{fmt.Sprintf("%d tool call(s): %s", t.toolCalls, strings.Join(t.tools, ", "))}
	switch {
	case t.dataCalls == 0:
		parts = append(parts, "no query was run, so no figures were retrieved")
	case t.dataRows == 0:
		parts = append(parts, "every query returned zero rows, so no figures were retrieved")
	default:
		parts = append(parts, fmt.Sprintf("%d row(s) returned across %d quer(y/ies)", t.dataRows, t.dataCalls))
	}
	if t.toolErrors > 0 {
		parts = append(parts, fmt.Sprintf("%d tool call(s) failed", t.toolErrors))
	}
	return strings.Join(parts, "; ")
}

// rowCount pulls row_count out of a data tool's JSON result. ok is false when
// the result is not JSON or carries no row_count — an unparseable result is
// not evidence of zero rows, and treating it as such would block honest
// replies.
func rowCount(result string) (int, bool) {
	var payload struct {
		RowCount *int `json:"row_count"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return 0, false
	}
	if payload.RowCount == nil {
		return 0, false
	}
	return *payload.RowCount, true
}

// tokensUsed reads the turn's provider-reported token total off the HTTP tap
// installed by app.MeteredLLM. Returns 0 when there is no tap, which leaves
// the dimension inert rather than tripping it.
func tokensUsed(ctx context.Context) int {
	c := llmusage.CollectorFrom(ctx)
	if c == nil {
		return 0
	}
	u, _ := c.Snapshot()
	return u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// iterationsUsed counts completed tool-calling iterations for this turn.
//
// There is no iteration counter to ask: the loop lives inside the provider
// client and the tools it calls are handed no iteration number. What the tap
// does see is one HTTP response per iteration, each carrying its own usage
// report — so the count of usage reports is the count of completed
// iterations. Zero when there is no tap (the Anthropic path, which reports
// usage through stream metadata instead), and the dimension is then inert:
// the SDK's own WithMaxIterations remains the backstop there.
func iterationsUsed(ctx context.Context) int {
	c := llmusage.CollectorFrom(ctx)
	if c == nil {
		return 0
	}
	_, events := c.Snapshot()
	return events
}
