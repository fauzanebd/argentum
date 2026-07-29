package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/Ingenimax/agent-sdk-go/pkg/multitenancy"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// AgentSpec is everything one turn needs from the factory. A struct rather
// than a parameter list because two of its fields are strings and two are
// LLMs, and a caller swapping either pair would compile.
type AgentSpec struct {
	// Primary and Light are the freshly resolved per-tenant clients.
	Primary, Light interfaces.LLM
	// PrimaryInterface names the provider, so the factory can gate
	// provider-specific options like Anthropic prompt caching.
	PrimaryInterface string
	// SystemAddendum is appended to the shared system prompt for this turn
	// only. It is how an instruction the caller did not write reaches the
	// model without passing through the input guardrails, which exist to
	// judge what the caller *did* write (T-A2b).
	SystemAddendum string
	// Persona is the roster agent's own instructions (T-S2), appended to the
	// shared prompt ahead of the addendum. An addendum, never a replacement:
	// the shared prompt carries the SQL-dialect rules, the anti-fabrication
	// language and the formatting contract, and a customer-authored prompt
	// that could replace them would be a self-service route back to C-1.
	Persona string
	// ToolNames restricts this turn to a subset of the registry, by name.
	// Empty means every tool — the roster's rule, stated once in
	// domain.Agent.AllowsTool and not restated here.
	ToolNames []string

	// Primitives rather than a *domain.Agent on purpose: the factory lives in
	// bootstrap, and it should not have to learn a domain entity in order to
	// append a string and filter a slice.
}

// AgentFactory builds a sdkagent.Agent for one chat turn. The worker captures
// tools, memory, the shared system prompt, and guardrails in the closure;
// callers pass what varies per turn.
type AgentFactory func(AgentSpec) (*sdkagent.Agent, error)

// LLMResolver is the half of llmtenant.ClientCache a turn needs: the tenant's
// client for a tier, and the profile that names its provider.
//
// Declared at the consumer, like ChatEnqueuer's BudgetChecker — and for the
// same reason it earns its keep here: the concrete cache resolves per-tenant
// credentials out of the control database, so a runner that took it could not
// be run at all in a test, and how a turn is assembled (T-A2b) is exactly the
// kind of thing that has to be.
type LLMResolver interface {
	For(ctx context.Context, companyID string, tier domain.LLMTier) (interfaces.LLM, *llmtenant.EffectiveProfile, error)
}

// BudgetResolver returns the per-turn budget a company's agent runs under.
//
// It is a function rather than a value because T-16 asks for per-company
// budgets and the ticket carries no migration number — the sprint's migration
// numbers are pre-assigned per ticket, and claiming an unassigned one would
// collide with a ticket that already owns it. So the seam is here and the
// storage is not: bootstrap installs a resolver that returns the process-wide
// defaults, and a per-company lookup replaces that one line when there is a
// table to read from.
type BudgetResolver func(ctx context.Context, companyID string) agentbudget.Budget

// AgentLoader is the half of the roster one turn needs (T-S2): the agent the
// payload names, or the company default when it names none.
//
// domain.AgentRepository satisfies it. Narrowed at the consumer because a
// runner that could write to the roster is a runner that could be asked to,
// and because a nil loader has to remain legal — the eval harness and every
// test below run without one, and a turn with no agent is the behaviour this
// product had before the roster existed.
type AgentLoader interface {
	GetByID(ctx context.Context, companyID, id string) (*domain.Agent, error)
	GetDefault(ctx context.Context, companyID string) (*domain.Agent, error)
}

// ScheduledRunMarker is the narrow contract ChatRunner uses to close out
// a scheduled_task_runs row when the agent finishes (or errors). Defined
// as an interface so the worker can pass *ScheduledTaskService while
// API-only flows can pass nil.
type ScheduledRunMarker interface {
	MarkRunResult(ctx context.Context, runID, assistantMsgID string, runErr error)
}

// APIReportCompleter is the narrow contract ChatRunner uses to close out an
// `api_reports` row when an agentic report turn finishes (T-A2). Shaped like
// ScheduledRunMarker and installed the same way, because it is the same idea:
// a turn that somebody else is waiting on the outcome of.
type APIReportCompleter interface {
	CompleteReport(ctx context.Context, reportID, threadID string, runErr error)
}

// ChatRunner is the worker-side half of the chat pipeline. It runs the
// agent against a queued ChatRunPayload, persists the assistant turn,
// publishes streaming events through the EventBus, and (for WhatsApp
// channels) sends the final reply via the WA provider directly.
type ChatRunner struct {
	threads      *ThreadService
	messages     domain.MessageRepository
	threadRepo   domain.ThreadRepository
	connections  domain.ConnectionRepository
	agentFactory AgentFactory
	llmCache     LLMResolver
	bus          EventBus
	wa           whatsapp.Provider
	larkProv     lark.Provider
	pool         *db.TenantConnPool
	scheduled    ScheduledRunMarker
	apiReports   APIReportCompleter
	historyLimit int
	budgetFor    BudgetResolver
	actions      domain.AgentActionRepository
	roster       AgentLoader

	// Embedding-based table picker. Both must be non-nil to inject hints;
	// otherwise the runner silently skips and the agent falls back to the
	// regular get_schema flow.
	embRepo    domain.TableEmbeddingRepository
	embedCache *llmtenant.EmbeddingCache
	embTopK    int
}

