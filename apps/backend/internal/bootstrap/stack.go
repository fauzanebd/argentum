// Package bootstrap builds the agent stack — repositories, tenant pool,
// per-tenant LLM caches, tools, guardrails and the agent factory — from a
// Config.
//
// It exists so that there is exactly one definition of "how Argentum runs a
// chat turn". Before this package, that definition lived inside
// cmd/worker/main.go, which meant anything else wanting to exercise the real
// agent (the eval harness in T-01, and cmd/mcp later) had to copy 150 lines
// of wiring and then drift from it. An eval harness that scores a slightly
// different agent than production runs is worse than no eval harness,
// because it reports confidence it has not earned.
//
// What stays out of here on purpose: the asynq server and periodic task
// manager, the WhatsApp and Lark providers, and the event bus. Those are
// process-shaped decisions — the worker wants Redis pub/sub and outbound
// delivery, the eval harness wants an in-memory recorder and no delivery at
// all — so callers supply them to NewChatRunner.
package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/llmclient"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/theme"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// Stack is everything a process needs to run agent turns. Construct with
// New, release with Close.
type Stack struct {
	Cfg       *config.Config
	ControlDB *sql.DB
	Redis     *redis.Client
	AsynqOpt  asynq.RedisConnOpt
	DSNCipher *crypto.DSNCipher

	Connections   *pgctl.ConnectionRepo
	Threads       domain.ThreadRepository
	Messages      domain.MessageRepository
	Usage         domain.UsageRepository
	Companies     domain.CompanyRepository
	ScheduledRepo domain.ScheduledTaskRepository
	AgentActions  domain.AgentActionRepository

	TenantPool *db.TenantConnPool
	UsageSvc   *app.UsageService
	LLMCache   *llmtenant.ClientCache
	EmbedCache *llmtenant.EmbeddingCache

	ThreadSvc    *app.ThreadService
	ScheduledSvc *app.ScheduledTaskService
	// MetabaseSync registers a tenant DSN as a Metabase database. Exposed
	// because a source created outside the HTTP API — the eval harness seeds
	// its own — is invisible to Metabase until this runs, and every
	// create_visualization call against it fails.
	MetabaseSync *app.MetabaseWarehouseSync

	Tools        []interfaces.Tool
	AgentFactory app.AgentFactory
	Budget       agentbudget.Budget

	// Docs is the one path from a spec to a stored document (T-A2). Nil when
	// the deployment has no object storage, which is the same condition that
	// leaves generate_document unregistered — the worker's async render task
	// checks it for exactly that reason.
	Docs      *docgen.Service
	Documents domain.DocumentRepository

	tableEmbeddings domain.TableEmbeddingRepository
	scheduledEnq    *queue.Enqueuer
	closers         []func()
}

