package main

import (
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/apiobs"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/transport/http/handlers"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
	"github.com/fauzanebd/argentum/internal/transport/ws"
)

func newRouter(d *apiDeps) *gin.Engine {
	cfg := d.cfg
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogging())
	// /v1 is excluded: it is a machine surface authenticated by an API key,
	// and with CORS_ORIGINS unset this middleware echoes any Origin.
	r.Use(middleware.CORS(cfg.CORSOrigins, "/v1"))

	registerHealthRoutes(r, d.metrics, d.controlDB, cfg.MetricsToken)

	api := r.Group("/api")
	// The span every `/api` request runs under, and the reason a turn's two
	// processes can share a trace at all: `Inject` on the enqueue reads
	// whatever span is on the request context, and until 2026-08-09 there was
	// never one. Below the health routes on purpose — a readiness probe every
	// few seconds is the highest-volume, least interesting span a collector
	// could be sent.
	api.Use(middleware.Tracing())
	handlers.NewMetaHandler().Register(api.Group("/meta"))
	handlers.NewAuthHandler(d.authSvc, d.teamSvc, cfg.CookieSecure, d.signer.RefreshTTL()).
		Register(api.Group("/auth"))

	authed := api.Group("")
	authed.Use(middleware.Auth(d.signer))
	// RequireRole runs after Auth (it reads the role Auth sets) and before the
	// rate limiter, so a request a member is not allowed to make does not
	// consume their budget. apiPolicy in policy.go is the whole access model.
	authed.Use(middleware.RequireRole(apiPolicy))
	if rateLimiter := middleware.NewRateLimiter(d.rdb, 60, 1.0); rateLimiter != nil {
		authed.Use(rateLimiter.Middleware())
	}
	handlers.NewCompanyHandler(d.companySvc, d.embeddingSvc).Register(authed)
	handlers.NewChatHandler(d.chatEnq, d.threadRepo, d.msgRepo, d.dashboardSvc).Register(authed)
	handlers.NewUsageHandler(d.usageSvc).Register(authed)
	handlers.NewConfigHandler(cfg).Register(authed)
	handlers.NewUserHandler(d.userRepo, d.companyRepo, d.teamSvc).Register(authed.Group("/users"))
	handlers.NewReportsHandler(d.brandingSvc, d.companyRepo).Register(authed)
	handlers.NewAuditHandler(d.actionRepo).Register(authed)
	handlers.NewAPIKeysHandler(d.apiKeySvc).
		WithTraffic(trafficReaderOrNil(d.requestRepo)).
		Register(authed)
	handlers.NewAgentsHandler(d.agentSvc).
		WithGenerator(d.agentGenSvc).
		Register(authed)
	handlers.NewAgentBindingsHandler(d.agentBindingSvc).Register(authed)
	handlers.NewCompanyProfileHandler(d.companyProfileSvc).Register(authed)
	handlers.NewMCPServersHandler(d.mcpServerSvc).Register(authed)
	handlers.NewMetricsHandler(d.metricSvc).Register(authed)
	handlers.NewWatchersHandler(d.watcherSvc).Register(authed)
	handlers.NewActionsHandler(d.actionSvc).Register(authed)
	handlers.NewHTTPEndpointsHandler(d.httpEndpointSvc).Register(authed)
	handlers.NewWebhooksHandler(d.webhookSubsSvc).Register(authed)
	if d.dashboardSvc != nil {
		handlers.NewDashboardHandler(d.dashboardSvc).Register(authed)
	}
	if d.scheduledSvc != nil {
		handlers.NewScheduledTasksHandler(d.scheduledSvc).Register(authed)
	}
	if d.discordSvc != nil {
		handlers.NewDiscordHandler(d.discordSvc).Register(authed)
	}
	if d.larkSvc != nil {
		handlers.NewLarkHandler(d.larkSvc).Register(authed)
	}
	if d.slackSvc != nil {
		handlers.NewSlackHandler(d.slackSvc).Register(authed)
	}
	authed.GET("/threads/:id/stream", ws.NewHandler(d.rdb, d.threadRepo, cfg.CORSOrigins).Stream)

	// The public API (T-13; T-A1 builds the rest of the contract on this
	// group). It is a sibling of /api rather than a subtree of it, and it
	// never sees middleware.Auth: a dashboard session and a machine credential
	// are different authorities, and a group that accepted either would make
	// "which routes can a key reach?" unanswerable.
	//
	// The engine-level CORS middleware skips this prefix on purpose. An API
	// key in a browser is a leaked API key; the browser path is T-19's embed
	// key. The live gate found this the hard way — /v1 inherited the
	// dashboard's headers because CORS is installed above every group.
	//
	// The chain order below is the contract, not a preference:
	//   RequestID   — first, so *every* response carries one, including the
	//                 503 the kill switch writes and the 401 an integrator
	//                 with a bad key gets. Those are the two responses most
	//                 likely to start a support conversation, and the live
	//                 gate found them going out without an id when this sat
	//                 second. It reads no credential and touches no I/O, so
	//                 there is nothing to protect by deferring it.
	//   Enabled     — a switched-off API answers before it reads a credential
	//   MaxBodyBytes— refuse an oversized body before anything parses it
	//   APIKeyAuth  — who is calling
	//   rate limit  — per key, so it needs the key
	// Idempotency is per route rather than group-wide: a GET does not need a
	// Redis key and a 24-hour TTL to be idempotent.
	//
	// The published contract is served from a sibling group carrying only the
	// first two links of that chain (T-A4). It is keyless on purpose — an
	// integrator reads the spec before they have a key — and it stays under the
	// kill switch, because a spec for a surface that is refusing every call
	// generates a client nobody can use.
	spec := r.Group("/v1")
	spec.Use(middleware.RequestID())
	spec.Use(middleware.Enabled(cfg.APIV1Enabled))
	handlers.NewV1OpenAPIHandler().Register(spec)

	v1 := r.Group("/v1")
	v1.Use(middleware.RequestID())
	// Below RequestID so the span carries the id the caller was handed, and
	// above everything else so a 503 from the kill switch and a 401 from a bad
	// key are both on the waterfall. This is the surface `POST /v1/chat` and
	// `POST /v1/reports*` enqueue from, so it is the trace the worker joins.
	v1.Use(middleware.Tracing())
	v1.Use(middleware.Enabled(cfg.APIV1Enabled))
	// The recorder (T-A5) wraps everything below it, which is what makes a 401
	// from a bad key and a 429 from the limiter both countable. It goes below
	// the kill switch on purpose: a 503 from a switched-off API is a fact about
	// us, not about anybody's integration.
	v1.Use(middleware.RecordAPIRequests(requestSinkOrNil(d.requestObs)))
	v1.Use(middleware.MaxBodyBytes(cfg.APIV1MaxBodyBytes))
	v1.Use(middleware.APIKeyAuth(apiKeyAuthOf(d)))
	if keyLimiter := middleware.NewRateLimiter(d.rdb, cfg.APIV1RatePerMin, float64(cfg.APIV1RatePerMin)/60.0); keyLimiter != nil {
		v1.Use(keyLimiter.APIKeyMiddleware())
	}
	handlers.NewV1MeHandler(d.companyRepo, budgetReaderOrNil(d.usageSvc), cfg.APIV1RatePerMin).
		WithWebhookSecrets(d.companyRepo).
		Register(v1)
	// `GET /v1/agents` (T-S5): the roster, so `agent_id` is a field an
	// integrator can fill in from the API rather than from a uuid somebody with
	// an admin session read out of the dashboard for them.
	handlers.NewV1AgentsHandler(rosterListerOrNil(d.agentSvc), mcpListerOrNil(d.mcpServerSvc)).Register(v1)
	// `GET /v1/usage` (T-A5): the spend and the balance, over a window the
	// caller chooses, so a tenant's own application can meter its own users
	// instead of polling `/v1/me` for a number with no period attached.
	handlers.NewV1UsageHandler(usageReaderOrNil(d.usageRepo), budgetReaderOrNil(d.usageSvc)).
		Register(v1)
	// T-A2's two doors and the documents they produce. Registered
	// unconditionally: a deployment without object storage answers a typed 503
	// from inside the handler, which tells an integrator why, where an absent
	// route tells them they got the path wrong.
	handlers.NewV1ReportsHandler(
		d.docGen, d.reportRepo, d.documentRepo, chatEnqueuerOrNil(d.chatEnq), d.enqueuer, d.rdb, d.idemStore,
		time.Duration(cfg.APIV1SyncRenderTimeoutSecs)*time.Second,
		cfg.APIV1CallbackAllowPrivate,
	).WithBudget(budgetReaderOrNil(d.usageSvc)).Register(v1)
	handlers.NewV1DocumentsHandler(d.documentRepo, d.docGen, contentStoreOrNil(d.storageSvc)).Register(v1)
	// T-A3's chat surface. Same reasoning as above: registered unconditionally
	// so a deployment missing a dependency answers a typed 503 from inside the
	// handler rather than a 404 that reads as a wrong path.
	handlers.NewV1ChatHandler(
		chatEnqueuerOrNil(d.chatEnq), d.threadRepo, d.msgRepo, turnUsageOrNil(d.usageRepo), d.rdb, d.idemStore,
		time.Duration(cfg.APIV1SyncTimeoutSeconds)*time.Second,
	).WithDashboards(d.dashboardSvc).Register(v1)

	webhookGroup := r.Group("/webhook")
	handlers.NewWebhookHandler(d.chatEnq, d.companySvc, d.wa, cfg.WhatsAppWebhookVerifyToken).
		Register(webhookGroup)
	if d.discordSvc != nil {
		handlers.NewDiscordWebhookHandler(d.discordSvc).Register(webhookGroup)
	}
	if d.larkSvc != nil {
		handlers.NewLarkWebhookHandler(d.larkSvc, d.chatEnq).
			WithReplier(d.larkReplier).
			Register(webhookGroup)
	}
	if d.slackSvc != nil {
		handlers.NewSlackWebhookHandler(d.slackSvc, d.chatEnq).
			WithReplier(d.slackReplier).
			WithDeduper(d.slackDedupe).
			Register(webhookGroup)
	}

	if cfg.MetabaseURL != "" {
		mbURL, _ := url.Parse(cfg.MetabaseURL)
		mbProxy := httputil.NewSingleHostReverseProxy(mbURL)
		r.Any("/metabase/*path", func(c *gin.Context) {
			c.Request.URL.Path = strings.TrimPrefix(c.Param("path"), "/metabase")
			if c.Request.URL.Path == "" {
				c.Request.URL.Path = "/"
			}
			c.Request.Host = mbURL.Host
			mbProxy.ServeHTTP(c.Writer, c.Request)
		})
	}

	return r
}