// NewChatRunner wires the worker's dependencies. scheduled is optional;
// pass nil when no scheduled-task subsystem is configured. historyLimit
// caps how many prior thread messages are re-hydrated into agent memory
// per turn; <=0 falls back to a safe default.
func NewChatRunner(
	threads *ThreadService,
	messages domain.MessageRepository,
	threadRepo domain.ThreadRepository,
	connections domain.ConnectionRepository,
	agentFactory AgentFactory,
	llmCache LLMResolver,
	bus EventBus,
	wa whatsapp.Provider,
	pool *db.TenantConnPool,
	scheduled ScheduledRunMarker,
	historyLimit int,
) *ChatRunner {
	if historyLimit <= 0 {
		historyLimit = 20
	}
	return &ChatRunner{
		threads:      threads,
		messages:     messages,
		threadRepo:   threadRepo,
		connections:  connections,
		agentFactory: agentFactory,
		llmCache:     llmCache,
		bus:          bus,
		wa:           wa,
		pool:         pool,
		scheduled:    scheduled,
		historyLimit: historyLimit,
	}
}

// WithBudget installs the per-turn budget resolver. A runner without one
// falls back to agentbudget.Default() — an unbounded turn is not an option
// the caller gets, because the failure it produces is a fabricated number.
func (r *ChatRunner) WithBudget(resolve BudgetResolver) *ChatRunner {
	r.budgetFor = resolve
	return r
}

// WithAPIReports lets the runner close out an agentic report job when its turn
// ends (T-A2). Optional: a stack with no `/v1` report routes passes nothing
// and every turn behaves exactly as it did before.
func (r *ChatRunner) WithAPIReports(c APIReportCompleter) *ChatRunner {
	r.apiReports = c
	return r
}

// WithActionLog attaches the audit repository so a turn a guardrail stopped
// leaves a row (T-05).
//
// Tool calls are audited by the tools.WithAudit decorator, which is the
// ticket's one integration point and needs nothing from here. What it cannot
// see is a turn blocked before or after the tools: an input guardrail refuses
// the question, and the fabrication check refuses the answer. Neither reaches
// a tool, so neither would appear in the log at all — and "the agent was
// stopped from saying that" is the entry an auditor most wants to find.
func (r *ChatRunner) WithActionLog(repo domain.AgentActionRepository) *ChatRunner {
	r.actions = repo
	return r
}

// WithRoster lets a turn run as one of the tenant's agents (T-S2): its
// persona, its tools, its sources.
//
// Optional, and a runner without one behaves exactly as this product did
// before the roster existed — the shared prompt, the whole registry, every
// source the company owns. That is not a courtesy to tests: it is what a
// company whose roster failed to seed gets, and "cannot ask a question because
// a settings table is empty" is not an acceptable failure.
func (r *ChatRunner) WithRoster(l AgentLoader) *ChatRunner {
	r.roster = l
	return r
}

// WithLark attaches a Lark outbound provider so the runner can post replies
// for chat:run tasks on the Lark channel. Returning the receiver mirrors
// WithTablePicker so the worker can chain configuration on construction.
func (r *ChatRunner) WithLark(p lark.Provider) *ChatRunner {
	r.larkProv = p
	return r
}

// WithTablePicker enables the embedding-based table-hint injection. Pass
// a nil repo or cache to leave the feature disabled. topK <= 0 falls back
// to 8.
func (r *ChatRunner) WithTablePicker(repo domain.TableEmbeddingRepository, embCache *llmtenant.EmbeddingCache, topK int) *ChatRunner {
	if topK <= 0 {
		topK = 8
	}
	r.embRepo = repo
	r.embedCache = embCache
	r.embTopK = topK
	return r
}