// New wires the stack. ctx governs the background refresh loops on the
// tenant pool and the LLM caches; cancel it before calling Close.
func New(ctx context.Context, cfg *config.Config) (*Stack, error) {
	s := &Stack{Cfg: cfg}

	// Report fonts are checked here, before anything can render, because a
	// broken face must stop a boot rather than surface hours later as a
	// customer's failed document (T-R1). The faces are embedded, so this can
	// only fail on a corrupt file — which is exactly the case a compile-time
	// check cannot see.
	if err := theme.VerifyFonts(); err != nil {
		return nil, fmt.Errorf("report theme: %w", err)
	}

	controlDB, err := pgctl.New(cfg.DatabaseURL())
	if err != nil {
		return nil, fmt.Errorf("control DB: %w", err)
	}
	s.ControlDB = controlDB
	s.onClose(func() { _ = controlDB.Close() })

	s.Connections = pgctl.NewConnectionRepo(controlDB)
	s.Threads = pgctl.NewThreadRepo(controlDB)
	s.Messages = pgctl.NewMessageRepo(controlDB)
	s.Usage = pgctl.NewUsageRepo(controlDB)
	// Kept concretely as well as behind the interface: the branding record
	// lives on the company row, and domain.CompanyRepository deliberately does
	// not carry it (see domain.BrandingRepository for why).
	companyRepo := pgctl.NewCompanyRepo(controlDB)
	s.Companies = companyRepo
	s.ScheduledRepo = pgctl.NewScheduledTaskRepo(controlDB)
	s.AgentActions = pgctl.NewAgentActionRepo(controlDB)
	creditsRepo := pgctl.NewCreditsRepo(controlDB)
	llmCredRepo := pgctl.NewCompanyLLMCredentialRepo(controlDB)

	dsnCipher, err := crypto.NewFromHex(cfg.DSNEncryptionKeyHex)
	if err != nil {
		return nil, fmt.Errorf("DSN cipher: %w", err)
	}
	s.DSNCipher = dsnCipher

	resolver := pgctl.NewConnectionResolver(s.Connections, dsnCipher)
	s.TenantPool = db.NewTenantConnPool(resolver, 200, 30*time.Minute)
	s.TenantPool.Start(ctx)
	s.onClose(s.TenantPool.CloseAll)

	s.Redis = buildRedisClient(cfg)
	if s.Redis == nil {
		return nil, fmt.Errorf("redis client is required (REDIS_URL)")
	}
	s.onClose(func() { _ = s.Redis.Close() })

	s.UsageSvc = app.NewUsageService(s.Usage, creditsRepo, app.DefaultPricing).
		WithCredits(app.CreditPolicy{
			Enforce:       cfg.CreditsEnforcementEnabled,
			WarnPct:       cfg.CreditsWarningThresholdPct,
			GrantMicroUSD: cfg.CreditsDefaultGrantMicroUSD(),
		}, llmCredRepo, app.NewRedisBudgetCache(s.Redis))

	// Env-default light LLM for the process-wide consumers (topic
	// classifier, rolling thread summary) that carry no tenant context.
	rawLightLLM, err := llmclient.BuildLight(cfg)
	if err != nil {
		return nil, fmt.Errorf("light LLM: %w", err)
	}
	lightLLMClient := app.NewMeteredLLM(rawLightLLM, cfg.EffectiveLightLLMModel(), s.UsageSvc)

	llmResolver := llmtenant.NewResolver(llmCredRepo, dsnCipher, cfg)
	s.LLMCache = llmtenant.NewClientCache(
		llmResolver,
		func(inner interfaces.LLM, model string) interfaces.LLM {
			return app.NewMeteredLLM(inner, model, s.UsageSvc)
		},
		300, 30*time.Minute,
	)
	s.LLMCache.Start(ctx)
	s.onClose(s.LLMCache.CloseAll)

	s.EmbedCache = llmtenant.NewEmbeddingCache(llmResolver, 100, 30*time.Minute)
	s.EmbedCache.Start(ctx)
	s.onClose(s.EmbedCache.CloseAll)

	asynqOpt, err := queue.BuildRedisOpt(cfg.ResolvedAsynqRedisURL(), cfg.RedisPassword)
	if err != nil {
		return nil, fmt.Errorf("asynq redis opt: %w", err)
	}
	s.AsynqOpt = asynqOpt
	s.scheduledEnq = queue.NewEnqueuer(asynqOpt)
	s.onClose(func() { _ = s.scheduledEnq.Close() })

	classifierLLM := lightLLMClient
	if cfg.ClassifierModel != "" {
		rawClassifier, err := llmclient.BuildClassifier(cfg)
		if err != nil {
			logrus.WithError(err).Warn("classifier LLM build failed; falling back to light LLM")
		} else {
			classifierLLM = app.NewMeteredLLM(rawClassifier, cfg.EffectiveClassifierModel(), s.UsageSvc)
		}
	}
	classifier := app.NewTopicClassifier(classifierLLM)
	s.ThreadSvc = app.NewThreadService(s.Threads, s.Messages, classifier, lightLLMClient,
		app.ThreadServiceConfig{
			IdleMinutes:        cfg.ThreadIdleMinutes,
			SummaryEveryNTurns: cfg.SummaryEveryNTurns,
		})
	// This is the construction the worker fires schedules through, so this
	// WithBudget is the one that actually refuses an unattended tick (T-03).
	s.ScheduledSvc = app.NewScheduledTaskService(s.ScheduledRepo, s.ThreadSvc, s.Companies, s.scheduledEnq).
		WithBudget(s.UsageSvc)

	metabaseClient := metabase.NewClient(
		cfg.MetabaseURL, cfg.MetabasePublicURL,
		cfg.MetabaseAdminEmail, cfg.MetabaseAdminPassword,
	)
	s.MetabaseSync = app.NewMetabaseWarehouseSync(metabaseClient)
	dashboardRepo := pgctl.NewDashboardRepo(controlDB)
	documentRepo := pgctl.NewDocumentRepo(controlDB)
	s.Documents = documentRepo

	s.Tools = []interfaces.Tool{
		tools.NewListSourcesTool(s.Connections),
		tools.NewGetSchemaToolWithRedis(s.TenantPool, s.Connections, s.Redis),
		tools.NewRunSQLTool(s.TenantPool, s.Connections, s.UsageSvc, cfg.MaxQueryRows, cfg.MaxQueryResultBytes),
		tools.NewCreateVisualizationTool(s.TenantPool, s.Connections, metabaseClient, s.Connections, s.UsageSvc),
		tools.NewCreateDashboardTool(metabaseClient, s.UsageSvc, app.NewDashboardService(dashboardRepo, metabaseClient)),
		tools.NewScheduleTaskTool(s.ScheduledSvc),
	}
	if storageSvc, err := buildStorageService(cfg); err != nil {
		logrus.WithError(err).Warn("storage disabled; generate_document tool will not be registered")
	} else if storageSvc != nil {
		presignTTL := time.Duration(cfg.DocumentPresignTTLSecs) * time.Second
		// The branding service reads the same bucket it writes logos to, and
		// the same company row the API's Reports tab writes (T-R5). One
		// resolver, so a document generated from chat carries exactly what the
		// preview showed.
		brandingSvc := branding.NewService(companyRepo, storageSvc, s.Companies)
		// One generator for the tool and for `/v1` (T-A2). The API builds its
		// own instance in cmd/api — separate process, same constructor — and
		// installs the untrusted-spec caps on top; the agent's path leaves them
		// off, which is the only difference between the two.
		s.Docs = docgen.New(storageSvc, documentRepo, s.Companies, brandingSvc, s.UsageSvc, presignTTL)
		s.Tools = append(s.Tools, tools.NewGenerateDocumentTool(s.Docs))
		logrus.WithFields(logrus.Fields{
			"bucket":   cfg.MinIOBucket,
			"endpoint": cfg.MinIOEndpoint,
		}).Info("generate_document tool enabled")
	}

	// Every tool runs behind the per-turn budget guard (T-16). Wrapping here
	// rather than at each construction site means a tool added later cannot
	// forget to be bounded: s.Tools is the registry, and nothing reaches the
	// agent except through it.
	s.Budget = agentbudget.Budget{
		MaxIterations: cfg.AgentMaxIterations,
		MaxToolCalls:  cfg.AgentMaxToolCalls,
		MaxTokens:     cfg.AgentMaxTurnTokens,
		Wall:          time.Duration(cfg.AgentTurnBudgetSecs) * time.Second,
	}.Normalize()
	s.Tools = agentbudget.GuardAll(s.Tools)

	// Audit outside the budget guard (T-05): a refused call returns a refusal
	// string with a nil error, so wrapping the other way round would record it
	// as an ordinary success — and "the agent tried to run one more query and
	// was stopped" is the line an incident review reads first.
	s.Tools = tools.WithAuditAll(s.Tools, s.AgentActions)

	mem := buildMemory(cfg)
	guardrailsTpl := buildGuardrails(cfg, lightLLMClient)

	var agentCfgOpt sdkagent.Option
	if cfg.AgentConfigPath != "" {
		if configs, err := sdkagent.LoadAgentConfigsFromFile(cfg.AgentConfigPath); err == nil {
			if agentCfg, ok := configs["analytics_agent"]; ok {
				agentCfgOpt = sdkagent.WithAgentConfig(agentCfg, nil)
			}
		}
	}

	systemPrompt := SystemPrompt()
	agentTools := s.Tools

	// The registry, by name, once per boot. The SDK looks a tool call up by
	// matching `tool.Name()` against what the model asked for and logs a bare
	// "Tool not found" when it misses — which is indistinguishable, in a log,
	// from a tool that was never registered. One line here tells the two
	// apart, and T-A2's gate needed exactly that.
	names := make([]string, 0, len(agentTools))
	for _, t := range agentTools {
		names = append(names, t.Name())
	}
	logrus.WithField("tools", names).Info("agent tool registry")

	// AgentFactory builds a fresh sdkagent.Agent per chat turn from the
	// per-tenant primary + light LLM clients. Tools / memory / system
	// prompt / streaming config are captured here once; primaryInterface
	// gates Anthropic-only prompt caching.
	s.AgentFactory = func(primary, light interfaces.LLM, primaryInterface string) (*sdkagent.Agent, error) {
		opts := []sdkagent.Option{
			sdkagent.WithLLM(primary),
			sdkagent.WithTools(agentTools...),
			sdkagent.WithMemory(mem),
			sdkagent.WithSystemPrompt(systemPrompt),
			sdkagent.WithName("Argentum"),
			sdkagent.WithDescription("Conversational analytics agent for B2B owners."),
			sdkagent.WithMaxIterations(s.Budget.MaxIterations),
			sdkagent.WithRequirePlanApproval(false),
			sdkagent.WithLLMConfig(interfaces.LLMConfig{Temperature: 0.2}),
			// Stream content from every iteration immediately. The SDK's default
			// filtering (filterIntermediateContent) has a bug: when the agent
			// finishes before maxIterations, content from the final iteration is
			// captured but never replayed — resulting in empty assistant messages
			// after tool calls.
			sdkagent.WithStreamConfig(&interfaces.StreamConfig{
				IncludeIntermediateMessages: true,
			}),
		}
		// Anthropic prompt caching: cache the system prompt, the tool definitions,
		// and the rolling conversation prefix so each turn only pays for the new
		// user message + assistant delta. With ~70k-token schema results in
		// history, this saves ~90% of input tokens on follow-up turns.
		if primaryInterface == config.LLMInterfaceAnthropic {
			opts = append(opts, sdkagent.WithCacheConfig(interfaces.CacheConfig{
				CacheSystemMessage: true,
				CacheTools:         true,
				CacheConversation:  true,
				CacheTTL:           "5m",
			}))
		}
		if guardrailsTpl != nil {
			opts = append(opts, sdkagent.WithGuardrails(guardrailsTpl.WithLLM(light)))
		}
		if agentCfgOpt != nil {
			opts = append(opts, agentCfgOpt)
		}
		return sdkagent.NewAgent(opts...)
	}

	if cfg.EmbeddingEnabled {
		s.tableEmbeddings = pgctl.NewTableEmbeddingRepo(controlDB)
	}

	return s, nil
}

