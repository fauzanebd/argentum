package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/actions"
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
	"github.com/fauzanebd/argentum/internal/dashboard"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/docwarehouse"
	"github.com/fauzanebd/argentum/internal/embedding"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/llmclient"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/migrate"
	"github.com/fauzanebd/argentum/internal/postimage"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/skill"
	"github.com/fauzanebd/argentum/internal/slack"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
	"github.com/fauzanebd/argentum/internal/webhookout"
	"github.com/fauzanebd/argentum/internal/whatsapp"
	"github.com/sirupsen/logrus"
)

// httpActionEgressTimeout bounds one http_action outbound call (T-12b). Fixed at
// the ticket's 10s rather than made configurable: it is a call to a tenant's own
// system on a human-approved proposal, and a system that has not answered in ten
// seconds is one the agent should report as unresponsive, not wait on.
const httpActionEgressTimeout = 10 * time.Second

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
	slackCredRepo := pgctl.NewCompanySlackCredentialRepo(controlDB)
	allowedSlackRepo := pgctl.NewAllowedSlackUserRepo(controlDB)
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

	dsnCipher, err := crypto.NewKeyring(cfg.DSNEncryptionKeyHex, cfg.DSNRetiredKeysHex)
	if err != nil {
		return nil, fmt.Errorf("DSN cipher: %w", err)
	}

	signer, err := auth.NewTokenSigner(cfg.JWTSecret, 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("JWT signer: %w", err)
	}
	deps.signer = signer

	// Embed keys (T-19). Built after the cipher and the signer because it needs
	// both: the cipher seals the signing secret at rest, and the signer mints
	// the session that secret's HMAC buys.
	deps.embedKeySvc = app.NewEmbedKeyService(
		pgctl.NewEmbedKeyRepo(controlDB), dsnCipher, signer,
		time.Duration(cfg.EmbedSessionTTLMinutes)*time.Minute,
	)

	// Does the key this process holds open the rows this database has? Asked
	// once, at boot, because the alternative discovery path is an agent turn
	// failing at query time in front of a customer — which is how the two rows
	// this deployment has lost were actually found. Never fatal: a deployment
	// whose key has moved on still serves every tenant whose rows were
	// re-sealed, and refusing to boot would take those down too.
	app.LogDSNKeyCoverage(ctx, connRepo, dsnCipher)

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
	// Said in this process too: the API is where a document is uploaded and
	// where a reindex is asked for, so an operator watching it deserves the
	// same sentence the worker prints.
	embedding.LogEnvCoverage(cfg)
	undo = append(undo, func() {
		deps.embedCache.CloseAll()
		deps.llmCache.CloseAll()
	})

	deps.authSvc = app.NewAuthService(companyRepo, userRepo, signer)

	rawLightLLM, err := llmclient.BuildLight(cfg)
	if err != nil {
		return nil, fmt.Errorf("light LLM: %w", err)
	}
	lightLLMClient := app.NewMeteredLLM(rawLightLLM, cfg.EffectiveLightLLMModel(), deps.usageSvc)
	// Schema-cache invalidation for the API: chat tools live in the worker
	// process, so this GetSchemaTool is dedicated to the api's invalidation
	// hooks (rotate DSN -> drop cache, publish a document table -> drop cache).
	//
	// **Redis-backed, and that is the point.** The worker writes every schema it
	// extracts into Redis with a one-hour TTL and reads it back there, so an
	// in-memory-only tool here invalidated a map this process had never filled
	// and left the entry the worker actually reads untouched. The 2026-08-19
	// gate found it through the document path — a published table invisible for
	// an hour — but it applies equally to the rotate-DSN hook this comment
	// already described.
	apiSchemaTool := tools.NewGetSchemaToolWithRedis(deps.tenant, connRepo, rdb)
	describer := app.NewConnectionDescriber(lightLLMClient, deps.tenant, connRepo)
	deps.companySvc = app.NewCompanyService(companyRepo, connRepo, phoneRepo, dsnCipher, deps.tenant, apiSchemaTool, describer).
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
	if cfg.SlackEnabled {
		deps.slackSvc = app.NewSlackService(slackCredRepo, allowedSlackRepo, dsnCipher)
		deps.slackReplier = slack.NewClient(slackCredRepo, dsnCipher, cfg.SlackAPIBaseURL)
		deps.slackDedupe = slack.NewRedisDeduper(rdb)
	}

	// Table-picker embeddings: per-tenant resolution via embedCache.
	// EmbeddingService resolves the client per ReindexSource call so each
	// company hits its own provider/key.
	if cfg.EmbeddingEnabled {
		tableEmbRepo := pgctl.NewTableEmbeddingRepo(controlDB)
		deps.embeddingSvc = app.NewEmbeddingService(connRepo, connRepo, tableEmbRepo, apiSchemaTool, deps.embedCache)
	}
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
	waTransport, err := whatsapp.ResolveTransport(cfg.WhatsAppProvider)
	if err != nil {
		return nil, fmt.Errorf("WhatsApp provider: %w", err)
	}
	deps.wa = waProvider
	deps.waTransport = waTransport

	// Report branding (T-R5). The logo lives in the same bucket the generated
	// documents do, so the API needs the storage client the worker already
	// builds — without MINIO_ENDPOINT the service still answers, minus the
	// logo: a tenant can set a colour and a footer line on a deployment with
	// no object storage at all.
	// Held as the concrete storage service rather than as branding's narrow
	// interface, because two packages want two different slices of it: the
	// logo needs upload and download, and the picture library needs to remove
	// a key as well (T-G12).
	var logoStore *storage.StorageService
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
	// Nil has to be passed as a nil *interface*, not as a typed nil pointer:
	// `branding.Service` tests `s.store == nil`, and a nil *StorageService in
	// an interface is not nil.
	var brandingStore branding.ObjectStore
	if logoStore != nil {
		brandingStore = logoStore
	}
	deps.brandingSvc = branding.NewService(companyRepo, brandingStore, companyRepo)

	// The tenant's picture library (T-G12), on the same bucket and the same
	// condition as the logo above: a deployment with no object storage has no
	// library, `Available()` is false, and the promo card renders as type on a
	// sunburst rather than failing.
	if logoStore != nil {
		deps.postImages = postimage.NewService(pgctl.NewPostImageRepo(controlDB), logoStore)
	}

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
		}).WithVideo(cfg.VideoClient(), cfg.VideoLimits())

		// Share links (T-V4). Inside the storage branch on purpose: the plan a
		// link plays lives in the bucket, so a deployment without one has
		// nothing to share and the routes answer 503 rather than minting
		// tokens for pages that cannot open.
		deps.shareSvc = app.NewReportShareService(
			pgctl.NewReportShareRepo(controlDB), deps.documentRepo, deps.docGen, deps.actionRepo,
		)

		// Uploaded documents (T-P1). Also inside the storage branch, and for a
		// blunter reason than the share links above: there is nowhere to put the
		// bytes. The parse queue is attached only when a parser is configured —
		// without one the upload still works and the document rests at
		// 'uploaded', which is the honest state for a file nothing has read.
		deps.documentIngestSvc = app.NewDocumentIngestService(
			pgctl.NewSourceDocumentRepo(controlDB), deps.storageSvc, cfg.DocMaxUploadMB,
		)
		if cfg.DocParseEnabled {
			deps.documentIngestSvc = deps.documentIngestSvc.WithParseQueue(deps.enqueuer)
		}

		// Review and publish (T-P6/T-P7). The tables are re-derived from the
		// page artifacts on every read, so this needs the same object storage
		// the parse wrote them to and nothing else — the warehouse below is
		// what publishing needs, and a deployment without one still reviews.
		sourceDocs := pgctl.NewSourceDocumentRepo(controlDB)
		deps.documentPageSvc = app.NewDocumentPageService(sourceDocs, deps.storageSvc)
		deps.documentTableSvc = app.NewDocumentTableService(
			sourceDocs, pgctl.NewDocumentTableRepo(controlDB), deps.storageSvc,
		)
		warehouse, err := docwarehouse.New(docwarehouse.Options{DSN: cfg.DocWarehouseDSN})
		switch {
		case err != nil:
			// Configured and unreachable. Warned rather than fatal, and the
			// distinction is the one T-H3's reverted refusals settled: a config
			// check that stops the process turns a feature nobody is using yet
			// into an outage on the deploy that carries it.
			logrus.WithError(err).Warn("document warehouse unreachable; publishing a document table will refuse")
		case warehouse != nil:
			deps.docWarehouse = warehouse
			deps.documentTableSvc = deps.documentTableSvc.WithWarehouse(
				warehouse, connRepo, dsnCipher, deps.tenant, apiSchemaTool,
			)
			// Deleting a document has to take its published rows with it. Wired
			// here rather than inside the ingest service because it is the same
			// condition: without a warehouse there is nothing published to
			// remove.
			deps.documentIngestSvc = deps.documentIngestSvc.WithTableCleanup(deps.documentTableSvc)
			logrus.Info("document warehouse enabled; extracted tables can be published as a source")
		}
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
			Dashboards:          deps.dashboardSvc,
			Scheduled:           deps.scheduledSvc,
			Docs:                deps.docGen,
			MaxQueryRows:        cfg.MaxQueryRows,
			MaxQueryResultBytes: cfg.MaxQueryResultBytes,
		})),
	).WithTemplates(agentTemplates).
		// Validate a submitted MCP binding set against the company's own servers
		// (T-M3). The repo satisfies the lister directly, so this needs no
		// ordering against mcpServerSvc, which is built later.
		WithMCPServers(pgctl.NewMCPServerRepo(controlDB))
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
	mcpRepo := pgctl.NewMCPServerRepo(controlDB)
	// One client for this process: the probe the CRUD makes, and the call an
	// approved `mcp_call` action makes (T-M4), go out through the same guard.
	mcpHTTPClient := mcpclient.NewClient(mcpGuard)
	deps.mcpServerSvc = app.NewMCPServerService(mcpRepo, dsnCipher, mcpHTTPClient)

	// The tenant's written procedures (T-K1). It reaches the roster repository
	// because a binding names an agent, and it holds no credential and makes no
	// outbound call — the whole object is text a company typed about itself.
	// The CRUD surface is the tenant's own rows only: an admin edits what they
	// wrote, and the shipped procedures are code rather than rows they can
	// reach. The merge happens on the read paths a turn uses (T-K8).
	skillRepo := pgctl.NewSkillRepo(controlDB)
	// **Loaded here as well as in the worker, and a malformed one is fatal in
	// both.** T-K8's rule is that a shipped skill over a cap fails the boot of
	// every deployment rather than only the process that happens to read it;
	// until now only the worker loaded them, so the API would have started
	// happily beside a worker that could not. The API needs them anyway: the
	// index cost it reports on the settings screen has to count the block a
	// turn actually carries, and two of its lines are ours.
	builtinSkills, err := skill.LoadBuiltins(cfg.BuiltinSkillsPath)
	if err != nil {
		return nil, fmt.Errorf("load built-in skills: %w", err)
	}
	if len(builtinSkills) > 0 {
		// Said in this process too, in the worker's words. Two processes load
		// the shipped set and only one of them used to say so, which makes
		// "did my procedures load" a question an operator can only answer by
		// reading the other service's log.
		logrus.WithField("count", len(builtinSkills)).Info("built-in skills loaded")
	}
	// The write-time vector T-K5 ranks with. Embedded inside the save because
	// that is the one moment the text is known to be new and somebody is
	// already waiting on a round trip; failing to get one never fails the save.
	deps.skillSvc = app.NewSkillService(skillRepo, agentRepo).
		WithEmbedder(app.NewSkillEmbedder(skillRepo, deps.embedCache)).
		WithIndexReader(skill.WithBuiltins(skillRepo, builtinSkills))
	// "Draft from a conversation" (T-K7), on the light model like T-B4's
	// generator and for its reason: one short structured call, billed because
	// the client is already the metered one. It writes nothing — what comes
	// back lands in the form, and the save above is the authorship event.
	deps.skillDraftSvc = app.NewSkillDraftService(
		lightLLMClient, threadRepo, messageRepo, cfg.EffectiveLightLLMModel(),
	).WithActions(deps.actionRepo).
		WithBudget(deps.usageSvc)

	// The metric registry (T-06). It renders each definition against the tenant
	// pool with the window bound as parameters, so validate-on-save and
	// query_metric run the same SQL the same way.
	deps.metricSvc = app.NewMetricService(pgctl.NewMetricRepo(controlDB), connRepo, deps.tenant).
		WithZeroCoverageProbe(cfg.MetricZeroCoverageProbe)

	// Native dashboards (T-D5→T-D10). The resolver reads through the same tenant
	// pool every other warehouse read goes through, and takes the metric service
	// so a KPI panel is the number query_metric would give the same question in a
	// chat thread rather than a second derivation of it. Built after metricSvc
	// for that reason.
	dashboardRepo := pgctl.NewDashboardRepo(controlDB)
	// One resolver, shared by the authenticated service and the share page, so
	// a stranger's render goes through exactly the code an owner's does — the
	// cache, the timeout, the guard and the query log included. Two resolvers
	// would be two places for the share path to quietly diverge.
	dashResolver := dashboard.NewResolver(connRepo, deps.tenant, deps.metricSvc).
		WithPanelTimeout(time.Duration(cfg.DashboardPanelTimeoutSecs) * time.Second).
		WithCache(dashboard.NewPanelCache(deps.rdb, time.Duration(cfg.DashboardPanelCacheTTLSecs)*time.Second)).
		WithQueryLog(pgctl.NewDashboardQueryLogRepo(controlDB))
	deps.dashboardSvc = app.NewDashboardService(dashboardRepo, connRepo, dashResolver)

	// Dashboard share links (T-D13). The only place in this product where an
	// unauthenticated request runs a query against a customer's warehouse.
	deps.dashboardShareSvc = app.NewDashboardShareService(
		pgctl.NewDashboardShareRepo(controlDB), dashboardRepo, dashResolver, deps.rdb,
	)

	// Answer feedback (T-Q2). It takes the concrete message repo rather than
	// the shared interface: the tenant-scoped single-message read lives on
	// *pgctl.MessageRepo alone, because widening domain.MessageRepository would
	// put a method on six test stubs that nothing else calls.
	feedbackRepo := pgctl.NewMessageFeedbackRepo(controlDB)
	deps.feedbackSvc = app.NewFeedbackService(feedbackRepo, deps.msgRepo)

	// Which next-step chips get pressed (T-U13). Same message repo and the same
	// reason: the pick is validated against the suggestions on the stored
	// message, so the service needs the tenant-scoped single-message read.
	deps.suggestionSvc = app.NewSuggestionService(
		pgctl.NewSuggestionPickRepo(controlDB), deps.msgRepo)

	// The query cookbook (T-Q8). The API owns the admin surface — status,
	// harvest, forget — while the turn-time retrieval lives in the worker's
	// stack. Both read the same table; only the harvester writes it.
	deps.cookbookSvc = app.NewCookbookService(
		pgctl.NewQueryExampleRepo(controlDB), pgctl.NewCookbookCandidateRepo(controlDB),
		feedbackRepo, deps.embedCache,
	)

	// Retention and erasure (T-H6). The API half: the settings write, the
	// erasure route, the export and the record. The nightly purge that uses the
	// same service lives in the worker.
	deps.retentionSvc = app.NewRetentionService(
		pgctl.NewRetentionRepo(controlDB), pgctl.NewDataErasureRepo(controlDB), companyRepo)

	// Watchers (T-08): CRUD and the dry-run. It shares the metric service above,
	// so a dry-run evaluates the same number the worker's fire path will. No
	// delivery providers and no budget: the API neither fires nor bills — the
	// worker's WatcherService, built in the stack, does both.
	deps.watcherSvc = app.NewWatcherService(
		pgctl.NewWatcherRepo(controlDB), deps.metricSvc, threadSvc, companyRepo, deps.enqueuer,
		cfg.WatcherMaxPerCompany,
	)

	// http_action's egress (T-12b) reuses the MCP egress guard's rules — the same
	// address pinning, the same private-range refusal — because "reach a tenant's
	// own system" and "reach a tenant's MCP server" are one threat model: a URL a
	// tenant supplied, fetched from our network position. The timeout is the
	// ticket's fixed 10s rather than the MCP probe's, and StrictClient refuses
	// redirects outright. allowPrivateEgress was already resolved above (and forced
	// off outside development), so a laptop endpoint is reachable for the gate and
	// nothing in production is.
	httpEndpointRepo := pgctl.NewHTTPEndpointRepo(controlDB)
	httpActionGuard := mcpclient.Guard{
		AllowPrivate:      allowPrivateEgress,
		AllowInsecureHTTP: cfg.MCPAllowInsecureHTTP,
		Timeout:           httpActionEgressTimeout,
	}

	// The action framework's execution side (T-10/T-11/T-12a/T-12b). The registry
	// holds the same actions the worker proposes against, so a kind the agent can
	// propose is a kind this process can carry out when a human approves it. The
	// messenger reuses the WhatsApp provider and the phone allowlist already built
	// above — send_message delivers only to numbers this company has authorized;
	// http_action resolves a registered endpoint (decrypting its header) and calls
	// it through the guarded egress.
	actionRegistry := actions.NewRegistry(
		// The linker matters more in this process than in the worker: the worker
		// only *proposes*, and this is where an approval is carried out (T-V3).
		actions.NewSendMessageAction(app.NewActionMessenger(phoneRepo, deps.wa)).
			WithDocuments(documentLinkerOrNil(deps.docGen)),
		actions.NewHTTPAction(
			app.NewHTTPEndpointResolver(httpEndpointRepo, dsnCipher),
			app.NewHTTPActionEgress(httpActionGuard, 0),
		),
		// T-M4. This process is where approval executes, so a registry without it
		// would accept the approval and fail the invocation closed with "action
		// kind no longer available" — the agent proposes in the worker and the
		// human approves here, and the two registries have to agree.
		actions.NewMCPCall(
			app.NewMCPCallStore(mcpRepo, dsnCipher),
			mcpHTTPClient,
			time.Duration(cfg.MCPCallTimeoutSecs)*time.Second,
			cfg.MCPMaxResponseBytes,
		),
	)
	deps.actionSvc = app.NewActionService(pgctl.NewActionRepo(controlDB), actionRegistry, deps.actionRepo)

	// Outbound webhooks (T-15). The API holds the subscription CRUD and — because
	// this is where a human approves an action — the publisher for
	// `action.executed`. Delivery is webhookout's, the same package T-A2 built for
	// report callbacks: this process registers a delivery row and queues it, and
	// the worker makes the attempts.
	deps.webhookSubsSvc = app.NewWebhookSubscriptionService(
		pgctl.NewWebhookSubscriptionRepo(controlDB),
		webhookout.NewSender(
			pgctl.NewWebhookDeliveryRepo(controlDB), companyRepo, deps.enqueuer,
			cfg.APIV1CallbackAllowPrivate,
		),
	)
	deps.actionSvc = deps.actionSvc.WithWebhooks(deps.webhookSubsSvc)
	deps.scheduledSvc = deps.scheduledSvc.WithWebhooks(deps.webhookSubsSvc)

	// The admin CRUD for those endpoints. It shares the egress guard so a host it
	// rejects at registration is the host the turn-time dial would reject too.
	deps.httpEndpointSvc = app.NewHTTPEndpointService(httpEndpointRepo, dsnCipher, httpActionGuard)

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

	// Queue depth (T-17). Sampled here rather than on the worker because this
	// process serves /metrics, and a gauge that lives in a process nothing
	// scrapes is not an exported metric.
	depthCtx, stopDepth := context.WithCancel(ctx)
	deps.stopQueueDepth = stopDepth
	go queue.NewDepthPoller(asynqOpt, deps.metrics, 0).Run(depthCtx)

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

// documentLinkerOrNil is budgetReaderOrNil for the document linker (T-V3).
// Fourth instance of the same trap: a nil *docgen.Service assigned into an
// interface arrives non-nil, and the action's own guard would not fire.
func documentLinkerOrNil(d *docgen.Service) actions.DocumentLinker {
	if d == nil {
		return nil
	}
	return d
}