// Run processes one chat task end-to-end. Errors returned trigger an
// asynq retry; user-visible failures (e.g. guardrail rejections) write a
// friendly assistant message and return nil.
func (r *ChatRunner) Run(ctx context.Context, p queue.ChatRunPayload) error {
	ctx = tenantctx.WithCompanyID(ctx, p.CompanyID)
	ctx = tenantctx.WithThreadID(ctx, p.ThreadID)
	if p.UserID != "" {
		ctx = tenantctx.WithUserID(ctx, p.UserID)
	}
	// Identity for the audit log (T-05). It rides the context because the
	// thing that writes the rows is a tool decorator four packages away, and
	// this is the only place that knows a cron tick is not a person.
	kind, ref := actorOf(p)
	ctx = tenantctx.WithActor(ctx, kind, ref)
	ctx = tenantctx.WithChannel(ctx, string(p.Channel))
	ctx = tenantctx.WithMessageID(ctx, p.UserMsgID)
	if p.RequestID != "" {
		ctx = tenantctx.WithRequestID(ctx, p.RequestID)
	}
	ctx = multitenancy.WithOrgID(ctx, p.CompanyID)
	ctx = memory.WithConversationID(ctx, p.ThreadID)

	// Cheap small-talk short-circuit: skip the agent (and the light-LLM
	// guardrail/classifier pipeline behind it) when the message is a
	// greeting or one-word ack. Saves multiple LLM calls per turn.
	//
	// Never for a turn carrying a directive. The deliverable of a report turn
	// is a file, and a caller whose prompt happens to read as small talk would
	// otherwise get a friendly sentence and a report that completed with
	// nothing attached — the exact silent failure T-A2b exists to remove.
	if reply, ok := trivialReply(p.Message); ok && p.Directive == "" {
		now := time.Now()
		_ = r.bus.Publish(p.ThreadID, ChatEvent{
			JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "started", Timestamp: now,
		})
		r.completeWith(ctx, p, reply, 0, 0, 0)
		return nil
	}

	// Install the turn's budget before anything can spend it. The tracker
	// rides the context because both ends need it: the tool guard inside the
	// provider's tool-calling loop reads it to decide whether a call is still
	// affordable, and the fabrication check below reads it to decide whether
	// the reply is allowed to contain a figure.
	budget := agentbudget.Default()
	if r.budgetFor != nil {
		budget = r.budgetFor(ctx, p.CompanyID).Normalize()
	}
	if p.APIReportID != "" {
		// A turn whose deliverable is a file needs one more call after it has
		// finished exploring, and that call is the only one the caller asked
		// for. See agentbudget.ForDocument for what the live gate found.
		budget = budget.ForDocument()
	}
	tracker := agentbudget.New(budget)
	ctx = agentbudget.WithTracker(ctx, tracker)

	// Which of the tenant's agents this turn runs as (T-S2). Installed beside
	// the budget tracker and for the same reason: the constraint has to reach
	// seven tools, and the tools take a context and a JSON string. Before the
	// LLMs are resolved, so that every row this turn writes — including the
	// audit row for a turn that fails to build an agent at all — carries it.
	agentRow := r.resolveAgent(ctx, p)
	ctx = agentscope.WithScope(ctx, scopeOf(agentRow))

	// Resolve the per-tenant LLMs for this turn. Primary is required; light
	// falls back to primary if resolution fails (preserves today's behavior
	// where missing LIGHT_LLM_API_KEY means "use primary").
	primaryLLM, primaryProfile, err := r.llmCache.For(ctx, p.CompanyID, domain.LLMTierPrimary)
	if err != nil {
		return r.handleRunError(ctx, p, fmt.Errorf("resolve primary LLM: %w", err))
	}
	lightLLM, _, err := r.llmCache.For(ctx, p.CompanyID, domain.LLMTierLight)
	if err != nil {
		logrus.WithError(err).Warn("resolve light LLM failed; using primary for guardrails")
		lightLLM = primaryLLM
	}
	agent, err := r.agentFactory(AgentSpec{
		Primary:          primaryLLM,
		Light:            lightLLM,
		PrimaryInterface: primaryProfile.Interface,
		// The one place the report directive enters the model's view. It is
		// not in p.Message, so the input guardrails never see it, and it is
		// not in the shared system prompt, so it applies to this turn alone.
		SystemAddendum: p.Directive,
		Persona:        personaOf(agentRow),
		ToolNames:      toolNamesOf(agentRow),
	})
	if err != nil {
		return r.handleRunError(ctx, p, fmt.Errorf("build agent: %w", err))
	}

	if err := r.hydrateMemory(ctx, agent, p); err != nil {
		logrus.WithError(err).Warn("memory hydration failed; continuing with empty context")
	}

	now := time.Now()
	_ = r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "started", Timestamp: now,
	})

	sources, err := r.connections.ListByCompany(ctx, p.CompanyID)
	if err != nil {
		logrus.WithError(err).Warn("source catalog prefetch failed; agent will discover via list_sources")
	}
	// The same scope the tools enforce, applied to the catalog the agent is
	// *told* about (T-S2). Skipping this would leave the agent reading about a
	// database its every query against would then be refused — the most
	// confusing failure available here, and one no tool-level test catches
	// because no tool is involved.
	sources = agentscope.FromContext(ctx).FilterSources(sources)

	start := time.Now()

	// Prepend organization, currency, source-catalog, and (optionally) the
	// embedding-based table-picker hint. Order matters: the table hint sits
	// closest to the top so the agent reads it before the source catalog.
	agentMsg := withCompanyNameContext(p.Message, p.CompanyName)
	agentMsg = withCurrencyContext(agentMsg, p.DefaultCurrency)
	agentMsg = withSourcesContext(agentMsg, sources)
	agentMsg = r.withRelevantTablesContext(ctx, agentMsg, p.Message, sources)

	// Try streaming first; fall back to blocking Run if the LLM doesn't
	// support it.
	var response string
	var streaming bool
	if agent.GetLLM().SupportsStreaming() {
		sp := p
		sp.Message = agentMsg
		if streamResp, err := r.runStream(ctx, agent, sp); err == nil {
			response = streamResp
			streaming = true
		} else {
			logrus.WithError(err).Warn("streaming failed; falling back to blocking run")
		}
	}
	if !streaming {
		var err error
		response, err = agent.Run(ctx, agentMsg)
		if err != nil {
			return r.handleRunError(ctx, p, err)
		}
	}

	latency := time.Since(start)
	response = r.rejectFabrication(ctx, p, response, tracker)
	r.completeWith(ctx, p, response, 0, 0, latency)
	return nil
}