// NewChatRunner builds the runner over this stack. bus is required; wa may
// be nil when the caller does not deliver to WhatsApp (the eval harness does
// not). Table-picker embeddings are attached when the config enables them,
// matching worker behaviour.
func (s *Stack) NewChatRunner(bus app.EventBus, wa whatsapp.Provider) *app.ChatRunner {
	runner := app.NewChatRunner(
		s.ThreadSvc, s.Messages, s.Threads, s.Connections,
		s.AgentFactory, s.LLMCache, bus, wa, s.TenantPool,
		s.ScheduledSvc, s.Cfg.HistoryHydrateLimit,
	).WithBudget(func(context.Context, string) agentbudget.Budget { return s.Budget }).
		WithActionLog(s.AgentActions)
	if s.tableEmbeddings != nil {
		runner = runner.WithTablePicker(s.tableEmbeddings, s.EmbedCache, s.Cfg.EmbeddingTopK)
		logrus.WithFields(logrus.Fields{
			"model": s.Cfg.EmbeddingModel,
			"topk":  s.Cfg.EmbeddingTopK,
		}).Info("table-picker embeddings enabled (per-tenant cache)")
	}
	return runner
}

// TableEmbeddings returns the repo when the table picker is enabled, nil
// otherwise. Callers that need it directly (reindex paths) can ask.
func (s *Stack) TableEmbeddings() domain.TableEmbeddingRepository { return s.tableEmbeddings }

