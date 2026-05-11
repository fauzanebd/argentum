// Argentum worker: consumes asynq tasks (`chat:run`, `scheduled:run`) and
// runs the analytics agent. Stays offline-friendly: any number of replicas
// can share the same Redis-backed queue; failures retry automatically;
// chat events fan out to API replicas via Redis pub/sub. The same process
// also hosts the asynq.PeriodicTaskManager that emits `scheduled:run`
// ticks for enabled scheduled_tasks rows.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/mysql"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/sqlserver"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/llmclient"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}
	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.Info("Argentum worker starting")

	// --- Control DB (read-only path: thread + message + usage repos) ---
	controlDB, err := pgctl.New(cfg.DatabaseURL())
	if err != nil {
		logrus.Fatalf("control DB: %v", err)
	}
	defer controlDB.Close()

	connRepo := pgctl.NewConnectionRepo(controlDB)
	threadRepo := pgctl.NewThreadRepo(controlDB)
	messageRepo := pgctl.NewMessageRepo(controlDB)
	usageRepo := pgctl.NewUsageRepo(controlDB)
	creditsRepo := pgctl.NewCreditsRepo(controlDB)
	companyRepo := pgctl.NewCompanyRepo(controlDB)
	scheduledRepo := pgctl.NewScheduledTaskRepo(controlDB)

	// --- Crypto (DSN decryption for tenant pool) ---
	dsnCipher, err := crypto.NewFromHex(cfg.DSNEncryptionKeyHex)
	if err != nil {
		logrus.Fatalf("DSN cipher: %v", err)
	}

	// --- Tenant DB pool ---
	resolver := pgctl.NewConnectionResolver(connRepo, dsnCipher)
	tenantPool := db.NewTenantConnPool(resolver, 200, 30*time.Minute)
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	tenantPool.Start(rootCtx)
	defer tenantPool.CloseAll()

	// --- Redis client (event publishing) ---
	rdb := buildRedisClient(cfg)
	if rdb == nil {
		logrus.Fatal("redis client is required (REDIS_URL)")
	}
	defer rdb.Close()
	bus := eventbus.NewRedisBus(rdb)

	// --- LLM (metered) ---
	usageSvc := app.NewUsageService(usageRepo, creditsRepo, app.DefaultPricing)
	rawLLM, err := llmclient.BuildPrimary(cfg)
	if err != nil {
		logrus.Fatalf("primary LLM: %v", err)
	}
	llmClient := app.NewMeteredLLM(rawLLM, usageSvc)
	rawLightLLM, err := llmclient.BuildLight(cfg)
	if err != nil {
		logrus.Fatalf("light LLM: %v", err)
	}
	lightLLMClient := app.NewMeteredLLM(rawLightLLM, usageSvc)

	// --- Agent + tools ---
	metabaseClient := metabase.NewClient(
		cfg.MetabaseURL, cfg.MetabasePublicURL,
		cfg.MetabaseAdminEmail, cfg.MetabaseAdminPassword,
	)
	getSchemaTool := tools.NewGetSchemaToolWithRedis(tenantPool, connRepo, rdb)
	dashboardRepo := pgctl.NewDashboardRepo(controlDB)
	documentRepo := pgctl.NewDocumentRepo(controlDB)

	// Thread service + scheduled-task service must exist before the agent
	// tools slice is built so schedule_task can be registered.
	asynqOpt, err := queue.BuildRedisOpt(cfg.ResolvedAsynqRedisURL(), cfg.RedisPassword)
	if err != nil {
		logrus.Fatalf("asynq redis opt: %v", err)
	}
	scheduledEnq := queue.NewEnqueuer(asynqOpt)
	defer scheduledEnq.Close()
	classifier := app.NewTopicClassifier(lightLLMClient)
	threadSvc := app.NewThreadService(threadRepo, messageRepo, classifier, llmClient,
		app.ThreadServiceConfig{
			IdleMinutes:        cfg.ThreadIdleMinutes,
			SummaryEveryNTurns: cfg.SummaryEveryNTurns,
		})
	scheduledSvc := app.NewScheduledTaskService(scheduledRepo, threadSvc, companyRepo, scheduledEnq)

	agentTools := []interfaces.Tool{
		tools.NewListSourcesTool(connRepo),
		getSchemaTool,
		tools.NewRunSQLTool(tenantPool, connRepo, usageSvc),
		tools.NewCreateVisualizationTool(tenantPool, connRepo, metabaseClient, connRepo, usageSvc),
		tools.NewCreateDashboardTool(metabaseClient, usageSvc, app.NewDashboardService(dashboardRepo, metabaseClient)),
		tools.NewScheduleTaskTool(scheduledSvc),
	}
	if storageSvc, err := buildStorageService(cfg); err != nil {
		logrus.WithError(err).Warn("storage disabled; generate_document tool will not be registered")
	} else if storageSvc != nil {
		presignTTL := time.Duration(cfg.DocumentPresignTTLSecs) * time.Second
		agentTools = append(agentTools, tools.NewGenerateDocumentTool(storageSvc, documentRepo, usageSvc, presignTTL))
		logrus.WithFields(logrus.Fields{
			"bucket":   cfg.MinIOBucket,
			"endpoint": cfg.MinIOEndpoint,
		}).Info("generate_document tool enabled")
	}
	mem := buildMemory(cfg)
	gr := buildGuardrails(cfg, lightLLMClient)

	systemPrompt := buildSystemPrompt()
	agentOpts := []sdkagent.Option{
		sdkagent.WithLLM(llmClient),
		sdkagent.WithTools(agentTools...),
		sdkagent.WithMemory(mem),
		sdkagent.WithSystemPrompt(systemPrompt),
		sdkagent.WithName("Argentum"),
		sdkagent.WithDescription("Conversational analytics agent for B2B owners."),
		sdkagent.WithMaxIterations(3),
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
	if gr != nil {
		agentOpts = append(agentOpts, sdkagent.WithGuardrails(gr))
	}
	if cfg.AgentConfigPath != "" {
		if configs, err := sdkagent.LoadAgentConfigsFromFile(cfg.AgentConfigPath); err == nil {
			if agentCfg, ok := configs["analytics_agent"]; ok {
				agentOpts = append(agentOpts, sdkagent.WithAgentConfig(agentCfg, nil))
			}
		}
	}
	analyticsAgent, err := sdkagent.NewAgent(agentOpts...)
	if err != nil {
		logrus.Fatalf("create agent: %v", err)
	}

	// --- WhatsApp provider (worker sends final replies for WA threads) ---
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
		logrus.Fatalf("WhatsApp provider: %v", err)
	}

	runner := app.NewChatRunner(threadSvc, messageRepo, threadRepo, connRepo, analyticsAgent, bus, waProvider, tenantPool, scheduledSvc, cfg.HistoryHydrateLimit)

	// --- asynq.Server ---
	srv := asynq.NewServer(asynqOpt, asynq.Config{
		Concurrency: cfg.WorkerConcurrency,
		Queues:      cfg.WorkerQueueMap(),
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			logrus.WithError(err).WithField("task", t.Type()).Error("task failed")
		}),
		Logger: &logrusAsynqLogger{},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeChatRun, makeChatRunHandler(runner))
	mux.HandleFunc(queue.TypeScheduledTaskRun, makeScheduledRunHandler(scheduledSvc))

	// --- Periodic task manager ---
	// Polls scheduled_tasks every SyncInterval and registers/refreshes one
	// asynq Scheduler entry per enabled row. Newly created tasks become
	// live within ~SyncInterval without a worker restart.
	pm, err := asynq.NewPeriodicTaskManager(asynq.PeriodicTaskManagerOpts{
		PeriodicTaskConfigProvider: queue.NewDBConfigProvider(scheduledRepo),
		RedisConnOpt:               asynqOpt,
		SyncInterval:               30 * time.Second,
	})
	if err != nil {
		logrus.Fatalf("periodic task manager: %v", err)
	}
	if err := pm.Start(); err != nil {
		logrus.Fatalf("periodic task manager start: %v", err)
	}
	defer pm.Shutdown()

	// Run blocks until OS signal. Capture signals here so we can shut
	// the asynq server down gracefully (it will let in-flight tasks
	// finish before exiting).
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logrus.Info("Shutting down worker…")
		srv.Shutdown()
	}()

	if err := srv.Run(mux); err != nil {
		logrus.Fatalf("asynq server: %v", err)
	}
	logrus.Info("Bye")
}