// resolveAgent loads the roster row this turn runs as (T-S2), or nil.
//
// Nil is a legitimate answer in three cases, and none of them is an error: no
// roster is wired (the eval harness), the company has none (a seed that never
// ran), or the lookup failed. All three run the turn unscoped, which is what
// this product did before the roster existed.
//
// The payload's agent is preferred and the company default is the fallback,
// including when the named agent has since been deleted — a conversation must
// not become unanswerable because an admin tidied the roster, and the thread's
// own `agent_id` is already NULL by then anyway.
func (r *ChatRunner) resolveAgent(ctx context.Context, p queue.ChatRunPayload) *domain.Agent {
	if r.roster == nil {
		return nil
	}
	if p.AgentID != "" {
		a, err := r.roster.GetByID(ctx, p.CompanyID, p.AgentID)
		if err == nil {
			return a
		}
		if !errors.Is(err, domain.ErrNotFound) {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": p.CompanyID, "agent_id": p.AgentID,
			}).Warn("agent lookup failed; falling back to the company default")
		} else {
			logrus.WithFields(logrus.Fields{
				"company_id": p.CompanyID, "agent_id": p.AgentID,
			}).Info("agent gone since the turn was queued; falling back to the company default")
		}
	}
	def, err := r.roster.GetDefault(ctx, p.CompanyID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			logrus.WithError(err).WithField("company_id", p.CompanyID).
				Warn("default agent lookup failed; running this turn unscoped")
		}
		return nil
	}
	return def
}

// scopeOf, personaOf and toolNamesOf are the three things a turn takes from an
// agent. Separate nil-tolerant functions rather than methods, because the
// caller's `nil` is the ordinary case and a method set that has to be
// nil-checked at three call sites is three chances to forget.
func scopeOf(a *domain.Agent) agentscope.Scope {
	if a == nil {
		return agentscope.Scope{}
	}
	return agentscope.Scope{AgentID: a.ID, Name: a.Name, SourceIDs: a.SourceIDs}
}

func personaOf(a *domain.Agent) string {
	if a == nil {
		return ""
	}
	return a.PersonaPrompt
}

func toolNamesOf(a *domain.Agent) []string {
	if a == nil {
		return nil
	}
	return a.AllowedTools
}

// rejectFabrication is the last thing between the agent and the user: a reply
// that states a figure no tool produced this turn is replaced with an honest
// account of what happened (ticket T-16).
//
// It runs here rather than as an output rule in config/guardrails.yaml for
// two reasons. The rule needs turn state — which tools ran, how many rows came
// back — and a YAML regex over the reply text has none. And agent-sdk-go only
// applies Guardrails.ProcessOutput on its blocking path (pkg/agent/agent.go);
// the streaming path every chat turn takes never calls it, so a YAML output
// rule would not have run at all.
//
// On a streaming turn the offending text has already reached the dashboard as
// deltas by the time this fires. The final event and the persisted message
// both carry the replacement, so the UI settles on the honest answer, and the
// WhatsApp / Discord / Lark paths — which only ever see the final — never see
// the figure at all.
func (r *ChatRunner) rejectFabrication(
	ctx context.Context, p queue.ChatRunPayload, response string, tracker *agentbudget.Tracker,
) string {
	snap := tracker.Snapshot()
	replacement, blocked := guardrails.CheckFabrication(response, guardrails.TurnEvidence{
		ToolCalls:    snap.ToolCalls,
		DataCalls:    snap.DataCalls,
		DataRows:     snap.DataRows,
		EmptyResults: snap.EmptyResults,
		Exhausted:    snap.Exhausted,
		Reason:       snap.Reason,
	}, p.Message)

	if !blocked {
		if snap.Exhausted {
			logrus.WithFields(logrus.Fields{
				"company_id": p.CompanyID,
				"thread_id":  p.ThreadID,
				"reason":     snap.Reason,
				"tool_calls": snap.ToolCalls,
				"data_rows":  snap.DataRows,
			}).Info("turn exhausted its budget and answered without stating a figure")
		}
		return response
	}

	// The blocked text is logged in full: this is the only record of what the
	// agent tried to say, and tuning the rule is impossible without it.
	logrus.WithFields(logrus.Fields{
		"company_id":    p.CompanyID,
		"thread_id":     p.ThreadID,
		"tool_calls":    snap.ToolCalls,
		"data_calls":    snap.DataCalls,
		"data_rows":     snap.DataRows,
		"empty_results": snap.EmptyResults,
		"exhausted":     snap.Exhausted,
		"reason":        snap.Reason,
		"blocked_reply": response,
	}).Warn("reply stated a figure no tool returned this turn; replaced with an incomplete-answer message")
	r.recordBlockedTurn(ctx, p, "final_answer", "reply stated a figure no tool returned this turn")
	return replacement
}