// budgetReaderOrNil hands the credit reader to `/v1/me` only when there is
// one. A nil *app.UsageService assigned straight into the handler's interface
// parameter would arrive as a non-nil interface holding a nil pointer — the
// handler's own `budget == nil` guard would not fire, and the first call would
// panic on a route whose job is to answer when everything else is broken.
func budgetReaderOrNil(svc *app.UsageService) handlers.V1BudgetReader {
	if svc == nil {
		return nil
	}
	return svc
}

// chatEnqueuerOrNil is budgetReaderOrNil for the chat enqueuer (T-A3). Third
// instance of the same trap, and the one that would be hardest to spot: a
// deployment with no queue would answer a panic on `POST /v1/chat` rather than
// the typed 503 the handler's own guard writes. `POST /v1/reports` runs
// through the same door since T-A2b, and needs the same conversion.
func chatEnqueuerOrNil(e *app.ChatEnqueuer) handlers.V1ChatEnqueuer {
	if e == nil {
		return nil
	}
	return e
}

// turnUsageOrNil is budgetReaderOrNil for the per-turn usage read (T-A3). The
// same nil-pointer-in-a-non-nil-interface trap: `/v1/chat` reports what a turn
// cost on a best-effort basis, and a typed nil would turn "omit the usage
// block" into a panic on the response path.
func turnUsageOrNil(repo *pgctl.UsageRepo) handlers.V1TurnUsageReader {
	if repo == nil {
		return nil
	}
	return repo
}

