package eval

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/bootstrap"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// Runner executes golden cases against a live tenant.
type Runner struct {
	stack   *bootstrap.Stack
	tenant  Tenant
	timeout time.Duration
}

// Tenant identifies who the eval runs as. The harness never invents these:
// cmd/eval seeds or looks them up first, so a failure to resolve the tenant
// is reported before any LLM call is paid for.
type Tenant struct {
	CompanyID   string
	CompanyName string
	UserID      string
	Currency    string
}

// NewRunner builds a runner over an already-wired stack.
func NewRunner(stack *bootstrap.Stack, tenant Tenant, timeout time.Duration) *Runner {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	return &Runner{stack: stack, tenant: tenant, timeout: timeout}
}

// RunCase executes one case end-to-end through the real ChatRunner and
// scores the result.
//
// Each case gets its own thread. That is not tidiness: threads carry agent
// memory and a rolling summary, so sharing one would let case 7's answer
// leak into case 8's context and make the score depend on file order.
//
// A case with follow-ups runs every turn in that one thread, sequentially, and
// scores the last. The turns before it are recorded but not scored: what a
// follow-up case asserts is where the conversation *ended up*, and a set-up
// turn that phrases its answer unexpectedly is not a failure of the thing
// under test. They are still reported on a failure, because "the follow-up
// went wrong" is unreadable without the answer it followed.
func (r *Runner) RunCase(ctx context.Context, c Case) Result {
	res := Result{ID: c.ID, Category: c.Category, Question: c.Question}

	// One timeout for the whole case rather than per turn: a three-turn case is
	// three agent runs, and the default was set for one.
	runCtx, cancel := context.WithTimeout(ctx, r.timeout*time.Duration(len(c.Questions())))
	defer cancel()

	// No agent: the eval measures the deployment's default, which is what the
	// T-16 baseline was scored against. Pinning a case to one would make the
	// score depend on the demo tenant's roster.
	thread, err := r.stack.ThreadSvc.CreateDashboardThread(runCtx, r.tenant.CompanyID, r.tenant.UserID, c.Question, "")
	if err != nil {
		res.Failures = []string{fmt.Sprintf("create thread: %v", err)}
		return res
	}
	res.ThreadID = thread.ID

	for i, question := range c.Questions() {
		turn := r.runTurn(runCtx, c, thread.ID, question)
		res.Turns = append(res.Turns, turn)
		res.DurationMS += turn.DurationMS

		// The last turn's reply and calls are the case's, and they are what
		// Score sees. Assigned every iteration so an aborted case still reports
		// the furthest it got rather than an empty reply.
		res.Reply = turn.Reply
		res.ToolCalls = turn.ToolCalls

		if turn.Error != "" {
			// A transport-level failure is not a wrong answer — it is a case
			// that did not run. Recorded distinctly so a flaky provider does
			// not silently depress the pass rate.
			res.Error = turn.Error
			res.Failures = []string{fmt.Sprintf("run failed on turn %d/%d: %s",
				i+1, len(c.Questions()), turn.Error)}
			return res
		}
	}

	// Over the thread, so a multi-turn case reports what the whole conversation
	// cost. That is the honest number for a set whose mean cost is compared
	// run to run: the follow-up is work the tenant pays for.
	r.attachUsage(runCtx, &res, thread.ID)
	res.Failures = Score(c, res.Reply, res.ToolCalls)
	res.Passed = len(res.Failures) == 0
	return res
}