// actorOf decides who a turn is attributable to. A scheduled run is not the
// user who authored the schedule — nobody was present, and an audit trail that
// says otherwise puts a person at a keyboard they were not sitting at. The
// channel refs are the identity each channel actually has: there is no
// dashboard user behind a WhatsApp message.
func actorOf(p queue.ChatRunPayload) (kind, ref string) {
	if p.ScheduledTaskID != "" {
		return string(domain.ActorKindSchedule), p.ScheduledTaskID
	}
	// An API key outranks any user reference on the payload for the same
	// reason a schedule does: the turn ran because a script called us, and the
	// tenant's own `user_ref` on a /v1 chat request is a label they chose, not
	// an identity we authenticated (T-13).
	if p.APIKeyID != "" {
		return string(domain.ActorKindAPIKey), p.APIKeyID
	}
	for _, candidate := range []string{p.UserID, p.DiscordUserID, p.LarkOpenID, p.PhoneNumber} {
		if candidate != "" {
			return string(domain.ActorKindUser), candidate
		}
	}
	return string(domain.ActorKindUser), ""
}

// recordBlockedTurn writes the audit row for a turn a guardrail stopped.
// toolName names which gate closed rather than a real tool: `guardrail` for a
// question refused on the way in, `final_answer` for an answer refused on the
// way out. Both are recorded with rows_returned unset, because neither ran
// anything.
//
// Silent no-op when no repository is attached: the eval harness runs the same
// runner and has no control-plane row to write into.
func (r *ChatRunner) recordBlockedTurn(ctx context.Context, p queue.ChatRunPayload, toolName, reason string) {
	if r.actions == nil {
		return
	}
	kind, ref := actorOf(p)
	action := &domain.AgentAction{
		CompanyID: p.CompanyID,
		ThreadID:  p.ThreadID,
		MessageID: p.UserMsgID,
		ActorKind: domain.ActorKind(kind),
		ActorRef:  ref,
		Channel:   p.Channel,
		// Off the context, not off the payload: the turn may be running under
		// the company default because the payload's agent was deleted, and the
		// row has to name the agent that actually ran (T-S2).
		AgentID:      agentscope.AgentID(ctx),
		ToolName:     toolName,
		ArgsRedacted: []byte(`{}`),
		ArgsHash:     sha256Hex(p.Message),
		ResultStatus: domain.ActionStatusBlocked,
		ErrorText:    reason,
		RequestID:    p.RequestID,
	}
	// Detached like the tool decorator's write, and for the same reason: the
	// turn this describes may already be over.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.actions.Create(writeCtx, action); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": p.CompanyID,
			"thread_id":  p.ThreadID,
			"tool":       toolName,
		}).Warn("blocked-turn audit write failed")
	}
}

// sha256Hex fingerprints the message a blocked turn was carrying. The text
// itself is not stored on the row — a refused question is exactly the input
// most likely to contain something the tenant would not want retained — but
// the same question asked twice is recognisable.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// runStream executes the agent with streaming and fans out delta / thinking /
// tool_call / tool_result events to the EventBus. It returns the full
// assembled response text.
func (r *ChatRunner) runStream(ctx context.Context, agent *sdkagent.Agent, p queue.ChatRunPayload) (string, error) {
	events, err := agent.RunStream(ctx, p.Message)
	if err != nil {
		return "", err
	}

	var fullResponse strings.Builder
	maxIterations := agentbudget.FromContext(ctx).Budget().MaxIterations
	lastIteration := 0

	for evt := range events {
		// Every provider event carries the iteration it came from. Republish
		// the boundary so a long multi-step turn reads as progress instead of
		// a stalled spinner — the SDK offers no iteration event of its own.
		if n := iterationOf(evt.Metadata); n > lastIteration {
			lastIteration = n
			_ = r.bus.Publish(p.ThreadID, ChatEvent{
				JobID:     p.UserMsgID,
				ThreadID:  p.ThreadID,
				Type:      "iteration",
				Metadata:  map[string]interface{}{"iteration": n, "max_iterations": maxIterations},
				Timestamp: time.Now(),
			})
		}

		switch evt.Type {
		case interfaces.AgentEventContent:
			fullResponse.WriteString(evt.Content)
			_ = r.bus.Publish(p.ThreadID, ChatEvent{
				JobID:     p.UserMsgID,
				ThreadID:  p.ThreadID,
				Type:      "delta",
				Content:   evt.Content,
				Timestamp: time.Now(),
			})

		case interfaces.AgentEventThinking:
			_ = r.bus.Publish(p.ThreadID, ChatEvent{
				JobID:        p.UserMsgID,
				ThreadID:     p.ThreadID,
				Type:         "thinking",
				ThinkingStep: evt.ThinkingStep,
				Timestamp:    time.Now(),
			})

		case interfaces.AgentEventToolCall:
			if evt.ToolCall != nil {
				args := map[string]interface{}{}
				if evt.ToolCall.Arguments != "" {
					_ = json.Unmarshal([]byte(evt.ToolCall.Arguments), &args)
				}
				_ = r.bus.Publish(p.ThreadID, ChatEvent{
					JobID:     p.UserMsgID,
					ThreadID:  p.ThreadID,
					Type:      "tool_call",
					ToolCall:  &ToolCallEvent{Name: evt.ToolCall.Name, Arguments: args},
					Timestamp: time.Now(),
				})
			}

		case interfaces.AgentEventToolResult:
			if evt.ToolCall != nil {
				res := map[string]interface{}{}
				if evt.ToolCall.Result != "" {
					_ = json.Unmarshal([]byte(evt.ToolCall.Result), &res)
				}
				_ = r.bus.Publish(p.ThreadID, ChatEvent{
					JobID:     p.UserMsgID,
					ThreadID:  p.ThreadID,
					Type:      "tool_result",
					ToolCall:  &ToolCallEvent{Name: evt.ToolCall.Name, Result: res},
					Timestamp: time.Now(),
				})
			}

		case interfaces.AgentEventError:
			if evt.Error != nil {
				errMsg := evt.Error.Error()
				// Guardrails rejections should be presented as normal
				// assistant messages, not raw errors.
				const guardrailsPrefix = "guardrails error: "
				if strings.HasPrefix(errMsg, guardrailsPrefix) {
					userMsg := strings.TrimPrefix(errMsg, guardrailsPrefix)
					fullResponse.Reset()
					fullResponse.WriteString(userMsg)
					r.recordBlockedTurn(ctx, p, "guardrail", userMsg)
					_ = r.bus.Publish(p.ThreadID, ChatEvent{
						JobID:     p.UserMsgID,
						ThreadID:  p.ThreadID,
						Type:      "delta",
						Content:   userMsg,
						Timestamp: time.Now(),
					})
				} else {
					_ = r.bus.Publish(p.ThreadID, ChatEvent{
						JobID:     p.UserMsgID,
						ThreadID:  p.ThreadID,
						Type:      "error",
						Error:     errMsg,
						Timestamp: time.Now(),
					})
				}
			}

		case interfaces.AgentEventComplete:
			// No-op; final event is published after the loop.
		}
	}

	return fullResponse.String(), nil
}