// usageReaderOrNil, requestSinkOrNil and trafficReaderOrNil are
// budgetReaderOrNil for T-A5's three dependencies. Same trap, three more
// places: a nil concrete pointer assigned into an interface parameter arrives
// as a non-nil interface holding nil, and each of these has a `== nil` guard
// downstream that exists precisely so a stripped-down wiring degrades instead
// of panicking.
func usageReaderOrNil(repo *pgctl.UsageRepo) handlers.V1UsageReader {
	if repo == nil {
		return nil
	}
	return repo
}

// rosterListerOrNil is budgetReaderOrNil for the roster (T-S5). Same trap: a
// nil *app.AgentService assigned into the interface parameter arrives as a
// non-nil interface holding nil, and `GET /v1/agents` would panic on a wiring
// without a roster rather than answering the typed 503 its own guard writes.
func rosterListerOrNil(svc *app.AgentService) handlers.V1RosterLister {
	if svc == nil {
		return nil
	}
	return svc
}

// mcpListerOrNil is the same nil-interface guard for the MCP server registry
// `GET /v1/agents` reads to name an agent's bound servers (T-M3).
func mcpListerOrNil(svc *app.MCPServerService) handlers.V1MCPServerLister {
	if svc == nil {
		return nil
	}
	return svc
}

func requestSinkOrNil(rec *apiobs.Recorder) middleware.APIRequestSink {
	if rec == nil {
		return nil
	}
	return rec
}

func trafficReaderOrNil(repo *pgctl.APIRequestRepo) handlers.APIKeyTrafficReader {
	if repo == nil {
		return nil
	}
	return repo
}

// apiKeyAuthOf is the one seam a test can drive the real router through.
//
// "Every `/v1` route names its scope" is a review rule, not a table, because
// scopes are per-key and RequireScope sits beside each route — there is
// nothing for a test to diff the router against, which is precisely the gap
// the sprint's risk register calls out. The only way to *prove* the rule is to
// send a real request with a real credential holding no scopes and watch every
// route refuse it, and that needs an authenticator a test can supply.
// Production never sets this field.
func apiKeyAuthOf(d *apiDeps) middleware.APIKeyAuthenticator {
	if d.apiKeyAuth != nil {
		return d.apiKeyAuth
	}
	return d.apiKeySvc
}

// contentStoreOrNil is budgetReaderOrNil for the object store (T-A2). A nil
// *storage.StorageService assigned into the interface parameter would arrive
// as a non-nil interface holding a nil pointer, and `GET
// /v1/documents/:id/content` would panic on a deployment with no object
// storage instead of answering the typed 503 its own guard writes.
func contentStoreOrNil(st *storage.StorageService) handlers.DocumentContentStore {
	if st == nil {
		return nil
	}
	return st
}
