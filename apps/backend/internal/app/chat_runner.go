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
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/slack"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/tracing"
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
	// CompanyContext is the tenant's business profile, rendered (T-B1). It is
	// composed ahead of the persona: facts about the business, then the
	// instructions that act on them, and both after the shared rules.
	//
	// Empty for a company that has never described itself, which is every
	// company until somebody fills the form in — and an empty string composes
	// to a byte-identical prompt.
	CompanyContext string
	// ToolNames restricts this turn to a subset of the registry, by name.
	// Empty means every tool — the roster's rule, stated once in
	// domain.Agent.AllowsTool and not restated here.
	ToolNames []string
	// CompanyTools is the tenant MCP tools this turn's agent may call (T-M2),
	// already budget-guarded and audited by the provider. The factory appends
	// them to the static registry before it filters by ToolNames, so a
	// namespaced MCP name in an agent's allowlist resolves like any other. Empty
	// for every turn with no bound MCP server, which is the common case and
	// composes to today's exact tool list.
	CompanyTools []interfaces.Tool
	// MaxIterations is this turn's tool-calling ceiling, which the SDK enforces
	// and agentbudget reserves the last of for the answer. Per-turn because the
	// budget is: a document turn gets ForDocument's headroom, and a ceiling
	// fixed at boot would leave that headroom unreachable — the SDK would stop
	// the turn first, and its way of stopping is one more model call with no
	// instruction attached, which is the failure agentbudget exists to remove.
	//
	// Zero means the deployment's configured ceiling, which is what the
	// composition tests and any caller outside a chat turn pass.
	MaxIterations int

	// Primitives rather than a *domain.Agent on purpose: the factory lives in
	// bootstrap, and it should not have to learn a domain entity in order to
	// append a string and filter a slice. CompanyTools is the exception — it is
	// interfaces already, built and wrapped outside the factory so the wrapping
	// stays in one layer.
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

// CompanyContextLoader is the half of the business profile a turn needs
// (T-B1): the company's row, or ErrNotFound when it has none.
//
// domain.CompanyProfileRepository satisfies it. Narrowed at the consumer for
// the same reason AgentLoader is — a runner that could write the profile is a
// runner that could be asked to — and nil stays legal: a turn with no profile
// is what every turn was before this ticket, and it must keep working.
type CompanyContextLoader interface {
	GetByCompany(ctx context.Context, companyID string) (*domain.CompanyProfile, error)
}

// OutputPolicy applies the `scope: output` guardrail rules to a finished reply
// under one company's PII policy (T-07b). *guardrails.Analytics satisfies it.
//
// It exists because agent-sdk-go calls Guardrails.ProcessOutput on its blocking
// path only, and every chat turn streams — so the redaction and leak rules in
// config/guardrails.yaml have never executed on a real turn. The runner is the
// same seam rejectFabrication uses, and for the same reason: it is the last
// place that holds both the finished text and the turn's context.
type OutputPolicy interface {
	ProcessOutputFor(ctx context.Context, output string, mode guardrails.PIIMode) (string, error)
}

// CompanyPolicyLoader reads the company row a turn's output policy comes from.
// domain.CompanyRepository satisfies it.
//
// Read here rather than carried on the payload because a policy has to be the
// one in force when the answer is produced, not when the question was queued —
// a watcher briefing can sit in Redis for a minute, and an admin who has just
// switched the tenant to `strict` means the next answer, not the next answer
// enqueued after now.
type CompanyPolicyLoader interface {
	GetByID(ctx context.Context, id string) (*domain.Company, error)
}

// CompanyToolProvider builds the tenant MCP tools one turn may call (T-M2),
// already wrapped. It is read once per turn, after the scope is installed, and
// returns nil for an agent with no MCP binding — which is every turn until a
// server is bound, so nil is the fast, common path.
//
// Declared at the consumer and narrowed to one method for the same reason
// AgentLoader is: a runner that could register or review MCP servers is a runner
// that could be asked to. tools/mcp.Source satisfies it.
type CompanyToolProvider interface {
	CompanyTools(ctx context.Context, companyID string) []interfaces.Tool
}

// ToolMemory is what a turn writes about its own tool calls and reads about
// the ones before it (T-Q6).
//
// Two methods, both already the shape the concrete repository has.
// *postgres.MessageRepo satisfies it. Narrowed at the consumer and installed
// through WithToolMemory rather than added to domain.MessageRepository for the
// reason MessageLookup gives: the shared interface has six stubs across three
// packages, and none of them has an opinion about tool rows.
//
// Nil is legal and is exactly today's behaviour — the behaviour this product
// has had since threading shipped, where a follow-up turn began knowing what
// was said and nothing about what was done.
type ToolMemory interface {
	Append(ctx context.Context, m *domain.Message) error
	ListByThreadRole(ctx context.Context, threadID string, role domain.MessageRole, limit int) ([]*domain.Message, error)
	// ListRecentByThread is the NEWEST n messages, oldest-first (T-Q7).
	//
	// It rides on this interface rather than getting one of its own because it
	// is the same store, reached the same way, installed by the same call. What
	// it fixes is that domain.MessageRepository's only windowed read is
	// ascending — so "the last twenty messages" was unsayable, and hydration
	// asked for the first twenty instead.
	ListRecentByThread(ctx context.Context, threadID string, limit int) ([]*domain.Message, error)
}

// MetricCatalog is the half of the metric registry a turn reads (T-07): the
// company's enabled metrics, injected into the turn context so the agent knows
// which authoritative numbers exist before it reaches for run_sql. Narrowed at
// the consumer, and nil stays legal — a turn with no catalog behaves exactly as
// it did before the registry existed. app.MetricService satisfies it.
type MetricCatalog interface {
	ListEnabled(ctx context.Context, companyID string) ([]*domain.MetricDefinition, error)
}