// iterationOf reads the tool-calling iteration number an SDK stream event was
// produced in. Returns 0 when absent — the provider tags content, tool-call
// and tool-result events but not the message-start/stop frames.
func iterationOf(md map[string]interface{}) int {
	if md == nil {
		return 0
	}
	switch n := md["iteration"].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// handleRunError deals with blocking-run failures. Guardrails errors are
// surfaced as assistant messages; everything else is retried by asynq.
func (r *ChatRunner) handleRunError(ctx context.Context, p queue.ChatRunPayload, err error) error {
	const guardrailsPrefix = "guardrails error: "
	userMsg := err.Error()
	if strings.HasPrefix(userMsg, guardrailsPrefix) {
		userMsg = strings.TrimPrefix(userMsg, guardrailsPrefix)
		r.recordBlockedTurn(ctx, p, "guardrail", userMsg)
		r.completeWith(ctx, p, userMsg, 0, 0, 0)
		return nil
	}
	_ = r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "error",
		Error:     "I encountered an error processing your request. Please try rephrasing.",
		Timestamp: time.Now(),
	})
	if p.ScheduledRunID != "" && r.scheduled != nil {
		r.scheduled.MarkRunResult(ctx, p.ScheduledRunID, "", err)
	}
	logrus.WithError(err).WithField("company_id", p.CompanyID).Error("agent run failed")
	return err
}

// completeWith persists the assistant message, publishes the final event,
// and (for WA channels) sends the reply through the WhatsApp provider.
func (r *ChatRunner) completeWith(
	ctx context.Context, p queue.ChatRunPayload, response string,
	tokensIn, tokensOut int, latency time.Duration,
) {
	now := time.Now()
	assistantMsg, err := r.threads.AppendAssistantMessage(
		ctx, p.ThreadID, response, tokensIn, tokensOut, latency.Milliseconds(),
	)
	if err != nil {
		logrus.WithError(err).Warn("append assistant message")
	}
	if p.ScheduledRunID != "" && r.scheduled != nil {
		var msgID string
		if assistantMsg != nil {
			msgID = assistantMsg.ID
		}
		r.scheduled.MarkRunResult(ctx, p.ScheduledRunID, msgID, nil)
	}
	// Before the `final` event, not after (T-A2). A caller streaming
	// `GET /v1/reports/:id/events` sees `final` and re-reads the report row;
	// completing afterwards would leave a window in which a finished report
	// reports itself as still running, and close the stream on it. Ordering it
	// here is what lets the SSE bridge be a forwarder rather than a poll loop.
	if p.APIReportID != "" && r.apiReports != nil {
		r.apiReports.CompleteReport(ctx, p.APIReportID, p.ThreadID, nil)
	}
	if err := r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "final",
		Content:   response,
		Metadata:  map[string]interface{}{"latency_ms": latency.Milliseconds()},
		Timestamp: now,
	}); err != nil {
		logrus.WithError(err).Warn("publish final event")
	}

	switch p.Channel {
	case domain.ChannelWhatsApp:
		if p.PhoneNumber != "" && r.wa != nil {
			waText := stripMarkdownLinks(response)
			if err := r.wa.SendMessage(p.PhoneNumber, waText); err != nil {
				logrus.WithError(err).WithField("phone", p.PhoneNumber).Error("whatsapp send failed")
			}
		}
	case domain.ChannelDiscord:
		if p.DiscordChannelID != "" && p.CompanyID != "" {
			if err := r.bus.PublishOutbound(OutboundEvent{
				Channel:    string(domain.ChannelDiscord),
				CompanyID:  p.CompanyID,
				ChannelRef: p.DiscordChannelID,
				UserRef:    p.DiscordUserID,
				Content:    response,
			}); err != nil {
				logrus.WithError(err).WithField("company_id", p.CompanyID).Error("discord outbound publish failed")
			}
		}
	case domain.ChannelLark:
		if r.larkProv != nil && p.LarkMessageID != "" && p.CompanyID != "" {
			if err := r.larkProv.Reply(ctx, p.CompanyID, p.LarkMessageID, response); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"company_id": p.CompanyID,
					"message_id": p.LarkMessageID,
				}).Error("lark reply failed")
			}
		}
	case domain.ChannelAPI:
		// Deliberately nothing, and deliberately written out rather than
		// left to fall through the switch (T-A1). Delivery already happened:
		// the caller is holding an HTTP response open and reading the `final`
		// event this function published two statements ago. There is no
		// outbound provider to add here later — an `api` turn that tried to
		// send somewhere would be sending a second copy of an answer the
		// caller already has. Do not "fix" this empty case.
	}
}