// makeChatRunHandler adapts ChatRunner.Run to asynq's HandlerFunc signature.
// Returning a non-nil error triggers asynq's retry/backoff machinery.
func makeChatRunHandler(runner *app.ChatRunner) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.ChatRunPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			// Malformed payload: SkipRetry so the bad task is archived
			// instead of looping forever.
			return asynq.SkipRetry
		}
		return runner.Run(ctx, p)
	}
}

// makeScheduledRunHandler dispatches a periodic `scheduled:run` tick. The
// payload only carries a task ID; the service reloads the latest
// definition and enqueues a regular chat:run against the dedicated thread.
func makeScheduledRunHandler(svc *app.ScheduledTaskService) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.ScheduledRunPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		if p.TaskID == "" {
			return asynq.SkipRetry
		}
		return svc.HandleFire(ctx, p.TaskID)
	}
}

// logrusAsynqLogger forwards asynq's internal log messages into the same
// logrus pipeline the rest of the worker uses.
type logrusAsynqLogger struct{}

func (l *logrusAsynqLogger) Debug(args ...interface{}) { logrus.Debug(args...) }
func (l *logrusAsynqLogger) Info(args ...interface{})  { logrus.Info(args...) }
func (l *logrusAsynqLogger) Warn(args ...interface{})  { logrus.Warn(args...) }
func (l *logrusAsynqLogger) Error(args ...interface{}) { logrus.Error(args...) }
func (l *logrusAsynqLogger) Fatal(args ...interface{}) { logrus.Fatal(args...) }

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