// ActionCatalog is the half of the action framework a turn reads: which kinds
// this company has enabled and what each one's params must hold. Same shape and
// same reason as MetricCatalog — an agent cannot choose a capability it has not
// been told exists, and `propose_action`'s static description can only ever name
// one example. *ActionService satisfies it.
type ActionCatalog interface {
	CatalogForTurn(ctx context.Context, companyID string) ([]ActionCatalogEntry, error)
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

// WatcherFireCloser is the narrow contract ChatRunner uses to close out a
// watcher's briefing turn (T-08). Shaped like ScheduledRunMarker and installed
// the same way, because it is the same idea one step further: a turn nobody is
// watching, whose answer has to be pushed to the channels the watcher names.
// The runner owns the outbound providers but not the watcher domain, so it hands
// off both the message id and the text and lets the service deliver.
type WatcherFireCloser interface {
	CompleteFire(ctx context.Context, eventID, assistantMsgID, response string)
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
	slackProv    slack.Provider
	pool         *db.TenantConnPool
	scheduled    ScheduledRunMarker
	apiReports   APIReportCompleter
	watchers     WatcherFireCloser
	historyLimit int
	budgetFor    BudgetResolver
	actions      domain.AgentActionRepository
	roster       AgentLoader
	profiles     CompanyContextLoader
	companyTools CompanyToolProvider
	metrics      MetricCatalog
	actionCat    ActionCatalog
	outputRules  OutputPolicy
	companyPol   CompanyPolicyLoader

	// Embedding-based table picker. Both must be non-nil to inject hints;
	// otherwise the runner silently skips and the agent falls back to the
	// regular get_schema flow.
	embRepo    domain.TableEmbeddingRepository
	embedCache *llmtenant.EmbeddingCache
	embTopK    int

	// What the previous turns of this thread actually did (T-Q6). Nil disables
	// both halves — nothing is written and nothing is read — which is the
	// behaviour every turn had before this ticket.
	toolMemory   ToolMemory
	priorWorkMax int

	// The tenant's own worked examples (T-Q8). Nil is legal and is every
	// deployment until the harvester has run. Retrieval also needs embedCache
	// above — the same client the table picker uses — so a tenant with no
	// embedding credentials silently gets today's prompt.
	cookbook     domain.QueryExampleRepository
	cookbookTopK int

	// What is worth asking next (T-Q10). Off unless a caller says otherwise —
	// see WithNextSteps — because the pass adds an LLM call and its latency to
	// every turn, and a runner nobody configured must behave exactly as it did
	// before this ticket.
	nextSteps       bool
	nextStepsBudget BudgetChecker
	// How long the pass may take. Zero means nextStepsTimeout's default. It is a
	// field rather than a constant because the ticket's 5s was a guess and the
	// first live turn measured the light tier three times slower than that.
	nextStepsTimeout time.Duration
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

// WithWatchers lets the runner deliver a watcher's briefing turn to its channels
// when the turn ends (T-08). Optional: a stack with no watcher subsystem — the
// eval harness, a deployment without the worker — passes nothing, and a turn
// carrying no WatcherEventID never reaches it regardless.
func (r *ChatRunner) WithWatchers(c WatcherFireCloser) *ChatRunner {
	r.watchers = c
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

// WithOutputRules switches the `scope: output` guardrails on for streaming
// turns, under the tenant's own PII policy (T-07b).
//
// Both arguments are optional and either being nil disables the stage, which is
// the behaviour every turn has had until now — the rules were configured, tested
// by eye, and never executed. A runner with rules but no policy loader would run
// them at `strict` for everybody, which is precisely the over-redaction the
// per-company mode exists to prevent, so it is not an accepted combination.
func (r *ChatRunner) WithOutputRules(p OutputPolicy, c CompanyPolicyLoader) *ChatRunner {
	if p == nil || c == nil {
		return r
	}
	r.outputRules = p
	r.companyPol = c
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

// WithCompanyContext lets a turn read what business it is working for (T-B1).
//
// Optional in the same way WithRoster is, and for the same reason: a company
// that has never filled the form in must get exactly the agent it has today,
// and so must a deployment where this was never wired.
func (r *ChatRunner) WithCompanyContext(l CompanyContextLoader) *ChatRunner {
	r.profiles = l
	return r
}

// WithCompanyTools lets a turn call the tenant's own MCP tools (T-M2).
//
// Optional in the same way WithRoster is: a deployment with no MCP servers, or
// an agent with no binding, gets exactly the static registry it does today. The
// provider returns already-wrapped tools, so this runner never touches the
// budget guard or the audit decorator — the wrapping stays where the static
// half's does.
func (r *ChatRunner) WithCompanyTools(p CompanyToolProvider) *ChatRunner {
	r.companyTools = p
	return r
}

// WithMetrics lets a turn read the company's metric catalog (T-07). Optional:
// a deployment or a company with no metrics gets exactly today's turn, because
// withMetricsContext adds nothing for an empty catalog.
func (r *ChatRunner) WithMetrics(c MetricCatalog) *ChatRunner {
	r.metrics = c
	return r
}

// WithActionCatalog tells a turn which actions the company has enabled. Optional
// in the same way WithMetrics is: a company that has enabled none gets exactly
// today's turn, because withActionsContext adds nothing for an empty catalog.
func (r *ChatRunner) WithActionCatalog(c ActionCatalog) *ChatRunner {
	r.actionCat = c
	return r
}

// WithLark attaches a Lark outbound provider so the runner can post replies
// for chat:run tasks on the Lark channel. Returning the receiver mirrors
// WithTablePicker so the worker can chain configuration on construction.
func (r *ChatRunner) WithLark(p lark.Provider) *ChatRunner {
	r.larkProv = p
	return r
}

// WithSlack attaches a Slack outbound provider so the runner can post replies
// for chat:run tasks on the Slack channel. Mirrors WithLark.
func (r *ChatRunner) WithSlack(p slack.Provider) *ChatRunner {
	r.slackProv = p
	return r
}

// WithToolMemory lets a turn read what the turns before it did, and record
// what it did for the turns after (T-Q6).
//
// turns caps how many previous turns' worth of digests are carried. A negative
// value means the default of 3 — three because the block competes with the
// user's own question for the model's attention, and a conversation's
// fourth-to-last turn is rarely what a follow-up is following up on.
//
// **Zero is meaningful and is not the default**: it writes the digests and
// reads none of them. That is the setting this feature is measured with — the
// same deployment, the same rows accumulating, with only the injection off —
// and collapsing it into the default would make the comparison unrunnable.
//
// Optional in the way WithRoster is: a nil memory writes nothing and reads
// nothing, and every turn behaves exactly as it did before.
func (r *ChatRunner) WithToolMemory(m ToolMemory, turns int) *ChatRunner {
	if m == nil {
		return r
	}
	if turns < 0 {
		turns = 3
	}
	r.toolMemory = m
	r.priorWorkMax = turns
	return r
}

// WithCookbook shows a turn the tenant's own worked examples (T-Q8).
//
// topK <= 0 falls back to 3. Three because each example carries a question and
// up to 800 characters of SQL, and the block is competing with the source
// catalog, the metric catalog, the table hint and the user's actual question
// for the model's attention. A cookbook that fills the context is a cookbook
// that makes answers worse.
//
// Optional in the way WithTablePicker is, and inert without it: retrieval
// needs the embedding cache, so a runner given a cookbook and no embeddings
// silently skips.
func (r *ChatRunner) WithCookbook(repo domain.QueryExampleRepository, topK int) *ChatRunner {
	if repo == nil {
		return r
	}
	if topK <= 0 {
		topK = 3
	}
	r.cookbook = repo
	r.cookbookTopK = topK
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
		// No suggestions on a greeting. The small-talk path never called the model
		// or a tool, so the agent has discovered nothing to suggest from — and
		// three chips under "hello" is the product talking to itself.
		r.completeWith(ctx, p, reply, 0, 0, 0, nil)
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

	// One turn, one source memory. After the scope, because what it may recall
	// is bounded by what the scope allows, and installed here so that every
	// tool call the turn makes shares it — a source resolved by get_schema is
	// what the tool call two calls later is missing
	// (coverage/eval-sprint1.md §4).
	ctx = tools.WithTurnSource(ctx)

	// The tenant's own MCP tools for this turn (T-M2), resolved from the scope
	// just installed: empty binding means none, so this is nil for every turn
	// until a server is bound. Built here, before the factory, because it needs
	// the context — the scope, the tenant, the deadline — and the factory takes
	// only what a turn composed from.
	var companyTools []interfaces.Tool
	if r.companyTools != nil {
		companyTools = r.companyTools.CompanyTools(ctx, p.CompanyID)
	}

	// The turn's own span (T-17), parent of every span below it.
	//
	// Started here rather than after the agent is built, which is where it used
	// to be: `hydrateMemory` runs between those two points, so its span had no
	// parent and Jaeger filed it as a separate trace — the first waterfall read
	// (2026-08-08) showed `agent.memory.hydrate` alone in one trace and the turn
	// in another. The user waits for LLM resolution, agent construction and
	// hydration exactly as they wait for the model, so covering them is also the
	// more honest reading of "the turn as the user experiences it".
	// The producer's trace, restored before the span is started (T-17b).
	//
	// Without this the API's spans and the worker's are two unrelated traces of
	// one turn, so the interval a slow turn is most often blamed on — the wait
	// in the queue — was the one interval no waterfall could show. An absent or
	// unreadable carrier leaves ctx alone and the turn starts its own trace,
	// which is what every task queued before this field existed carries.
	ctx = tracing.Extract(ctx, p.Trace)
	ctx, turnSpan := tracing.Turn(ctx, p.CompanyID, p.ThreadID, string(p.Channel))
	defer turnSpan.End()
	tracing.QueueWait(turnSpan, p.EnqueuedAt)

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
		CompanyContext: r.companyContext(ctx, p.CompanyID),
		Persona:        personaOf(agentRow),
		ToolNames:      toolNamesOf(agentRow),
		CompanyTools:   companyTools,
		// The same ceiling the tracker installed above. Handing the SDK a
		// different number is how a document turn's headroom went unused.
		MaxIterations: budget.MaxIterations,
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
	agentMsg := withLanguageReminder(p.Message)
	agentMsg = withCompanyNameContext(agentMsg, p.CompanyName)
	agentMsg = withCurrencyContext(agentMsg, p.DefaultCurrency)
	agentMsg = withSourcesContext(agentMsg, sources)
	agentMsg = r.withMetricsContext(ctx, agentMsg, p.CompanyID)
	agentMsg = r.withActionsContext(ctx, agentMsg, p.CompanyID)
	// One embedding call per turn, shared by the table picker and the cookbook
	// (T-Q8). Both ask the same question of the same sentence; doing it twice
	// would be two network round trips before the model is called at all.
	questionVec := r.questionVector(ctx, p.CompanyID, p.Message)
	agentMsg = r.withCookbookContext(ctx, agentMsg, questionVec)
	agentMsg = r.withRelevantTablesContext(ctx, agentMsg, questionVec, sources)
	// Last, so it sits closest to the user's own words — above only the
	// language reminder, whose position is itself the fix for a measured
	// regression (T-Q6). What this conversation has already done is the block
	// most likely to make the difference between one tool call and four, and
	// the table hint immediately below it is advice about work that may now be
	// unnecessary.
	agentMsg = r.withPriorWorkContext(ctx, agentMsg, p.ThreadID)
	// Above the prior-work block, because it is background rather than
	// instruction: what the conversation is about, then what has already been
	// done about it (T-Q7). Fires only on threads longer than the memory
	// window, which is where the agent would otherwise have forgotten the
	// opening.
	agentMsg = r.withThreadSummaryContext(ctx, agentMsg, p.ThreadID)

	// Try streaming first; fall back to blocking Run if the LLM doesn't
	// support it.
	var response string
	var streaming bool
	// Filled by the streaming path with every number the data tools returned
	// (T-Q9). Empty after a blocking run, which produces no tool events at all
	// — CheckGrounding reports Checked=false for that and says nothing.
	var returnedNumbers []float64
	if agent.GetLLM().SupportsStreaming() {
		sp := p
		sp.Message = agentMsg
		if streamResp, err := r.runStream(ctx, agent, sp, &returnedNumbers); err == nil {
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
	metrics.Default().RecordTurn(latency)
	// Kept so the post-turn chain can tell an agent's own answer from one a gate
	// wrote in its place. Suggesting what to ask next on top of "I could not
	// complete that" is the product being cheerful about its own failure (T-Q10).
	agentWrote := response
	response = r.rejectFabrication(ctx, p, response, tracker)
	// After the fabrication gate and before the redaction: this measures the
	// text the agent actually produced, and a reply the gate has already
	// replaced is not one whose figures say anything about the agent (T-Q9).
	ungrounded := r.checkGrounding(ctx, p, response, returnedNumbers)
	// After the fabrication check, never before it: that check reads the figures
	// in the reply and compares them against the turn's evidence, and a redaction
	// that has already blanked part of the text would have it judging a sentence
	// the agent did not write.
	response = r.applyOutputRules(ctx, p, response)
	// Last, and after the redaction on purpose: `strict` blanking an entire
	// short reply reaches the user as the same blank message, so the guard has
	// to see what completeWith is about to persist rather than what the agent
	// produced.
	response = r.rescueEmptyReply(ctx, p, response, tracker, streaming)
	// Last in the post-turn chain and before completeWith, so the suggestions are
	// written against the text the user will actually read — the redacted one, the
	// rescued one — rather than against what the agent first produced (T-Q10).
	snap := tracker.Snapshot()
	steps := r.suggestNextSteps(ctx, p, response,
		heldToolNames(agent.GetTools()), snap.Tools,
		response != agentWrote)
	r.completeWith(ctx, p, response, 0, 0, latency, steps)
	// One line per turn, carrying what the turn cost and what it may have got
	// wrong (T-Q11). `ungrounded` is here rather than only in checkGrounding's
	// own Warn because a turn is found by its completion line: a rate nobody
	// can filter for is a rate nobody reads.
	logrus.WithFields(logrus.Fields{
		"company_id": p.CompanyID,
		"thread_id":  p.ThreadID,
		"message_id": p.UserMsgID,
		"channel":    p.Channel,
		"latency_ms": latency.Milliseconds(),
		"tool_calls": snap.ToolCalls,
		"data_rows":  snap.DataRows,
		"ungrounded": ungrounded,
		"next_steps": len(steps),
	}).Info("turn completed")
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

// companyContext renders the tenant's business profile for this turn (T-B1),
// or returns empty.
//
// Empty is the answer in every failure: no loader wired, no profile, or a
// lookup that failed. A company description is context that makes an answer
// better, never the thing that makes an answer possible — refusing to run a
// turn because a settings row could not be read would trade a slightly less
// informed answer for no answer at all. The failed read is logged so it is
// visible; unlike the roster's, it is not fail-closed, because nothing about
// this block is a permission.
//
// One read per turn, beside the agent lookup — not per tool call, not in a
// middleware.
func (r *ChatRunner) companyContext(ctx context.Context, companyID string) string {
	if r.profiles == nil {
		return ""
	}
	p, err := r.profiles.GetByCompany(ctx, companyID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("business profile lookup failed; running this turn without the company block")
		}
		return ""
	}
	block, truncated := p.ContextBlock()
	if truncated {
		// The tenant sees this in the dashboard too. Logged here as well
		// because the turn is where it costs something, and "the agent did not
		// know X" is asked about a turn, not about a form.
		logrus.WithFields(logrus.Fields{
			"company_id": companyID, "cap_tokens": domain.CompanyContextMaxTokens,
		}).Info("business profile truncated to the context cap")
	}
	return block
}

// scopeOf, personaOf and toolNamesOf are the three things a turn takes from an
// agent. Separate nil-tolerant functions rather than methods, because the
// caller's `nil` is the ordinary case and a method set that has to be
// nil-checked at three call sites is three chances to forget.
func scopeOf(a *domain.Agent) agentscope.Scope {
	if a == nil {
		return agentscope.Scope{}
	}
	return agentscope.Scope{
		AgentID:      a.ID,
		Name:         a.Name,
		SourceIDs:    a.SourceIDs,
		MCPServerIDs: a.MCPServerIDs,
	}
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
		ToolCalls:        snap.ToolCalls,
		DataCalls:        snap.DataCalls,
		DataRows:         snap.DataRows,
		DeliverableCalls: snap.DeliverableCalls,
		EmptyResults:     snap.EmptyResults,
		Exhausted:        snap.Exhausted,
		Reason:           snap.Reason,
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
		// A figure that passed the check above is tool-derived; it is not
		// necessarily printed at the right magnitude. "IDR 3,863,405,700
		// (approximately $3.86 million)" is the observed shape, twice — once in
		// chat and once in a watcher briefing a customer receives unprompted.
		// Correcting the unit is arithmetic over digits already in the reply.
		corrected, fixes := guardrails.CheckScale(response)
		if len(fixes) > 0 {
			logrus.WithFields(logrus.Fields{
				"company_id":  p.CompanyID,
				"thread_id":   p.ThreadID,
				"corrections": fmt.Sprint(fixes),
			}).Warn("a restatement disagreed with the figure it restated; the unit was corrected")
			return corrected
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

// applyOutputRules runs the `scope: output` guardrails over the finished reply
// (T-07b): the redaction rules, under the tenant's own policy, and the
// system-prompt leak rule, which no policy switches off.
//
// It shares rejectFabrication's caveat and its answer. On a streaming turn the
// unredacted text has already reached the dashboard as deltas; the final event
// and the persisted message carry the processed version, so the UI settles on
// it, and every push channel — WhatsApp, Discord, Lark, a watcher briefing —
// only ever sees the final and so never sees the raw text at all.
//
// A turn whose company row cannot be read runs at `strict`. That is the
// recoverable failure: a tenant on `contact_ok` loses email addresses from one
// answer during a database blip, where the other direction prints personal data
// the tenant asked us not to.
func (r *ChatRunner) applyOutputRules(ctx context.Context, p queue.ChatRunPayload, response string) string {
	if r.outputRules == nil || response == "" {
		return response
	}

	// Started after the nil check so a deployment with no output rules records
	// no span for work it did not do. A blocked reply is not an error here —
	// the rule fired as designed — so the span carries the outcome as an
	// attribute rather than an error.
	ctx, span := tracing.Step(ctx, "guardrails.output")
	defer span.End()

	mode := guardrails.PIIStrict
	if c, err := r.companyPol.GetByID(ctx, p.CompanyID); err != nil {
		logrus.WithError(err).WithField("company_id", p.CompanyID).
			Warn("pii policy lookup failed; this turn's output is redacted at strict")
	} else {
		mode = guardrails.PIIMode(c.PIIRedactionMode).Normalize()
	}

	processed, err := r.outputRules.ProcessOutputFor(ctx, response, mode)
	if err != nil {
		// A blocking output rule fired. The error text is the rule's own message
		// to the user, which is what the caller gets instead of the reply.
		logrus.WithFields(logrus.Fields{
			"company_id":    p.CompanyID,
			"thread_id":     p.ThreadID,
			"pii_mode":      string(mode),
			"blocked_reply": response,
		}).Warn("reply was blocked by an output guardrail; replaced with the rule's message")
		r.recordBlockedTurn(ctx, p, "final_answer", "reply was blocked by an output guardrail")
		tracing.Outcome(span, "blocked")
		return err.Error()
	}
	if processed != response {
		tracing.Outcome(span, "redacted")
		// Not the text — that is what was redacted, and logging it would put the
		// personal data in the log the redaction just took out of the answer.
		logrus.WithFields(logrus.Fields{
			"company_id": p.CompanyID,
			"thread_id":  p.ThreadID,
			"pii_mode":   string(mode),
		}).Info("output guardrails redacted part of the reply")
	}
	return processed
}

// rescueEmptyReply turns a blank answer into a sentence, and records the turn
// that produced one.
//
// The failure it covers is not hypothetical and not an error path: twice in 58
// scored turns of the 2026-08-14 run the agent called its tools, the tools
// succeeded, and the reply was the empty string — once after a `create_dashboard`
// that actually built a dashboard nothing was said about
// (docs/coverage/eval-q1.md). No component upstream of here looks at the reply
// and asks whether there is one.
//
// **The log line is the diagnosis and the replacement is the mitigation**, and
// they are separate on purpose. `streaming` is the field that matters most:
// the two candidate mechanisms are a final provider message with no text and a
// reply lost in the delta assembly, and only the streaming path has the second.
// The rest of the snapshot says whether the turn did any work, which is what
// decides which sentence the user gets.
//
// The audit row uses its own tool name rather than `final_answer`. This is not
// a guardrail firing — nothing was refused — and counting it as one would
// corrupt the only number that says how often the product refuses to answer.
func (r *ChatRunner) rescueEmptyReply(
	ctx context.Context, p queue.ChatRunPayload, response string,
	tracker *agentbudget.Tracker, streaming bool,
) string {
	snap := tracker.Snapshot()
	replacement, empty := guardrails.CheckEmptyReply(response, guardrails.TurnEvidence{
		ToolCalls:        snap.ToolCalls,
		DataCalls:        snap.DataCalls,
		DataRows:         snap.DataRows,
		DeliverableCalls: snap.DeliverableCalls,
		EmptyResults:     snap.EmptyResults,
		Exhausted:        snap.Exhausted,
		Reason:           snap.Reason,
		Tools:            snap.Tools,
	}, p.Message)
	if !empty {
		return response
	}

	logrus.WithFields(logrus.Fields{
		"company_id":  p.CompanyID,
		"thread_id":   p.ThreadID,
		"streaming":   streaming,
		"tool_calls":  snap.ToolCalls,
		"data_calls":  snap.DataCalls,
		"data_rows":   snap.DataRows,
		"tool_errors": snap.ToolErrors,
		"exhausted":   snap.Exhausted,
		"reason":      snap.Reason,
		"tools":       strings.Join(snap.Tools, ","),
		"elapsed_ms":  snap.Elapsed.Milliseconds(),
	}).Error("the turn produced an empty reply; replaced with a message that says what ran")
	r.recordBlockedTurn(ctx, p, "empty_reply", "the turn finished with no reply text")
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
	// A watcher fire is unattended, like a schedule — nobody was at the keyboard.
	// It gets its own actor kind so the audit log can tell an alert's queries
	// apart from a cron report's (T-08).
	if p.WatcherEventID != "" {
		return string(domain.ActorKindWatcher), p.WatcherEventID
	}
	// An API key outranks any user reference on the payload for the same
	// reason a schedule does: the turn ran because a script called us, and the
	// tenant's own `user_ref` on a /v1 chat request is a label they chose, not
	// an identity we authenticated (T-13).
	if p.APIKeyID != "" {
		return string(domain.ActorKindAPIKey), p.APIKeyID
	}
	// A widget turn is a person, but not one of ours: the tenant's backend
	// asserted who they are and we verified the assertion, not the human. It
	// gets its own kind rather than joining the ActorKindUser list below,
	// because every ref in that list is an identity we authenticated ourselves
	// and `embed_user_ref` is a name a tenant chose. The ref is that name; the
	// key that vouched for it is on the payload and is what an admin revokes
	// when the vouching turns out to be wrong (T-20).
	if p.EmbedUserRef != "" {
		return string(domain.ActorKindEmbed), p.EmbedUserRef
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
func (r *ChatRunner) runStream(ctx context.Context, agent *sdkagent.Agent, p queue.ChatRunPayload, groundTruth *[]float64) (string, error) {
	events, err := agent.RunStream(ctx, p.Message)
	if err != nil {
		return "", err
	}

	// What this turn did, for the turns after it (T-Q6). Collected here because
	// this is the only place both an argument list and its result are in hand:
	// the audit decorator sees them too, but it writes a control-plane row the
	// agent never reads, and the SDK does not hand the pair back anywhere else.
	//
	// The consequence is that the blocking fallback path below records nothing.
	// That is honest rather than convenient — a turn that fell back to blocking
	// produced no tool events at all — and it is the rarer path: every provider
	// this deployment supports streams.
	var digests []ToolDigest
	defer func() { r.rememberToolWork(ctx, p, digests) }()

	// The numbers the data tools returned, for the grounding check (T-Q9).
	// Assigned to the runner-independent slice the caller reads through the
	// closure below, because runStream returns only the text.
	var returnedNumbers []float64
	defer func() { *groundTruth = returnedNumbers }()

	// The reply, kept per iteration rather than concatenated (T-Q11). What the
	// reader watches is unchanged — every delta is published below — but what
	// this function returns, and therefore what is persisted, is the last
	// iteration that produced prose.
	fullResponse := newAnswerBuffer()
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
			fullResponse.Write(evt.Metadata, evt.Content)
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
				// The digest, built from the arguments and the result together
				// (T-Q6). Arguments are re-parsed here rather than carried from
				// the tool_call event above, because the two events are not
				// guaranteed to pair up one-to-one in the stream and matching
				// them would be a state machine to maintain for no gain.
				callArgs := map[string]interface{}{}
				if evt.ToolCall.Arguments != "" {
					_ = json.Unmarshal([]byte(evt.ToolCall.Arguments), &callArgs)
				}
				// The raw result travels with the parsed one because a tool that
				// returned a Go error has no parsed one: the SDK renders it as a
				// plain string, which unmarshals into an empty map and used to
				// leave a digest saying nothing happened wrong (T-Q12).
				digests = append(digests,
					BuildToolDigest(evt.ToolCall.Name, callArgs, res, evt.ToolCall.Result))

				// Every number this turn's data tools actually returned (T-Q9),
				// for the grounding check after the answer is written. Only the
				// data tools: a Metabase card id and a document's byte count are
				// numbers no reply should be quoting as a business figure, and
				// counting them as evidence would ground a fabrication.
				if agentbudget.IsDataTool(evt.ToolCall.Name) && len(returnedNumbers) < maxGroundingNumbers {
					returnedNumbers = append(returnedNumbers,
						guardrails.CollectNumbers(res, maxGroundingNumbers-len(returnedNumbers))...)
				}

				_ = r.bus.Publish(p.ThreadID, ChatEvent{
					JobID:     p.UserMsgID,
					ThreadID:  p.ThreadID,
					Type:      "tool_result",
					ToolCall:  &ToolCallEvent{Name: evt.ToolCall.Name, Result: res},
					Timestamp: time.Now(),
				})
				// A proposed action gets its own event on top of the tool_result
				// (T-11), so the dashboard can render an inline approval card in
				// the stream without pattern-matching on a tool name it otherwise
				// treats opaquely. Only a proposal awaiting a human decision needs
				// one: an admin-opt-out kind (status executed/failed) has nothing
				// to approve, and the tool_result already carries its outcome.
				if evt.ToolCall.Name == "propose_action" && res["status"] == string(domain.InvocationProposed) {
					_ = r.bus.Publish(p.ThreadID, ChatEvent{
						JobID:     p.UserMsgID,
						ThreadID:  p.ThreadID,
						Type:      "action_proposed",
						Metadata:  res,
						Timestamp: time.Now(),
					})
				}
			}

		case interfaces.AgentEventError:
			if evt.Error != nil {
				errMsg := evt.Error.Error()
				// Guardrails rejections should be presented as normal
				// assistant messages, not raw errors.
				const guardrailsPrefix = "guardrails error: "
				if strings.HasPrefix(errMsg, guardrailsPrefix) {
					userMsg := strings.TrimPrefix(errMsg, guardrailsPrefix)
					fullResponse.Replace(userMsg)
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

	// A turn whose earlier iterations narrated before calling a tool is the
	// shape that produced a stored answer stating a figure no tool returned
	// (T-Q11). Logged rather than counted: what is worth knowing is that this
	// turn's record is narrower than its stream, and which turn it was.
	if dropped := fullResponse.Dropped(); dropped > 0 {
		logrus.WithFields(logrus.Fields{
			"company_id":    p.CompanyID,
			"thread_id":     p.ThreadID,
			"message_id":    p.UserMsgID,
			"dropped_chars": dropped,
			"iterations":    lastIteration,
		}).Info("pre-tool prose dropped from the stored reply; the record is the last iteration's answer")
	}
	return fullResponse.String(), nil
}

// maxGroundingNumbers bounds how many returned values the grounding check
// holds (T-Q9). The comparison is quadratic in this slice, and a hundred-row
// result with twenty columns is two thousand numbers. Two hundred covers every
// aggregate and the head of any result set, which is what a reply quotes.
const maxGroundingNumbers = 200

// checkGrounding measures whether the figures in a finished reply are the
// figures the tools returned (T-Q9).
//
// **It reports and does not rewrite.** rejectFabrication above is the gate; it
// asks whether evidence exists, and a reply stating "roughly 4.1 billion" over
// a query that returned 3,863,405,700 passes it — evidence existed, rows came
// back, the magnitudes agree. This asks the narrower question and writes the
// answer to the log, because an analyst's reply legitimately carries numbers no
// query returned (a delta, a percentage, a per-unit figure) and blocking those
// is the guardrail-overreach cycle this repo has already been through.
//
// So it is an instrument. The wrong-but-nonempty rate is currently not merely
// unenforced but unmeasured, and nothing can be tightened before it is counted.
//
// **It is counted now (T-Q11).** One Warn line per occurrence was what made a
// persisted answer stating 1,667 against a true 300 invisible for a week: a log
// line nothing reads is not a measurement. The count lands on the process
// counters and on the turn's own span, so a turn can be found by it.
func (r *ChatRunner) checkGrounding(ctx context.Context, p queue.ChatRunPayload, response string, returned []float64) int {
	rep := guardrails.CheckGrounding(response, returned)
	if rep.Clean() {
		return 0
	}
	n := len(rep.Ungrounded)
	metrics.Default().RecordUngroundedFigures(n)
	tracing.Count(ctx, "ungrounded_figures", n)
	logrus.WithFields(logrus.Fields{
		"company_id":       p.CompanyID,
		"thread_id":        p.ThreadID,
		"message_id":       p.UserMsgID,
		"stated":           len(rep.Stated),
		"ungrounded":       fmt.Sprint(rep.Ungrounded),
		"returned_numbers": len(returned),
	}).Warn("reply states a figure no tool result contains; not blocked, recorded for review")
	return n
}

// rememberToolWork records what this turn did, as one `role: tool` message
// (T-Q6).
//
// One row per turn rather than one per call: the follow-up reads them as a
// block, and a turn that made seven calls would otherwise put seven rows
// between two sentences of conversation — which is the same crowding-out that
// makes `historyLimit` a worse number than it looks.
//
// Best-effort throughout. A turn that answered correctly and failed to write
// its own memory is a turn that answered correctly; failing it here would
// trade a delivered answer for a bookkeeping error. Detached from the request
// context for the audit decorator's reason: the turn this describes may
// already be over.
func (r *ChatRunner) rememberToolWork(ctx context.Context, p queue.ChatRunPayload, digests []ToolDigest) {
	if r.toolMemory == nil || len(digests) == 0 {
		return
	}
	kept := DedupeDigests(digests)
	if len(kept) == 0 {
		return
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	msg := &domain.Message{
		ThreadID: p.ThreadID,
		Role:     domain.MessageRoleTool,
		Content:  EncodeDigests(kept),
		// Metadata rather than ToolCalls: the dashboard renders `tool_calls` on
		// a message as expandable cards, and these rows are not shown at all —
		// the assistant turn beside them already carries the cards a reader
		// wants. This is memory, not transcript.
		Metadata: map[string]interface{}{
			"kind":       "tool_digest",
			"turn_msg":   p.UserMsgID,
			"tool_calls": len(kept),
		},
	}
	if err := r.toolMemory.Append(writeCtx, msg); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": p.CompanyID,
			"thread_id":  p.ThreadID,
		}).Warn("tool memory write failed; the next turn will start without this turn's work")
	}
}

// priorWork reads what earlier turns of this thread did (T-Q6).
//
// Empty is the answer for every failure and for the first turn of every
// conversation, which is the common case: nothing here is a permission, and a
// turn that cannot read its own history must still answer.
func (r *ChatRunner) priorWork(ctx context.Context, threadID string) []ToolDigest {
	// priorWorkMax == 0 is the write-but-do-not-read setting — see
	// WithToolMemory. Checked before the query rather than after, so the
	// measurement it exists for does not also measure a database read.
	if r.toolMemory == nil || r.priorWorkMax <= 0 || threadID == "" {
		return nil
	}
	rows, err := r.toolMemory.ListByThreadRole(ctx, threadID, domain.MessageRoleTool, r.priorWorkMax)
	if err != nil {
		logrus.WithError(err).WithField("thread_id", threadID).
			Warn("tool memory read failed; this turn starts without the earlier turns' work")
		return nil
	}
	var out []ToolDigest
	for _, row := range rows {
		out = append(out, DecodeDigests(row.Content)...)
	}
	// Deduped across turns as well as within one: a conversation that has read
	// the same schema in three consecutive turns should say so once.
	return DedupeDigests(out)
}

// withThreadSummaryContext prepends the conversation's rolling summary, but
// only when the turn's memory does not already hold the whole thread (T-Q7).
//
// **The gap.** `conversation_threads.summary` has existed since the threading
// migration and the agent has never seen it: it is read by the relatedness
// classifier deciding whether a new message continues the thread, and by the
// title generator. Meanwhile `historyLimit` drops everything older than twenty
// messages, so on a long conversation the agent forgets the opening — which is
// usually where the user said what they were actually trying to find out.
// The summary of that opening was sitting in a column two feet away.
//
// **Only when it adds something.** A thread inside the window is already fully
// in memory, and pasting a summary of messages the model can read verbatim
// would spend context restating them, less accurately. So this fires on long
// threads alone, which are also the only threads where it could help.
func (r *ChatRunner) withThreadSummaryContext(ctx context.Context, msg, threadID string) string {
	if r.threadRepo == nil || threadID == "" {
		return msg
	}
	// Cheap and exact: if the thread is no longer than what hydration keeps,
	// there is nothing older to summarise. Counting is one indexed query, and
	// getting this wrong in the other direction — injecting always — is how a
	// two-turn conversation ends up with a paragraph about itself in its
	// prompt.
	count, err := r.messageCount(ctx, threadID)
	if err != nil {
		// Split from the length test so a store without CountByThread is not
		// silently the same event as a short thread: that error disables T-Q7
		// entirely, on every thread, and looks exactly like the feature working.
		logrus.WithError(err).WithField("thread_id", threadID).
			Debug("thread length unavailable; this turn runs without the summary")
		return msg
	}
	if count <= r.historyLimit {
		return msg
	}
	thread, err := r.threadRepo.GetByID(ctx, threadID)
	if err != nil {
		logrus.WithError(err).WithField("thread_id", threadID).
			Debug("thread summary lookup failed; this turn runs without it")
		return msg
	}
	summary := strings.TrimSpace(thread.Summary)
	if summary == "" {
		return msg
	}
	// Logged for the same reason withPriorWorkContext logs: nothing downstream
	// records the composed user message, so without this line the only way to
	// tell an injected summary from a skipped one is to read the model's reply
	// and guess. Every branch above returns silently, and three of them are
	// indistinguishable from "the thread is short" — which is what made this
	// gate unobservable when it was first run (2026-08-14).
	logrus.WithFields(logrus.Fields{
		"thread_id":      threadID,
		"summary_chars":  len(summary),
		"message_count":  count,
		"history_window": r.historyLimit,
	}).Debug("thread summary injected")
	return "[System context: This conversation is longer than what you can see above. " +
		"Summary of the whole conversation so far, including the parts no longer in view: " +
		summary + "\nTreat it as background about what the user is working towards. " +
		"It contains no figures you may quote — re-run a query for any number you state.]\n\n" + msg
}

// messageCount reads how long the thread is. Narrowed inline because
// domain.MessageRepository carries CountByThread and the runner holds the
// interface already; a store without it simply skips the summary.
func (r *ChatRunner) messageCount(ctx context.Context, threadID string) (int, error) {
	type counter interface {
		CountByThread(ctx context.Context, threadID string) (int, error)
	}
	c, ok := r.messages.(counter)
	if !ok {
		return 0, errors.ErrUnsupported
	}
	return c.CountByThread(ctx, threadID)
}

// withPriorWorkContext prepends what this conversation has already done.
//
// Composed into the user message beside the source catalog and the table hint,
// rather than replayed into the SDK's memory — see RenderPriorWork for why
// that is a protocol constraint and not a preference.
func (r *ChatRunner) withPriorWorkContext(ctx context.Context, msg, threadID string) string {
	block := RenderPriorWork(r.priorWork(ctx, threadID))
	if block == "" {
		return msg
	}
	logrus.WithFields(logrus.Fields{
		"thread_id":   threadID,
		"block_chars": len(block),
	}).Debug("prior-turn tool work injected")
	return block + msg
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
		// A refusal is a guardrail's message, not an answer, so it carries no
		// suggestions — the same rule the post-turn chain applies to a reply the
		// fabrication gate replaced.
		r.completeWith(ctx, p, userMsg, 0, 0, 0, nil)
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
	tokensIn, tokensOut int, latency time.Duration, steps []domain.NextStep,
) {
	now := time.Now()
	// The suggestions ride the message's metadata column, which already exists
	// and is already marshalled at both ends (T-Q10). Nil steps produce a nil map
	// and the row is written exactly as it was before this ticket.
	meta := nextStepsMetadata(steps)
	assistantMsg, err := r.threads.AppendAssistantMessage(
		ctx, p.ThreadID, response, tokensIn, tokensOut, latency.Milliseconds(), meta,
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
	// A watcher's briefing turn (T-08): record the assistant message on the
	// event and push the answer to every channel the watcher names. After the
	// message is persisted (so the id is real) and before the `final` event is
	// not required — delivery to WhatsApp/Discord/Lark is proactive and
	// independent of the dashboard stream.
	if p.WatcherEventID != "" && r.watchers != nil {
		var msgID string
		if assistantMsg != nil {
			msgID = assistantMsg.ID
		}
		r.watchers.CompleteFire(ctx, p.WatcherEventID, msgID, response)
	}
	// The same slice on the `final` event, beside latency_ms. No new event type,
	// so every consumer that already reads `final` — the dashboard, the widget,
	// `/v1` — gets the suggestions for free and none of them has to learn
	// anything to keep working (T-Q10).
	finalMeta := map[string]interface{}{"latency_ms": latency.Milliseconds()}
	if len(steps) > 0 {
		finalMeta["next_steps"] = steps
	}
	if err := r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "final",
		Content:   response,
		Metadata:  finalMeta,
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
	case domain.ChannelSlack:
		if r.slackProv != nil && p.SlackChannelID != "" && p.CompanyID != "" {
			if err := r.slackProv.Reply(ctx, p.CompanyID, p.SlackChannelID, p.SlackThreadTS, response); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"company_id": p.CompanyID,
					"channel_id": p.SlackChannelID,
					"thread_ts":  p.SlackThreadTS,
				}).Error("slack reply failed")
			}
		}
	case domain.ChannelWidget:
		// Deliberately nothing, for ChannelAPI's reason one surface further out
		// (T-20). Delivery is the WebSocket the browser is already attached to,
		// and the `final` event this function published two statements ago is
		// what travels down it. There is no outbound provider to add here
		// later: a widget turn that tried to send somewhere would be sending a
		// second copy of an answer already on screen. Do not "fix" this empty
		// case either.
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
func (r *ChatRunner) hydrateMemory(ctx context.Context, agent *sdkagent.Agent, p queue.ChatRunPayload) (err error) {
	ctx, span := tracing.Step(ctx, "memory.hydrate")
	defer func() { tracing.End(span, err) }()

	// Fetched wider than the window and trimmed after filtering (T-Q6). The
	// thread now carries a `role: tool` row per turn that hydration must skip,
	// and a plain LIMIT would spend roughly a third of the window on rows this
	// loop then discards — quietly shortening the conversation the agent
	// remembers, on exactly the long threads where remembering matters.
	fetch := r.historyLimit
	if r.toolMemory != nil {
		fetch *= 2
	}

	// The NEWEST `fetch` messages, not the oldest (T-Q7).
	//
	// ListByThread is ascending with a LIMIT, so the call this replaces —
	// `ListByThread(id, 20, 0)` — was the first twenty messages of the thread.
	// On any conversation longer than the window, hydration was replaying the
	// opening and dropping everything the user had said since. Below twenty
	// messages the two reads are identical, which is why every test thread and
	// every demo hid it.
	//
	// The old call is kept as the fallback for a runner with no tool memory
	// installed: the eval harness and the runner tests build one, their threads
	// are short, and the two reads agree there.
	var msgs []*domain.Message
	if r.toolMemory != nil {
		msgs, err = r.toolMemory.ListRecentByThread(ctx, p.ThreadID, fetch)
	} else {
		msgs, err = r.messages.ListByThread(ctx, p.ThreadID, fetch, 0)
	}
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

	added := 0
	for _, m := range msgs {
		// The window is over conversation turns, not over rows — see the fetch
		// above. Enforced here rather than by the query because only this loop
		// knows which rows count.
		if added >= r.historyLimit {
			break
		}
		// Skip the current user message; the agent will add it itself
		// during Run/RunStream.
		if m.Role == domain.MessageRoleUser && m.Content == p.Message && m.ID == p.UserMsgID {
			continue
		}
		// Tool-digest rows never enter the provider's message list (T-Q6). A
		// tool-result message is only valid immediately after the assistant
		// message whose tool_call_id it answers, and a row synthesised in a
		// previous turn has no such id — Anthropic and OpenAI both reject the
		// sequence, so replaying these would break every follow-up turn on the
		// thread rather than inform it. They reach the model as the context
		// block RenderPriorWork composes instead.
		if m.Role == domain.MessageRoleTool {
			continue
		}
		sdkMsg := interfaces.Message{
			Role:    interfaces.MessageRole(m.Role),
			Content: m.Content,
		}
		if err := mem.AddMessage(ctx, sdkMsg); err != nil {
			logrus.WithError(err).Warn("hydrate memory: add message")
		}
		added++
	}
	return nil
}

// withCompanyNameContext prepends the tenant organization name so the
// agent can personalize references. If name is empty, msg is unchanged.
// withLanguageReminder restates guideline 1 immediately above the user's own
// sentence.
//
// The system prompt already opens with "LANGUAGE IS THE TOP PRIORITY … never
// default to Indonesian when the user wrote in English", and that rule held
// until T-07 gave every turn a metric catalog. The eval gate of 2026-08-02
// measured what changed: eleven English questions answered in Indonesian with
// metrics defined, six of eight flipping back with the registry emptied. The
// composed message contains **no Indonesian at all** — 1,500 characters of
// English scaffolding and the question — so this was never language detection
// going wrong. It is the rule losing its grip as the distance between it and
// the question grows, which is why moving the catalog into the system prompt
// (tried first, on T-A2b's precedent) changed three of six and settled nothing.
//
// So the fix is position, not content: the reminder is prepended FIRST, which
// leaves it LAST — the final line before the user's own words, on every turn,
// for ~70 characters. It names both directions, because the failure this
// project has shipped runs one way and the fix must not cause the other.
func withLanguageReminder(msg string) string {
	return "[System context: Reply in the same language the user writes below — " +
		"English question, English answer; Indonesian question, Indonesian answer. " +
		"The context blocks above are always English and say nothing about which " +
		"language to reply in.]\n\n" + msg
}

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
func (r *ChatRunner) withRelevantTablesContext(ctx context.Context, msg string, questionVec []float32, sources []*domain.DBConnection) string {
	// Plain End rather than tracing.End: every failure in here is a deliberate
	// silent skip, so there is no error to record — what the waterfall answers
	// is how long the embedding round-trip cost before the turn even started.
	ctx, span := tracing.Step(ctx, "table_picker")
	defer span.End()

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

	// The vector is computed once per turn and passed in (T-Q8): the cookbook
	// asks the same question of the same text, and embedding it twice would be
	// two network round trips before the model is even called.
	qv := questionVec
	if len(qv) == 0 {
		logrus.WithField("company_id", companyID).Debug("table picker: no question vector; skipping hint")
		return msg
	}

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

// questionVector embeds the user's message once for every consumer that wants
// it: the table picker and the cookbook (T-Q8).
//
// Returns nil for every ordinary reason a tenant has no embeddings — no cache
// wired, no credentials, an API that failed — and each of those disables the
// features that need it rather than the turn. Skipped entirely when nothing
// would use the vector, so a deployment with neither feature pays nothing.
func (r *ChatRunner) questionVector(ctx context.Context, companyID, userMsg string) []float32 {
	if r.embedCache == nil || strings.TrimSpace(userMsg) == "" {
		return nil
	}
	if r.embRepo == nil && r.cookbook == nil {
		return nil
	}

	ctx, span := tracing.Step(ctx, "embed_question")
	defer span.End()

	client, err := r.embedCache.For(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("embed question: resolving the client failed; the table hint and cookbook are skipped")
		return nil
	}
	if client == nil {
		logrus.WithField("company_id", companyID).
			Debug("embed question: no embedding credentials for this tenant")
		return nil
	}

	start := time.Now()
	vecs, err := client.Embed(ctx, []string{userMsg})
	if err != nil || len(vecs) == 0 {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "model": client.Model(),
		}).Warn("embed question: failed; the table hint and cookbook are skipped")
		return nil
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  companyID,
		"model":       client.Model(),
		"duration_ms": time.Since(start).Milliseconds(),
	}).Debug("embed question: done")
	return vecs[0]
}

// withCookbookContext shows the turn how this tenant's own questions have been
// answered before (T-Q8).
//
// **What it is for.** Every turn rediscovers that "revenue" means
// SUM(sales_amount) in this warehouse, that the fiscal year starts in April,
// that "active customers" excludes the test accounts. The table picker narrows
// which tables to read and stops there. This carries the rest — and it costs no
// new data collection, because `agent_actions` has been recording the SQL of
// every query since T-05.
//
// **The two constraints in the block's wording** are both failure modes rather
// than politeness. The examples are old, so their numbers are stale — a model
// that quotes one has stated a figure no tool produced this turn, which is the
// worst failure this product has. And the examples are a precedent, not an
// answer: a question that resembles a previous one is not the previous one, and
// an agent that pattern-matches too hard will answer last week's question with
// this week's words.
func (r *ChatRunner) withCookbookContext(ctx context.Context, msg string, questionVec []float32) string {
	if r.cookbook == nil || len(questionVec) == 0 {
		return msg
	}

	ctx, span := tracing.Step(ctx, "cookbook")
	defer span.End()

	companyID := tenantctx.CompanyID(ctx)
	// Scoped to the sources this turn may read. A permission, not a filter: an
	// agent scoped away from a warehouse must not be shown queries against it,
	// or its prompt carries the table names the scope exists to hide (T-S2).
	scope := agentscope.FromContext(ctx)
	hits, err := r.cookbook.TopK(ctx, companyID, scope.SourceIDs, questionVec, r.cookbookTopK)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("cookbook: lookup failed; this turn runs without examples")
		return msg
	}
	if len(hits) == 0 {
		return msg
	}

	var b strings.Builder
	b.WriteString("[System context: How questions like this one have been answered before, ")
	b.WriteString("for THIS organization's own data. Use them for the table and column names, ")
	b.WriteString("the joins, and the conventions they show — this is how your predecessors ")
	b.WriteString("read this warehouse.\n")
	b.WriteString("Two rules. These are PRECEDENTS, not answers: adapt the SQL to what is actually ")
	b.WriteString("being asked now, and if none of them fits, write your own query and ignore them. ")
	b.WriteString("And they are OLD: the row counts below are from when they ran, so never quote a ")
	b.WriteString("number from here — run the query.\n")

	ids := make([]int64, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
		fmt.Fprintf(&b, " - Asked: %s\n   Source: %s\n   SQL: %s\n",
			oneLineDigest(h.Question), h.SourceID, oneLineDigest(h.SQL))
	}
	b.WriteString("]\n\n")

	// Bookkeeping, detached and best-effort: which examples keep matching real
	// questions is how anyone prunes this later, and it must not be able to
	// slow down or fail a turn.
	go func(ids []int64) {
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := r.cookbook.MarkUsed(markCtx, ids, time.Now()); err != nil {
			logrus.WithError(err).Debug("cookbook: marking examples used failed")
		}
	}(ids)

	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "examples": len(hits),
	}).Debug("cookbook: examples injected")
	return b.String() + msg
}

// withSourcesContext prepends the catalog of available data sources so the
// agent can pick a source_id per tool call without spending a list_sources /
// get_schema round-trip. Per-source dialect hints are returned in each
// run_sql / get_schema result (db_type field) so we
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
	b.WriteString("Pick the appropriate source_id when calling get_schema, run_sql, or create_dashboard. ")
	if len(sources) > 1 {
		b.WriteString("If unsure which source the user means, ASK before querying.")
	} else {
		b.WriteString("Only one source exists, so source_id is optional.")
	}
	b.WriteString("]\n\n")
	b.WriteString(msg)
	return b.String()
}

// withMetricsContext prepends the company's enabled metric catalog to the turn
// (T-07), the same way withSourcesContext prepends the source catalog. It is
// what makes "prefer query_metric" actionable: the agent cannot choose a defined
// number over a re-derived one if it does not know which numbers are defined.
//
// A read failure is swallowed to a no-op — a turn that cannot list metrics still
// answers via run_sql, which is strictly the pre-registry behaviour.
func (r *ChatRunner) withMetricsContext(ctx context.Context, msg, companyID string) string {
	if r.metrics == nil {
		return msg
	}
	defs, err := r.metrics.ListEnabled(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("metric catalog prefetch failed; the turn falls back to run_sql")
		return msg
	}
	if len(defs) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString("[System context: Defined metrics for this organization — authoritative, pre-validated numbers. ")
	b.WriteString("If one of these answers the question, call query_metric with its key rather than composing run_sql; ")
	b.WriteString("only fall back to run_sql for questions no metric covers, and say so.\n")
	for _, m := range defs {
		desc := m.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, " - %s | %s (%s, per %s) — %s\n", m.Key, m.Label, m.Unit, m.Grain, desc)
	}
	b.WriteString("]\n\n")
	b.WriteString(msg)
	return b.String()
}

// withActionsContext prepends the kinds this company has enabled, the same way
// withMetricsContext prepends the metric catalog and for the same reason: a
// capability the agent is not told about is one it cannot choose.
//
// `propose_action`'s description can only ever name one example — it is a static
// string on a tool shared by every tenant — so before this, an agent asked to
// file a ticket had to guess that `http_action` existed and that an endpoint was
// called `ops_ticket`. The 2026-08-02 gate measured what guessing is worth: four
// turns tried to reach it and one landed, the one whose user message dictated
// the arguments.
//
// A read failure is swallowed to a no-op. The turn then behaves exactly as it
// did before this block existed — it can still propose, it just has to guess.
func (r *ChatRunner) withActionsContext(ctx context.Context, msg, companyID string) string {
	if r.actionCat == nil {
		return msg
	}
	entries, err := r.actionCat.CatalogForTurn(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("action catalog prefetch failed; the turn proposes without knowing what is enabled")
		return msg
	}
	if len(entries) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString("[System context: Actions this workspace has enabled. ")
	b.WriteString("To do one, call propose_action with the action_kind and the params shown; ")
	b.WriteString("it records a proposal for a human to approve and performs nothing itself. ")
	b.WriteString("Do not propose a kind that is not listed here — say plainly that it is not available.\n")
	for _, e := range entries {
		fmt.Fprintf(&b, " - %s — %s", e.Kind, e.Usage)
		if len(e.Options) > 0 {
			fmt.Fprintf(&b, " Registered names: %s.", strings.Join(e.Options, ", "))
		}
		if !e.RequiresApproval {
			// Worth saying: this kind executes on proposal, so the model should
			// not tell the user to go and approve something that already ran.
			b.WriteString(" This kind runs immediately on proposal, with no approval step.")
		}
		b.WriteString("\n")
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