// stripMarkdownLinks converts [text](url) to "text: url" so WhatsApp can
// auto-detect and hyperlink the raw URL.
func stripMarkdownLinks(s string) string {
	re := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	return re.ReplaceAllString(s, "$1: $2")
}

// hydrateMemory loads prior turns from Postgres into the agent's memory so
// the agent has full context even if Redis was empty or reset.
func (r *ChatRunner) hydrateMemory(ctx context.Context, agent *sdkagent.Agent, p queue.ChatRunPayload) error {
	msgs, err := r.messages.ListByThread(ctx, p.ThreadID, r.historyLimit, 0)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}

	mem := agent.GetMemory()

	// If the memory already holds messages for this conversation, skip
	// hydration to avoid duplicates.
	if convMem, ok := mem.(interfaces.ConversationMemory); ok {
		existing, err := convMem.GetConversationMessages(ctx, p.ThreadID)
		if err == nil && len(existing) > 0 {
			return nil
		}
	}

	for _, m := range msgs {
		// Skip the current user message; the agent will add it itself
		// during Run/RunStream.
		if m.Role == domain.MessageRoleUser && m.Content == p.Message && m.ID == p.UserMsgID {
			continue
		}
		sdkMsg := interfaces.Message{
			Role:    interfaces.MessageRole(m.Role),
			Content: m.Content,
		}
		if err := mem.AddMessage(ctx, sdkMsg); err != nil {
			logrus.WithError(err).Warn("hydrate memory: add message")
		}
	}
	return nil
}

// withCompanyNameContext prepends the tenant organization name so the
// agent can personalize references. If name is empty, msg is unchanged.
func withCompanyNameContext(msg, companyName string) string {
	if companyName == "" {
		return msg
	}
	return fmt.Sprintf(
		"[System context: The user's organization is named %s.]\n\n%s",
		companyName, msg,
	)
}

// withCurrencyContext prepends a short currency instruction to the user
// message so the agent knows which currency to use for formatting. If
// currency is empty, the message is returned unchanged.
func withCurrencyContext(msg, currency string) string {
	if currency == "" {
		return msg
	}
	return fmt.Sprintf(
		"[System context: The default currency is %s. Format money accordingly.]\n\n%s",
		currency, msg,
	)
}

