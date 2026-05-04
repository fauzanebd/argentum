// Argentum worker: consumes asynq tasks (today only `chat:run`) and runs
// the analytics agent. Stays offline-friendly: any number of replicas can
// share the same Redis-backed queue; failures retry automatically; chat
// events fan out to API replicas via Redis pub/sub.
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
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/openai"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/mysql"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/guardrails"
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
	rawLLM := buildLLM(cfg)
	llmClient := app.NewMeteredLLM(rawLLM, usageSvc)
	lightLLMClient := buildLightLLM(cfg)

	// --- Agent + tools ---
	metabaseClient := metabase.NewClient(
		cfg.MetabaseURL, cfg.MetabasePublicURL,
		cfg.MetabaseAdminEmail, cfg.MetabaseAdminPassword,
	)
	getSchemaTool := tools.NewGetSchemaTool(tenantPool)
	dashboardRepo := pgctl.NewDashboardRepo(controlDB)
	agentTools := []interfaces.Tool{
		getSchemaTool,
		tools.NewRunSQLTool(tenantPool, usageSvc),
		tools.NewCreateVisualizationTool(tenantPool, metabaseClient, connRepo, usageSvc),
		tools.NewCreateDashboardTool(metabaseClient, usageSvc, app.NewDashboardService(dashboardRepo, metabaseClient)),
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
		sdkagent.WithMaxIterations(5),
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

	// --- Thread service (worker uses it for AppendAssistantMessage +
	//     summary refresh; uses the same metered LLM). ---
	classifier := app.NewTopicClassifier(lightLLMClient)
	threadSvc := app.NewThreadService(threadRepo, messageRepo, classifier, llmClient,
		app.ThreadServiceConfig{
			IdleMinutes:        cfg.ThreadIdleMinutes,
			SummaryEveryNTurns: cfg.SummaryEveryNTurns,
		})

	runner := app.NewChatRunner(threadSvc, messageRepo, threadRepo, analyticsAgent, bus, waProvider, tenantPool, getSchemaTool)

	// --- asynq.Server ---
	asynqOpt, err := queue.BuildRedisOpt(cfg.ResolvedAsynqRedisURL(), cfg.RedisPassword)
	if err != nil {
		logrus.Fatalf("asynq redis opt: %v", err)
	}
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

func buildLLM(cfg *config.Config) interfaces.LLM {
	opts := []openai.Option{}
	if cfg.LLMModel != "" {
		opts = append(opts, openai.WithModel(cfg.LLMModel))
	}
	if cfg.LLMBaseURL != "" {
		opts = append(opts, openai.WithBaseURL(cfg.LLMBaseURL))
	}
	return openai.NewClient(cfg.LLMAPIKey, opts...)
}

func buildLightLLM(cfg *config.Config) interfaces.LLM {
	if cfg.LightLLMAPIKey == "" {
		return buildLLM(cfg)
	}
	opts := []openai.Option{}
	if cfg.LightLLMModel != "" {
		opts = append(opts, openai.WithModel(cfg.LightLLMModel))
	}
	if cfg.LightLLMBaseURL != "" {
		opts = append(opts, openai.WithBaseURL(cfg.LightLLMBaseURL))
	}
	return openai.NewClient(cfg.LightLLMAPIKey, opts...)
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

func buildSystemPrompt() string {
	return `You are Argentum, an expert data analyst helping business owners understand their metrics.

You have access to these tools:
- get_schema: Retrieve database schema information (tables, columns, relationships).
- run_sql: Execute read-only SELECT queries against the connected analytics DB.
- create_visualization: Create a Metabase card from a SQL query. Returns card_id and chart_type.
- create_dashboard: Combine multiple card_ids into a single Metabase dashboard with a shareable URL.

CRITICAL GUIDELINES:
1. LANGUAGE IS THE TOP PRIORITY: Detect the language of the user's message and reply ONLY in that exact same language. If the user writes in English, reply fully in English. If the user writes in Indonesian/Bahasa Indonesia, reply fully in Indonesian. Never mix languages and never default to Indonesian when the user wrote in English.
2. ONLY call tools when the user asks a question that requires database data or a visualization. For greetings ("hi", "hello", "test"), small-talk, or general conversation that does NOT need data, reply directly without calling any tools.
3. When you DO need to query data: call get_schema FIRST if you are unsure about table or column names. Never invent identifiers.
4. Generate valid SQL appropriate for the connected DB. The DB type (mysql or postgres) and dialect hints are prepended to every user message — respect them. Aggregations + filters as needed.
5. When the user wants charts/graphs/dashboards: call create_visualization for each card, then create_dashboard ONCE.
   - After create_visualization returns, copy the exact "dashboard_cards" array into create_dashboard's "cards" parameter.
   - Alternatively, pass just "card_ids": [123, 456] to create_dashboard.
   - When returning the dashboard URL to the user, format it as a markdown link with descriptive text, e.g. [Sales Performance Dashboard](url). Never show the raw URL.
6. NEVER return individual card IDs to the user — always wrap with a dashboard.
7. Use LIMIT 100 unless explicitly asked otherwise.`
}
