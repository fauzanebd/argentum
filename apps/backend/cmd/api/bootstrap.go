package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	mcpclient "github.com/fauzanebd/argentum/internal/adapters/mcp"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/agenttemplates"
	"github.com/fauzanebd/argentum/internal/apiobs"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/llmclient"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/migrate"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
	"github.com/fauzanebd/argentum/internal/whatsapp"
	"github.com/sirupsen/logrus"
)

// bootstrap wires control-plane DB, tenant pool, Redis, queue, services, and WhatsApp.
// On failure, partial resources are torn down before returning.
func bootstrap(ctx context.Context, cfg *config.Config) (_ *apiDeps, err error) {
	deps := &apiDeps{cfg: cfg}
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	defer func() {
		if err != nil {
			rollback()
		}
	}()

	if err := migrate.Up(cfg.DatabaseURL(), cfg.ControlMigrationsDir); err != nil {
		return nil, fmt.Errorf("control migrations: %w", err)
	}

	controlDB, err := pgctl.New(cfg.DatabaseURL())
	if err != nil {
		return nil, fmt.Errorf("control DB: %w", err)
	}
	deps.controlDB = controlDB
	undo = append(undo, func() { controlDB.Close() })

	companyRepo := pgctl.NewCompanyRepo(controlDB)
	userRepo := pgctl.NewUserRepo(controlDB)
	connRepo := pgctl.NewConnectionRepo(controlDB)
	phoneRepo := pgctl.NewPhoneRepo(controlDB)
	threadRepo := pgctl.NewThreadRepo(controlDB)
	messageRepo := pgctl.NewMessageRepo(controlDB)
	usageRepo := pgctl.NewUsageRepo(controlDB)
	creditsRepo := pgctl.NewCreditsRepo(controlDB)
	llmCredRepo := pgctl.NewCompanyLLMCredentialRepo(controlDB)
	discordCredRepo := pgctl.NewCompanyDiscordCredentialRepo(controlDB)
	allowedDiscordRepo := pgctl.NewAllowedDiscordUserRepo(controlDB)
	larkCredRepo := pgctl.NewCompanyLarkCredentialRepo(controlDB)
	allowedLarkRepo := pgctl.NewAllowedLarkUserRepo(controlDB)
	deps.threadRepo = threadRepo
	deps.msgRepo = messageRepo
	deps.usageRepo = usageRepo
	deps.userRepo = userRepo
	deps.companyRepo = companyRepo
	// Read-only here. The rows are written by the worker, which is where the
	// agent runs; the API only serves them back to an admin.
	deps.actionRepo = pgctl.NewAgentActionRepo(controlDB)
	deps.teamSvc = app.NewTeamService(userRepo, pgctl.NewUserInviteRepo(controlDB))
	// The only machine credential in the product (T-13). It authenticates
	// `/v1`; the dashboard routes beside it are how an admin mints one.
	deps.apiKeySvc = app.NewAPIKeyService(pgctl.NewAPIKeyRepo(controlDB))

	dsnCipher, err := crypto.NewFromHex(cfg.DSNEncryptionKeyHex)
	if err != nil {
		return nil, fmt.Errorf("DSN cipher: %w", err)
	}

	signer, err := auth.NewTokenSigner(cfg.JWTSecret, 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("JWT signer: %w", err)
	}
	deps.signer = signer

	resolver := pgctl.NewConnectionResolver(connRepo, dsnCipher)
	deps.tenant = db.NewTenantConnPool(resolver, 200, 30*time.Minute)
	tenCtx, cancelTen := context.WithCancel(ctx)
	deps.cancelTen = cancelTen
	deps.tenant.Start(tenCtx)
	undo = append(undo, func() {
		deps.tenant.CloseAll()
		cancelTen()
	})

	rdb := buildRedisClient(cfg)
	if rdb == nil {
		return nil, fmt.Errorf("redis client is required (REDIS_URL)")
	}
	deps.rdb = rdb
	undo = append(undo, func() { _ = rdb.Close() })

	asynqOpt, err := queue.BuildRedisOpt(cfg.ResolvedAsynqRedisURL(), cfg.RedisPassword)
	if err != nil {
		return nil, fmt.Errorf("asynq redis opt: %w", err)
	}
	deps.enqueuer = queue.NewEnqueuer(asynqOpt)
	undo = append(undo, func() { _ = deps.enqueuer.Close() })

	// Per-tenant LLM cache (shared with the worker's chat runner — separate
	// process, independent cache). Used by the API's embedding service for
	// reindex, and stashed on deps for future tenant-scoped flows.
	llmResolver := llmtenant.NewResolver(llmCredRepo, dsnCipher, cfg)
	deps.usageSvc = app.NewUsageService(usageRepo, creditsRepo, app.DefaultPricing).
		WithCredits(app.CreditPolicy{
			Enforce:       cfg.CreditsEnforcementEnabled,
			WarnPct:       cfg.CreditsWarningThresholdPct,
			GrantMicroUSD: cfg.CreditsDefaultGrantMicroUSD(),
		}, llmCredRepo, app.NewRedisBudgetCache(rdb))
	deps.llmCache = llmtenant.NewClientCache(
		llmResolver,
		func(inner interfaces.LLM, model string) interfaces.LLM {
			return app.NewMeteredLLM(inner, model, deps.usageSvc)
		},
		300, 30*time.Minute,
	)
	deps.llmCache.Start(tenCtx)
	deps.embedCache = llmtenant.NewEmbeddingCache(llmResolver, 100, 30*time.Minute)
	deps.embedCache.Start(tenCtx)
	undo = append(undo, func() {
		deps.embedCache.CloseAll()
		deps.llmCache.CloseAll()
	})

	deps.authSvc = app.NewAuthService(companyRepo, userRepo, signer)

	var mbCli *metabase.Client
	var metabaseWarehouse *app.MetabaseWarehouseSync
	if cfg.MetabaseURL != "" && cfg.MetabaseAdminEmail != "" && cfg.MetabaseAdminPassword != "" {
		mbCli = metabase.NewClient(cfg.MetabaseURL, cfg.MetabasePublicURL,
			cfg.MetabaseAdminEmail, cfg.MetabaseAdminPassword)
		metabaseWarehouse = app.NewMetabaseWarehouseSync(mbCli)
	}
	rawLightLLM, err := llmclient.BuildLight(cfg)
	if err != nil {
		return nil, fmt.Errorf("light LLM: %w", err)
	}
	lightLLMClient := app.NewMeteredLLM(rawLightLLM, cfg.EffectiveLightLLMModel(), deps.usageSvc)
	// Schema-cache invalidation for the API: chat tools live in the worker
	// process, so this GetSchemaTool is dedicated to the api's invalidation
	// hooks (rotate DSN -> drop cache). Each process has its own cache.
	apiSchemaTool := tools.NewGetSchemaTool(deps.tenant, connRepo)
	describer := app.NewConnectionDescriber(lightLLMClient, deps.tenant, connRepo)
	deps.companySvc = app.NewCompanyService(companyRepo, connRepo, phoneRepo, dsnCipher, deps.tenant, metabaseWarehouse, apiSchemaTool, describer).
		// Business inference runs in the worker (T-B2); this process only ever
		// asks for it — when a source is added, when its DSN rotates, when a
		// test passes, and when a tenant presses Re-scan.
		WithInference(deps.enqueuer)
	if cfg.DiscordEnabled {
		reloadBus := eventbus.NewRedisBus(rdb)
		deps.discordSvc = app.NewDiscordService(discordCredRepo, allowedDiscordRepo, dsnCipher, reloadBus)
	}
	if cfg.LarkEnabled {
		deps.larkSvc = app.NewLarkService(larkCredRepo, allowedLarkRepo, dsnCipher)
		deps.larkReplier = lark.NewClient(larkCredRepo, dsnCipher, cfg.LarkAPIBaseURL)
	}

	// Table-picker embeddings: per-tenant resolution via embedCache.
	// EmbeddingService resolves the client per ReindexSource call so each
	// company hits its own provider/key.
	if cfg.EmbeddingEnabled {
		tableEmbRepo := pgctl.NewTableEmbeddingRepo(controlDB)
		deps.embeddingSvc = app.NewEmbeddingService(connRepo, connRepo, tableEmbRepo, apiSchemaTool, deps.embedCache)
	}
	dashboardRepo := pgctl.NewDashboardRepo(controlDB)
	deps.dashboardSvc = app.NewDashboardService(dashboardRepo, mbCli)
	classifierLLM := lightLLMClient
	if cfg.ClassifierModel != "" {
		rawClassifier, err := llmclient.BuildClassifier(cfg)
		if err != nil {
			logrus.WithError(err).Warn("classifier LLM build failed; falling back to light LLM")
		} else {
			classifierLLM = app.NewMeteredLLM(rawClassifier, cfg.EffectiveClassifierModel(), deps.usageSvc)
		}
	}
	classifier := app.NewTopicClassifier(classifierLLM)
	threadSvc := app.NewThreadService(threadRepo, messageRepo, classifier, lightLLMClient,
		app.ThreadServiceConfig{
			IdleMinutes:        cfg.ThreadIdleMinutes,
			SummaryEveryNTurns: cfg.SummaryEveryNTurns,
		})
	// The roster is read on two paths in this process: the enqueuer pins every
	// turn to an agent (T-S2), and the Agents tab edits the rows (T-S1). One
	// repository, constructed here because the enqueuer is built several
	// hundred lines before the service that owns the CRUD surface.
	agentRepo := pgctl.NewAgentRepo(controlDB)
	// The bindings are read on the enqueue path of every WhatsApp, Discord and
	// Lark turn (T-S4) and written by the Settings tab. The enqueuer takes the
	// repository rather than the service for the same reason it takes agentRepo:
	// its contract is two reads, and a write method it can reach is a write
	// method somebody eventually calls from there.
	bindingRepo := pgctl.NewAgentBindingRepo(controlDB)
	deps.chatEnq = app.NewChatEnqueuer(threadSvc, messageRepo, companyRepo, deps.enqueuer).
		WithBudget(deps.usageSvc).
		WithRoster(agentRepo).
		WithChannelBindings(bindingRepo)
	scheduledRepo := pgctl.NewScheduledTaskRepo(controlDB)
	// The API process only creates and edits schedules — the worker fires
	// them — but the service is the same type, and wiring it here keeps the
	// two constructions from drifting into different enforcement.
	deps.scheduledSvc = app.NewScheduledTaskService(scheduledRepo, threadSvc, companyRepo, deps.enqueuer).
		WithBudget(deps.usageSvc)

	waProvider, err := whatsapp.NewProvider(whatsapp.Config{
		Provider:           cfg.WhatsAppProvider,
		APIVersion:         cfg.WhatsAppAPIVersion,
		PhoneNumberID:      cfg.WhatsAppPhoneNumberID,
		AccessToken:        cfg.WhatsAppAccessToken,
		AppSecret:          cfg.WhatsAppAppSecret,
		WebhookVerifyToken: cfg.WhatsAppWebhookVerifyToken,
		TwilioAccountSID:   cfg.TwilioAccountSID,
		TwilioAuthToken:    cfg.TwilioAuthToken,
		TwilioFromNumber:   cfg.TwilioFromNumber,
	})
	if err != nil {
		return nil, fmt.Errorf("WhatsApp provider: %w", err)
	}
	deps.wa = waProvider

	// Report branding (T-R5). The logo lives in the same bucket the generated
	// documents do, so the API needs the storage client the worker already
	// builds — without MINIO_ENDPOINT the service still answers, minus the
	// logo: a tenant can set a colour and a footer line on a deployment with
	// no object storage at all.
	var logoStore branding.ObjectStore
	if cfg.MinIOEndpoint != "" {
		st, err := storage.NewStorageService(&storage.MinIOConfig{
			Endpoint:        cfg.MinIOEndpoint,
			AccessKeyID:     cfg.MinIOAccessKeyID,
			SecretAccessKey: cfg.MinIOSecretAccessKey,
			Bucket:          cfg.MinIOBucket,
			UseSSL:          cfg.MinIOUseSSL,
		})
		if err != nil {
			logrus.WithError(err).Warn("object storage unavailable; report logo upload is disabled")
		} else {
			logoStore = st
			deps.storageSvc = st
		}
	}
	deps.brandingSvc = branding.NewService(companyRepo, logoStore, companyRepo)

	// The `/v1` report surface (T-A2). Same constructor the worker's stack
	// uses for the agent's generate_document, with the untrusted-spec caps
	// installed on top — that difference, and nothing else, is what separates
	// a spec written by our own model from one posted by a stranger.
	deps.reportRepo = pgctl.NewAPIReportRepo(controlDB)
	deps.documentRepo = pgctl.NewDocumentRepo(controlDB)
	deps.idemStore = idempotencyStoreOrNil(rdb)
	if deps.storageSvc != nil {
		deps.docGen = docgen.New(
			deps.storageSvc, deps.documentRepo, companyRepo, deps.brandingSvc, deps.usageSvc,
			time.Duration(cfg.DocumentPresignTTLSecs)*time.Second,
		).WithLimits(spec.Limits{
			MaxRows: cfg.APIV1MaxSpecRows,
			MaxCols: cfg.APIV1MaxSpecCols,
		})
	}

	// The agent roster (T-S1). The tool checkboxes an admin scopes an agent
	// with are the names of the registry the worker actually runs, built here
	// from the same tools.Registry — this process constructs the tools and
	// calls none of them, which is a few pointer copies and the only way the
	// two lists cannot drift. Docs is deps.docGen, so a deployment without
	// object storage offers no generate_document checkbox for the same reason
	// the worker registers no such tool.
	//
	// The gallery an agent can be created from (T-B3) is validated against
	// tools.AllNames rather than the list below: a template naming a tool that
	// does not exist is a typo in a file we ship, and it has to fail everywhere
	// rather than only on the deployments that run the tool it misspelled. A
	// malformed file stops the boot, which is why this error is returned and
	// not warned about — see config.AgentTemplatesPath.
	agentTemplates, err := agenttemplates.LoadFromFile(cfg.AgentTemplatesPath, tools.AllNames())
	if err != nil {
		return nil, fmt.Errorf("agent templates: %w", err)
	}
	deps.agentSvc = app.NewAgentService(
		agentRepo, connRepo,
		tools.Names(tools.Registry(tools.RegistryDeps{
			Pool:                deps.tenant,
			Connections:         connRepo,
			Redis:               rdb,
			Usage:               deps.usageSvc,
			Metabase:            mbCli,
			MetabaseSource:      connRepo,
			Dashboards:          deps.dashboardSvc,
			Scheduled:           deps.scheduledSvc,
			Docs:                deps.docGen,
			MaxQueryRows:        cfg.MaxQueryRows,
			MaxQueryResultBytes: cfg.MaxQueryResultBytes,
		})),
	).WithTemplates(agentTemplates)
	deps.agentBindingSvc = app.NewAgentBindingService(bindingRepo, agentRepo)
	companyProfileRepo := pgctl.NewCompanyProfileRepo(controlDB)
	sourceProfileRepo := pgctl.NewSourceProfileRepo(controlDB)
	deps.companyProfileSvc = app.NewCompanyProfileService(companyProfileRepo).
		// The review panel (T-B2). The drafts are written by the worker; this
		// side reads them, folds them, and applies one when a human says so.
		WithSuggestions(sourceProfileRepo, deps.usageSvc)
	// "Generate with AI" (T-B4) on the light model, like inference: one short
	// structured call, billed because the client is already the metered one.
	// It reads both profile tables and the gallery — the ladder it degrades
	// down when a tenant has none of them is in AgentGenerateService.
	deps.agentGenSvc = app.NewAgentGenerateService(
		lightLLMClient, companyProfileRepo, cfg.EffectiveLightLLMModel(),
	).WithSourceProfiles(sourceProfileRepo).
		WithTemplates(agentTemplates).
		WithBudget(deps.usageSvc)
	// The tenant's MCP servers (T-M1). The egress guard is built here, once,
	// from config — a Guard constructed at a call site is a Guard somebody
	// constructs with the wrong flag, and the flag in question is the one that
	// decides whether a tenant can point us at our own metadata endpoint.
	//
	// AllowPrivate is refused outside development rather than trusted: an
	// operator who sets MCP_ALLOW_PRIVATE_EGRESS in production has almost
	// certainly copied it from a developer's .env, and the failure mode of
	// believing them is an SSRF with our network position behind it.
	allowPrivateEgress := cfg.MCPAllowPrivateEgress
	if allowPrivateEgress && !cfg.IsDevelopment() {
		logrus.Warn("MCP_ALLOW_PRIVATE_EGRESS is set outside development and is being ignored")
		allowPrivateEgress = false
	}
	// Plaintext http is a separate opt-in and is honoured everywhere: it keeps
	// every address rule and trades only TLS, which is a call an operator whose
	// tenants run MCP servers without certificates gets to make. It is logged
	// because a token crossing the network in the clear should be a thing
	// somebody can find in a boot log rather than a thing they infer later.
	if cfg.MCPAllowInsecureHTTP {
		logrus.Warn("MCP_ALLOW_INSECURE_HTTP is on: tenant MCP servers may be reached over plaintext http, " +
			"which sends their bearer token and their tool results unencrypted")
	}
	mcpGuard := mcpclient.Guard{
		AllowPrivate:      allowPrivateEgress,
		AllowInsecureHTTP: cfg.MCPAllowInsecureHTTP,
		Timeout:           time.Duration(cfg.MCPProbeTimeoutSecs) * time.Second,
	}
	deps.mcpServerSvc = app.NewMCPServerService(
		pgctl.NewMCPServerRepo(controlDB), dsnCipher,
		mcpclient.NewClient(mcpGuard),
	)

	// Signup seeds the new company's first agent. Wired after the roster
	// exists rather than at NewAuthService, which runs several hundred lines
	// earlier and before there is a connection repository to validate against.
	deps.authSvc.WithRoster(deps.agentSvc)

	// Shared with app.MeteredLLM, which is too deep in the call graph to be
	// handed a collector, so /metrics reports streaming-metering health too.
	deps.metrics = metrics.Default()

	// The `/v1` request recorder (T-A5). It is built last because it needs the
	// collector, and it owns a goroutine: the flush loop runs for the life of
	// the process and is stopped by deps.cleanup, which flushes what is left
	// rather than dropping it.
	deps.requestRepo = pgctl.NewAPIRequestRepo(controlDB)
	deps.requestObs = apiobs.New(deps.requestRepo, deps.metrics,
		apiobs.WithRetention(time.Duration(cfg.APIV1ObsRetentionDays)*24*time.Hour))
	obsCtx, stopObs := context.WithCancel(ctx)
	deps.stopObs = stopObs
	go deps.requestObs.Run(obsCtx, time.Duration(cfg.APIV1ObsFlushSeconds)*time.Second)

	return deps, nil
}

// idempotencyStoreOrNil hands back a typed nil-free store, or a genuinely nil
// interface. Assigning a nil *idempotency.RedisStore straight into the
// interface would produce a non-nil interface holding a nil pointer, and the
// middleware's own `store == nil` guard — the one that degrades to no replay
// protection instead of refusing every write — would never fire. The same trap
// budgetReaderOrNil exists for in router.go.
func idempotencyStoreOrNil(rdb *redis.Client) idempotency.Store {
	st := idempotency.NewRedisStore(rdb)
	if st == nil {
		return nil
	}
	return st
}
