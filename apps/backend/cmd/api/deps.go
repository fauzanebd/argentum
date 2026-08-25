package main

import (
	"context"
	"database/sql"

	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/apiobs"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/docwarehouse"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/slack"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// apiDeps holds infra and services needed to build the HTTP router and run health checks.
type apiDeps struct {
	cfg *config.Config

	controlDB *sql.DB
	cancelTen context.CancelFunc
	tenant    *db.TenantConnPool
	rdb       *redis.Client
	enqueuer  *queue.Enqueuer

	signer       *auth.TokenSigner
	authSvc      *app.AuthService
	teamSvc      *app.TeamService
	companySvc   *app.CompanyService
	embeddingSvc *app.EmbeddingService
	usageSvc     *app.UsageService
	chatEnq      *app.ChatEnqueuer
	threadRepo   *pgctl.ThreadRepo
	msgRepo      *pgctl.MessageRepo
	userRepo     *pgctl.UserRepo
	companyRepo  *pgctl.CompanyRepo
	actionRepo   *pgctl.AgentActionRepo
	// actionSvc is the human side of the action framework (T-10/T-11): the
	// approval endpoints call Approve/Reject on it, and it executes an approved
	// action exactly once. Built with the same action registry the worker
	// proposes against, so a kind the agent can propose is a kind this process
	// can carry out.
	actionSvc *app.ActionService

	// webhookSubsSvc is Settings → Webhooks and the `action.executed` fan-out
	// (T-15). Nil is legal — the routes then answer 503 — but this process always
	// builds one.
	webhookSubsSvc *app.WebhookSubscriptionService
	// httpEndpointSvc is the admin CRUD behind the http_action targets (T-12b):
	// the registered endpoints an approved http_action calls. Built in bootstrap
	// with the DSN cipher and the egress guard, so a header is sealed at rest and a
	// private host is refused at registration rather than at execute time.
	httpEndpointSvc *app.HTTPEndpointService
	// usageRepo is read directly by `/v1/chat` for one thing UsageService does
	// not expose: what a single turn cost, over a window bounded by time.Time
	// rather than by the dashboard's string dates.
	usageRepo *pgctl.UsageRepo
	// The Metabase-backed dashboards (006), on their way out under T-D15. The
	// native ones (056) are a separate service so the decommission is a deletion
	// rather than an edit.
	savedDashboardSvc *app.SavedDashboardService
	// Native dashboards (T-D6/T-D7): the stored spec, and the resolver that runs
	// it against the tenant warehouse through this process's own pool.
	dashboardSvc *app.DashboardService
	scheduledSvc *app.ScheduledTaskService
	discordSvc   *app.DiscordService
	larkSvc      *app.LarkService
	slackSvc     *app.SlackService
	brandingSvc  *branding.Service
	apiKeySvc    *app.APIKeyService
	// The browser-visible credential (T-19). It mints the short-lived sessions
	// `/api/embed` runs on; the dashboard routes beside it are how an admin
	// creates one and states which sites may use it. Separate service from
	// apiKeySvc on purpose — merging a server-side credential with one that
	// ships in somebody's page source is how scope leaks.
	embedKeySvc *app.EmbedKeyService
	// The tenant agent roster (T-S1). It holds this deployment's tool registry
	// by name, which is why it is built from the same tools.Registry the
	// worker runs rather than from a list maintained beside the handler.
	agentSvc *app.AgentService
	// Channel bindings (T-S4): which agent answers in which Discord channel,
	// Lark chat or WhatsApp number. Separate from agentSvc because the enqueuer
	// reads the same table on every inbound message and must not be handed a
	// service that can write to it.
	agentBindingSvc *app.AgentBindingService
	// The company business profile (T-B1): what this workspace does, in the
	// tenant's own words. The API writes it; the worker reads the same table on
	// every turn through its own repository, which is why nothing here is shared
	// with the runner.
	companyProfileSvc *app.CompanyProfileService
	// "Generate with AI" (T-B4): the create form's one LLM call. Separate from
	// agentSvc because it spends the tenant's credit and reads the two profile
	// tables, and the roster's CRUD should be able to reach neither.
	agentGenSvc *app.AgentGenerateService
	// The tenant's own MCP servers (T-M1). It holds the egress guard this
	// process makes outbound requests through, which is why it is built in
	// bootstrap from config rather than at the handler.
	mcpServerSvc *app.MCPServerService
	// The tenant's written procedures (T-K1). CRUD and the agent binding only:
	// the index that rides the prompt is composed in bootstrap (T-K3) and the
	// tool that opens a body is registered with the rest (T-K4).
	skillSvc *app.SkillService
	// The metric registry (T-06): the tenant's named, validated numbers, and the
	// turn-time query path query_metric runs through.
	metricSvc *app.MetricService
	// Answer feedback (T-Q2): whether the people reading the answers thought
	// they were right. The first quality signal in this product that does not
	// come from the golden set.
	feedbackSvc   *app.FeedbackService
	suggestionSvc *app.SuggestionService
	// The query cookbook (T-Q8): what the agent has learned about answering
	// this tenant's questions against this tenant's warehouse.
	cookbookSvc *app.CookbookService
	// Retention, erasure and export (T-H6). The API owns all three: the purge
	// runs in the worker, but its *record* and the two routes a tenant uses to
	// discharge their own UU PDP obligation are read and written here.
	retentionSvc *app.RetentionService
	// Watchers (T-08): CRUD and the dry-run. The API never fires or delivers —
	// that is the worker's WatcherService — so this instance carries no delivery
	// providers and no budget checker.
	watcherSvc *app.WatcherService
	// The `/v1` report surface (T-A2). docGen and storageSvc are nil on a
	// deployment without object storage — the same condition that leaves
	// generate_document unregistered in the worker — and the routes that need
	// them answer a typed 503 rather than being absent, so an integrator gets
	// a reason instead of a 404.
	docGen       *docgen.Service
	storageSvc   *storage.StorageService
	reportRepo   *pgctl.APIReportRepo
	documentRepo *pgctl.DocumentRepo
	// documentIngestSvc takes the PDFs a tenant uploads (T-P1) — the input side
	// of documentRepo's output. Nil without object storage, and the routes then
	// answer 503 rather than accepting a file this deployment cannot store.
	documentIngestSvc *app.DocumentIngestService
	// documentTableSvc is the review-and-publish half (T-P6/T-P7): what was
	// extracted from a document, what a reviewer decided, and the one call that
	// puts it in the document warehouse. Nil without object storage — there are
	// no artifacts to review — and review-only without DOC_WAREHOUSE_DSN, where
	// Apply refuses with a sentence rather than falling back to any other
	// database.
	documentTableSvc *app.DocumentTableService
	documentPageSvc  *app.DocumentPageService
	// docWarehouse is the second Postgres this deployment may hold. Nil is the
	// supported no-publishing configuration; closing it is the API process's
	// job because the API process opened it.
	docWarehouse *docwarehouse.Warehouse
	// shareSvc mints and resolves report player links (T-V4). Nil when there
	// is no object storage: without it no plan was ever written, so there is
	// nothing a link could play.
	shareSvc *app.ReportShareService
	// dashboardShareSvc mints and opens native-dashboard links (T-D13). Unlike
	// shareSvc it needs no object storage — what it serves is a live query, not
	// a stored artefact — so it is built unconditionally.
	dashboardShareSvc *app.DashboardShareService
	idemStore         idempotency.Store
	// apiKeyAuth overrides what authenticates `/v1`. Nil in production, where
	// apiKeySvc is used; see apiKeyAuthOf in router.go for why the seam exists.
	apiKeyAuth middleware.APIKeyAuthenticator
	// larkReplier lets the webhook answer a turn it refuses before enqueueing
	// (T-03). Nil when Lark is disabled.
	larkReplier lark.Provider
	// slackReplier is Lark's counterpart for Slack, and slackDedupe is what
	// keeps a redelivered event from running a second turn. Both nil when
	// Slack is disabled.
	slackReplier slack.Provider
	slackDedupe  slack.Deduper

	wa whatsapp.Provider
	// waTransport is which of the two WhatsApp providers this deployment runs.
	// The webhook handler authenticates by it rather than by sniffing the
	// request, which is what T-H1 fixed.
	waTransport whatsapp.Transport

	llmCache   *llmtenant.ClientCache
	embedCache *llmtenant.EmbeddingCache

	metrics *metrics.Collector
	// Integrator-facing observability over `/v1` (T-A5). requestObs buffers
	// samples off the request path and flushes batches; requestRepo is the same
	// store read back by the dashboard's API Keys tab. stopObs ends the flush
	// loop — cleanup calls it, then flushes what the loop had not.
	requestObs  *apiobs.Recorder
	requestRepo *pgctl.APIRequestRepo
	stopObs     context.CancelFunc
	// stopQueueDepth ends the asynq depth poller (T-17). The API is where it
	// runs because the API is what serves /metrics.
	stopQueueDepth context.CancelFunc
}

// cleanup releases resources in reverse order of creation (same as the original defer stack).
func (d *apiDeps) cleanup() {
	// First, because it writes to the control DB this function later closes —
	// and because the records it is holding cover the minutes immediately
	// before a shutdown, which is when somebody is most likely to be looking.
	if d.stopObs != nil {
		d.stopObs()
	}
	if d.stopQueueDepth != nil {
		d.stopQueueDepth()
	}
	if d.requestObs != nil {
		d.requestObs.Close()
	}
	if d.docWarehouse != nil {
		// The second Postgres this process may hold (T-P6). Closed here rather
		// than left to the runtime because it is a pool with live sessions on
		// somebody else's database, and a deployment that restarts the API in a
		// loop would otherwise leak one set of them per restart.
		_ = d.docWarehouse.Close()
	}
	if d.embedCache != nil {
		d.embedCache.CloseAll()
	}
	if d.llmCache != nil {
		d.llmCache.CloseAll()
	}
	if d.enqueuer != nil {
		_ = d.enqueuer.Close()
	}
	if d.rdb != nil {
		_ = d.rdb.Close()
	}
	if d.tenant != nil {
		d.tenant.CloseAll()
	}
	if d.cancelTen != nil {
		d.cancelTen()
	}
	if d.controlDB != nil {
		d.controlDB.Close()
	}
}