// Enqueuer is the shared asynq client. The worker needs it to queue a webhook
// delivery from inside a task it is already running (T-A2), which is the one
// place a consumer is also a producer — every other caller is in cmd/api.
func (s *Stack) Enqueuer() *queue.Enqueuer { return s.scheduledEnq }

// Close releases every resource the stack opened, in reverse order.
func (s *Stack) Close() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
}

func (s *Stack) onClose(f func()) { s.closers = append(s.closers, f) }

func buildRedisClient(cfg *config.Config) *redis.Client {
	if cfg.RedisURL == "" {
		return nil
	}
	url := cfg.RedisURL
	if !strings.Contains(url, "://") {
		return redis.NewClient(&redis.Options{Addr: url, Password: cfg.RedisPassword})
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		logrus.WithError(err).Warn("redis: invalid REDIS_URL; using bare addr")
		return redis.NewClient(&redis.Options{Addr: url, Password: cfg.RedisPassword})
	}
	if cfg.RedisPassword != "" {
		opt.Password = cfg.RedisPassword
	}
	return redis.NewClient(opt)
}

func buildMemory(cfg *config.Config) interfaces.Memory {
	if cfg.RedisURL != "" {
		mem, err := memory.NewRedisMemoryFromConfig(memory.RedisConfig{
			URL: cfg.RedisDialAddr(), Password: cfg.RedisPassword, DB: 0,
		})
		if err != nil {
			logrus.WithError(err).Warn("redis memory unavailable; falling back to buffer")
		} else {
			return mem
		}
	}
	return memory.NewConversationBuffer(memory.WithMaxSize(20))
}

func buildGuardrails(cfg *config.Config, llm interfaces.LLM) *guardrails.Analytics {
	if cfg.GuardrailsConfigPath == "" {
		return nil
	}
	gr, err := guardrails.LoadFromFile(cfg.GuardrailsConfigPath, llm)
	if err != nil {
		logrus.WithError(err).Warn("guardrails disabled")
		return nil
	}
	return gr
}

// buildStorageService returns a configured MinIO/S3 client, or (nil, nil)
// when no MINIO_ENDPOINT is set (object storage is optional — without it
// the generate_document tool simply isn't registered).
func buildStorageService(cfg *config.Config) (*storage.StorageService, error) {
	if cfg.MinIOEndpoint == "" {
		return nil, nil
	}
	return storage.NewStorageService(&storage.MinIOConfig{
		Endpoint:        cfg.MinIOEndpoint,
		AccessKeyID:     cfg.MinIOAccessKeyID,
		SecretAccessKey: cfg.MinIOSecretAccessKey,
		Bucket:          cfg.MinIOBucket,
		UseSSL:          cfg.MinIOUseSSL,
	})
}