func buildGuardrails(cfg *config.Config, llm interfaces.LLM) interfaces.Guardrails {
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
// the generate_report tool simply isn't registered).
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

func buildSystemPrompt() string {
	return `You are Argentum, an expert data analyst helping business owners understand their metrics.

You have access to these tools:
- list_sources: List the data sources (analytical databases) registered for this organization. Returns id, label, db_type, description, is_default for each.
- get_schema: Without source_id, returns the source catalog. With source_id, returns that source's tables, columns, and relationships.
- run_sql: Execute a read-only SELECT against ONE source. Pass source_id when more than one source is registered.
- create_visualization: Create a Metabase card from a SQL query against ONE source. Pass source_id when more than one source is registered. Returns card_id and chart_type.
- create_dashboard: Combine multiple card_ids into a single Metabase dashboard with a shareable URL.
- generate_document: Generate a downloadable file (PDF, XLSX, or CSV) from a structured spec. Generic-purpose: invoices, agreements, terms & conditions, research summaries, data exports, ad-hoc reports — any artifact the user wants to download. Returns a presigned download URL — embed it as a markdown link with descriptive text. (Only available when object storage is configured.)
- schedule_task: Create a recurring scheduled task. Each run executes a saved prompt through this agent and writes the result to a dedicated thread. Parameters: name, prompt (the instruction to run), cron_expression (5-field cron, e.g. "0 7 * * 1" = Mondays 07:00), timezone (IANA, default UTC). When the user's request is ambiguous about WHAT to run, WHEN, or in WHICH timezone, ASK the user to clarify before calling schedule_task. After it returns, tell the user the task was scheduled and quote the task_id; do not invent a URL — the dashboard renders the task by id.

CRITICAL GUIDELINES:
1. LANGUAGE IS THE TOP PRIORITY: Detect the language of the user's message and reply ONLY in that exact same language. If the user writes in English, reply fully in English. If the user writes in Indonesian/Bahasa Indonesia, reply fully in Indonesian. Never mix languages and never default to Indonesian when the user wrote in English.
2. ONLY call tools when the user asks a question that requires database data or a visualization. For greetings ("hi", "hello", "test"), small-talk, or general conversation that does NOT need data, reply directly without calling any tools.
3. MULTI-SOURCE: An organization can have several databases. The available sources are listed in the "[System context: Available data sources …]" block prepended to the user's message. Pick the source whose description best matches the user's question. To answer a question that spans sources, issue ONE run_sql per source and combine results in your reply — never JOIN across sources in a single SQL statement.
4. AMBIGUITY: If the user's question doesn't clearly map to one source (e.g. "how many users do we have?" with both a CRM and an HRIS source), ASK the user which source they mean BEFORE running SQL. Do not guess. If only one source exists, use it without asking.
5. When you DO need to query data: call get_schema with the chosen source_id FIRST if you are unsure about table or column names. Never invent identifiers.
6. SQL DIALECT: Each get_schema / run_sql / create_visualization response includes a "db_type" field (postgres, mysql, or sqlserver). Generate SQL in that exact dialect; different sources may use different dialects.
   - postgres: DATE_TRUNC, STRING_AGG, NOW(), LIMIT n.
   - mysql: DATE_FORMAT, GROUP_CONCAT, NOW(), DATE_ADD/DATE_SUB, LIMIT n.
   - sqlserver: DATEADD/DATEDIFF/DATEPART (no DATE_TRUNC), STRING_AGG, SYSDATETIME()/GETDATE(), TOP n (or OFFSET … FETCH NEXT … with ORDER BY); identifiers in [brackets]; tables live in dbo.
7. When the user wants charts/graphs/dashboards: call create_visualization for each card (with the appropriate source_id), then create_dashboard ONCE.
   - After create_visualization returns, copy the exact "dashboard_cards" array into create_dashboard's "cards" parameter.
   - Alternatively, pass just "card_ids": [123, 456] to create_dashboard.
   - When returning the dashboard URL to the user, format it as a markdown link with descriptive text, e.g. [Sales Performance Dashboard](url). Never show the raw URL.
   - Time-series charts (line/bar/combo where an axis is date, datetime, month, week, quarter, year, or similar): put earliest periods first and latest last. In SQL, ORDER BY the true time dimension ascending (use the underlying date/timestamp for grouping labels if needed). Never rely on unspecified row order and do not use DESC for the time axis unless the user explicitly asks for newest-first.
8. NEVER return individual card IDs to the user — always wrap with a dashboard.
9. Cap result sets to 100 rows unless explicitly asked otherwise (LIMIT 100 in postgres/mysql, TOP 100 in sqlserver).`
}