// withRelevantTablesContext queries the per-source embedding index for the
// top-K tables semantically closest to the user's message, then prepends a
// hint listing them so the agent calls get_schema with a pre-filtered
// `tables` argument instead of dumping the full catalog. Silent skip when
// the feature is off, the source has no embeddings, or the embedding API
// fails — the agent's regular get_schema flow still works.
func (r *ChatRunner) withRelevantTablesContext(ctx context.Context, msg, userMsg string, sources []*domain.DBConnection) string {
	companyID := tenantctx.CompanyID(ctx)
	if r.embRepo == nil || r.embedCache == nil || len(sources) == 0 {
		logrus.WithField("company_id", companyID).Debug("table picker: feature off (nil repo/cache or no sources)")
		return msg
	}
	eligible := make([]*domain.DBConnection, 0, len(sources))
	for _, s := range sources {
		if s.EnableTableEmbedding {
			eligible = append(eligible, s)
		}
	}
	if len(eligible) == 0 {
		logrus.WithField("company_id", companyID).Debug("table picker: no eligible sources (enable_table_embedding=false on all)")
		return msg
	}

	embClient, err := r.embedCache.For(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).Warn("table picker: resolve embedding client failed; skipping hint")
		return msg
	}
	if embClient == nil {
		logrus.WithField("company_id", companyID).Info("table picker: no embedding client for tenant (no key); skipping hint")
		return msg
	}

	embedStart := time.Now()
	vecs, err := embClient.Embed(ctx, []string{userMsg})
	if err != nil || len(vecs) == 0 {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID,
			"model":      embClient.Model(),
		}).Warn("table picker: embed user message failed; skipping hint")
		return msg
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  companyID,
		"model":       embClient.Model(),
		"duration_ms": time.Since(embedStart).Milliseconds(),
	}).Debug("table picker: user message embedded")
	qv := vecs[0]

	var b strings.Builder
	fmt.Fprintf(&b, "[System context: Likely-relevant tables (top-%d semantic match per source):\n", r.embTopK)
	any := false
	hintedTotal := 0
	for _, s := range eligible {
		n, err := r.embRepo.CountBySource(ctx, s.ID)
		if err != nil {
			logrus.WithError(err).WithField("source_id", s.ID).Warn("table picker: CountBySource failed")
			continue
		}
		if n == 0 {
			logrus.WithField("source_id", s.ID).Warn("table picker: source has no embeddings — run reindex")
			continue
		}
		hits, err := r.embRepo.TopK(ctx, s.ID, qv, r.embTopK)
		if err != nil {
			logrus.WithError(err).WithField("source_id", s.ID).Warn("table picker: TopK query failed")
			continue
		}
		if len(hits) == 0 {
			logrus.WithField("source_id", s.ID).Info("table picker: TopK returned 0 rows")
			continue
		}
		names := make([]string, 0, len(hits))
		for _, h := range hits {
			names = append(names, h.TableName)
		}
		any = true
		hintedTotal += len(names)
		logrus.WithFields(logrus.Fields{
			"source_id": s.ID,
			"k":         r.embTopK,
			"hits":      len(hits),
			"names":     names,
		}).Info("table picker: top-k resolved")
		fmt.Fprintf(&b, " - %s: %s\n", s.ID, strings.Join(names, ", "))
	}
	if !any {
		logrus.WithField("company_id", companyID).Info("table picker: no source produced hits; hint skipped")
		return msg
	}
	b.WriteString("Pass these as the `tables` argument to get_schema for the matching source; only call get_schema unfiltered if the hint clearly misses what the user asked about.]\n\n")
	b.WriteString(msg)
	logrus.WithFields(logrus.Fields{
		"company_id":          companyID,
		"sources_with_hits":   len(eligible),
		"total_tables_hinted": hintedTotal,
	}).Info("table picker: hint injected")
	return b.String()
}

// withSourcesContext prepends the catalog of available data sources so the
// agent can pick a source_id per tool call without spending a list_sources /
// get_schema round-trip. Per-source dialect hints are returned in each
// run_sql / get_schema / create_visualization result (db_type field) so we
// don't repeat them here.
func withSourcesContext(msg string, sources []*domain.DBConnection) string {
	if len(sources) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString("[System context: Available data sources for this organization:\n")
	for _, s := range sources {
		label := s.Label
		if label == "" {
			label = "(unlabelled)"
		}
		marker := ""
		if s.IsDefault {
			marker = ", default"
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, " - %s | %s (%s%s) — %s\n", s.ID, label, s.DBType, marker, desc)
	}
	b.WriteString("Pick the appropriate source_id when calling get_schema, run_sql, or create_visualization. ")
	if len(sources) > 1 {
		b.WriteString("If unsure which source the user means, ASK before querying.")
	} else {
		b.WriteString("Only one source exists, so source_id is optional.")
	}
	b.WriteString("]\n\n")
	b.WriteString(msg)
	return b.String()
}

// trivialMessagePattern matches greetings and one-word acks in English and
// Indonesian. Anchored, case-insensitive, optional punctuation/emoji-free
// trailing chars. Question marks short-circuit so "hi?" still goes to the
// agent — though in practice questions are longer.
var trivialMessagePattern = regexp.MustCompile(
	`(?i)^\s*(hi|hello|hey|yo|hai|halo|haloo+|p|pagi|selamat pagi|siang|selamat siang|sore|selamat sore|malam|selamat malam|` +
		`thanks|thank you|thx|ok|okay|okey|noted|sip|oke|baik|terima kasih|makasih|mksh|` +
		`test|tes|ping|ok thanks|thank you so much)[\s.!,]*$`,
)

var indonesianTrivialPattern = regexp.MustCompile(
	`(?i)^\s*(hai|halo|haloo+|p|pagi|selamat pagi|siang|selamat siang|sore|selamat sore|malam|selamat malam|` +
		`sip|oke|baik|terima kasih|makasih|mksh|tes)[\s.!,]*$`,
)

// trivialReply returns a canned response when the user message is small-talk
// (greeting, one-word ack, ping). Skipping the agent on these saves the
// primary-LLM call plus all light-LLM guardrail/classifier work.
func trivialReply(msg string) (string, bool) {
	if !trivialMessagePattern.MatchString(msg) {
		return "", false
	}
	if indonesianTrivialPattern.MatchString(msg) {
		return "Halo! Ada pertanyaan tentang data atau metrik bisnis yang bisa saya bantu?", true
	}
	return "Hi! Ask me a question about your business data or metrics and I'll dig in.", true
}