// runTurn runs one question against an existing thread and records what the
// bus saw. Every turn builds its own ChatRunner and its own bus, exactly as
// the worker does per task — sharing a bus across turns would pool their tool
// calls and make `must_not_call` on a follow-up unassertable.
func (r *Runner) runTurn(ctx context.Context, c Case, threadID, question string) TurnResult {
	turn := TurnResult{Question: question}

	userMsg, err := r.stack.ThreadSvc.AppendUserMessage(ctx, threadID, question)
	if err != nil {
		turn.Error = fmt.Sprintf("append user message: %v", err)
		return turn
	}

	bus := &recordingBus{}
	runner := r.stack.NewChatRunner(bus, nil)

	start := time.Now()
	runErr := runner.Run(ctx, queue.ChatRunPayload{
		CompanyID: r.tenant.CompanyID,
		ThreadID:  threadID,
		UserID:    r.tenant.UserID,
		Channel:   domain.ChannelDashboard,
		Message:   question,
		// Assembled exactly as `POST /v1/reports` assembles it, from the same
		// function, so that a case asserting "our own directive is not read as
		// an injection" is asserting it about the directive that ships.
		Directive:       reportDirective(c, r.tenant.Currency),
		UserMsgID:       userMsg.ID,
		CompanyName:     r.tenant.CompanyName,
		DefaultCurrency: r.tenant.Currency,
	})
	turn.DurationMS = time.Since(start).Milliseconds()
	turn.Reply = bus.reply()
	turn.ToolCalls = bus.toolCalls()
	if runErr != nil {
		turn.Error = runErr.Error()
	}
	return turn
}

// reportDirective returns the per-turn directive for a case that asked to be
// run as a report, and "" for every other case — which is what a dashboard
// turn carries.
func reportDirective(c Case, currency string) string {
	if c.ReportFormat == "" {
		return ""
	}
	return app.ReportDirective(app.ReportDirectiveInput{
		Format:   domain.DocumentFormat(c.ReportFormat),
		Currency: currency,
	})
}

// attachUsage reads back what the turn cost. Best-effort: the usage rows are
// written by a different code path and a metering bug (finding Q-12, live in
// the T-00 smoke test) must not fail an otherwise correct case. When the
// numbers come back zero, that is itself worth seeing in the report.
func (r *Runner) attachUsage(ctx context.Context, res *Result, threadID string) {
	from := time.Now().Add(-24 * time.Hour)
	to := time.Now().Add(time.Minute)
	summary, err := r.stack.Usage.SummaryByThread(ctx, r.tenant.CompanyID, threadID, from, to)
	if err != nil {
		logrus.WithError(err).WithField("thread_id", threadID).Debug("eval: usage lookup failed")
		return
	}
	res.TokensIn = summary.TotalTokensIn
	res.TokensOut = summary.TotalTokensOut
	res.CostUSD = summary.TotalCostUSD
	res.LLMCalls = summary.EventCounts[domain.UsageEventLLMCall]
}

// recordingBus is the eval harness's stand-in for Redis pub/sub. The worker
// publishes the agent's whole turn through this interface — deltas, tool
// calls, the final message — so implementing it is how the harness observes
// a run without reaching inside the agent.
type recordingBus struct {
	mu     sync.Mutex
	events []app.ChatEvent
}

func (b *recordingBus) Publish(threadID string, evt app.ChatEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, evt)
	return nil
}

// PublishOutbound is a no-op: the eval tenant has no Discord or Lark
// delivery, and firing real outbound messages from a scoring run would be a
// bug with an audience.
func (b *recordingBus) PublishOutbound(evt app.OutboundEvent) error { return nil }

// reply prefers the final event's content and falls back to the assembled
// deltas, which is what a WebSocket client sees when a turn ends without a
// clean final frame.
func (b *recordingBus) reply() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	var deltas strings.Builder
	final := ""
	for _, e := range b.events {
		switch e.Type {
		case "delta":
			deltas.WriteString(e.Content)
		case "final":
			final = e.Content
		}
	}
	if strings.TrimSpace(final) != "" {
		return final
	}
	return deltas.String()
}

func (b *recordingBus) toolCalls() []ToolInvocation {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]ToolInvocation, 0, 4)
	for _, e := range b.events {
		if e.Type != "tool_call" || e.ToolCall == nil {
			continue
		}
		out = append(out, ToolInvocation{Name: e.ToolCall.Name, Args: e.ToolCall.Arguments})
	}
	return out
}
