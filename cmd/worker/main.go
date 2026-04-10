package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/llm/openai"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/Ingenimax/agent-sdk-go/pkg/multitenancy"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/database"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/metadata"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/whatsapp"
	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/sirupsen/logrus"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}

	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.JSONFormatter{})

	logrus.Info("Starting Analytics Agent Worker (agent-sdk-go)")

	// Database
	db, err := database.NewDB(cfg.DatabaseURL())
	if err != nil {
		logrus.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Metadata / schema manager
	schemaManager := metadata.NewSchemaManager(db.DB)

	// Metabase client
	metabaseClient := metabase.NewClient(
		cfg.MetabaseURL,
		cfg.MetabasePublicURL,
		cfg.MetabaseAdminEmail,
		cfg.MetabaseAdminPassword,
	)

	// --- Build agent-sdk-go agent ---

	// LLM provider
	llmClient := buildLLMClient(cfg)

	// Custom tools
	agentTools := []interfaces.Tool{
		tools.NewGetSchemaTool(schemaManager),
		tools.NewRunSQLTool(db),
		tools.NewCreateVisualizationTool(db, metabaseClient),
		tools.NewCreateDashboardTool(metabaseClient),
	}

	// Memory (Redis-backed for distributed workers, fallback to buffer)
	mem := buildMemory(cfg)

	// Guardrails
	var gr interfaces.Guardrails
	if cfg.GuardrailsConfigPath != "" {
		loaded, err := guardrails.LoadFromFile(cfg.GuardrailsConfigPath)
		if err != nil {
			logrus.Warnf("Failed to load guardrails config, continuing without guardrails: %v", err)
		} else {
			gr = loaded
			logrus.Info("Guardrails loaded from config")
		}
	}

	// System prompt: inject live schema context
	systemPrompt := buildSystemPrompt(schemaManager)

	// Agent configuration via YAML (if available) or direct options
	agentOpts := []sdkagent.Option{
		sdkagent.WithLLM(llmClient),
		sdkagent.WithTools(agentTools...),
		sdkagent.WithMemory(mem),
		sdkagent.WithSystemPrompt(systemPrompt),
		sdkagent.WithName("AnalyticsAgent"),
		sdkagent.WithDescription("Conversational analytics agent for business owners"),
		sdkagent.WithMaxIterations(5),
		sdkagent.WithRequirePlanApproval(false),
		sdkagent.WithLLMConfig(interfaces.LLMConfig{
			Temperature: 0.2,
		}),
	}

	if gr != nil {
		agentOpts = append(agentOpts, sdkagent.WithGuardrails(gr))
	}

	// Load YAML agent config if available (merges with programmatic options)
	if cfg.AgentConfigPath != "" {
		configs, err := sdkagent.LoadAgentConfigsFromFile(cfg.AgentConfigPath)
		if err != nil {
			logrus.Warnf("Failed to load agent YAML config, using defaults: %v", err)
		} else if agentCfg, ok := configs["analytics_agent"]; ok {
			agentOpts = append(agentOpts, sdkagent.WithAgentConfig(agentCfg, nil))
			logrus.Info("Agent YAML config loaded")
		}
	}

	analyticsAgent, err := sdkagent.NewAgent(agentOpts...)
	if err != nil {
		logrus.Fatalf("Failed to create agent: %v", err)
	}

	// WhatsApp provider
	whatsappProvider, err := whatsapp.NewProvider(whatsapp.Config{
		Provider:         cfg.WhatsAppProvider,
		APIVersion:       cfg.WhatsAppAPIVersion,
		PhoneNumberID:    cfg.WhatsAppPhoneNumberID,
		AccessToken:      cfg.WhatsAppAccessToken,
		AppSecret:        cfg.WhatsAppAppSecret,
		TwilioAccountSID: cfg.TwilioAccountSID,
		TwilioAuthToken:  cfg.TwilioAuthToken,
		TwilioFromNumber: cfg.TwilioFromNumber,
	})
	if err != nil {
		logrus.Fatalf("Failed to create WhatsApp provider: %v", err)
	}

	// RabbitMQ
	queueClient, err := queue.NewClient(cfg.RabbitMQURL)
	if err != nil {
		logrus.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer queueClient.Close()

	logrus.Info("Worker ready, starting to consume messages...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		errChan <- queueClient.Consume(func(msg *models.QueueMessage) error {
			return processMessage(ctx, analyticsAgent, whatsappProvider, msg)
		})
	}()

	select {
	case sig := <-sigChan:
		logrus.Infof("Received signal %s, shutting down...", sig)
		cancel()
	case err := <-errChan:
		if err != nil {
			logrus.Errorf("Consumer error: %v", err)
		}
	}

	time.Sleep(2 * time.Second)
	logrus.Info("Worker exited")
}

func processMessage(ctx context.Context, agent *sdkagent.Agent, waClient whatsapp.Provider, msg *models.QueueMessage) error {
	logrus.Infof("Processing message %s from %s", msg.MessageID, msg.PhoneNumber)

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()

	// Set multi-tenancy and conversation context
	ctx = multitenancy.WithOrgID(ctx, msg.BusinessID)
	ctx = memory.WithConversationID(ctx, msg.PhoneNumber)

	response, err := agent.Run(ctx, msg.Body)
	if err != nil {
		// Guardrails rejections carry a user-friendly message prefixed by the
		// SDK with "guardrails error: ".  Forward that message directly to the
		// user and treat the exchange as successfully handled (return nil so the
		// message is not re-queued).
		const guardrailsPrefix = "guardrails error: "
		if strings.HasPrefix(err.Error(), guardrailsPrefix) {
			userMsg := strings.TrimPrefix(err.Error(), guardrailsPrefix)
			logrus.Infof("Guardrails rejected message %s: %s", msg.MessageID, userMsg)
			guardrailsResponse := &models.AgentResponse{
				MessageID: msg.MessageID,
				Query:     msg.Body,
				Insight:   userMsg,
			}
			if sendErr := waClient.SendResponse(msg.PhoneNumber, guardrailsResponse); sendErr != nil {
				logrus.Errorf("Failed to send guardrails response: %v", sendErr)
				return sendErr
			}
			return nil
		}

		logrus.Errorf("Agent run failed: %v", err)
		errorResponse := &models.AgentResponse{
			MessageID: msg.MessageID,
			Query:     msg.Body,
			Insight:   "I apologize, but I encountered an error processing your request. Please try rephrasing your question or try again later.",
			Error:     err.Error(),
		}
		if sendErr := waClient.SendResponse(msg.PhoneNumber, errorResponse); sendErr != nil {
			logrus.Errorf("Failed to send error response: %v", sendErr)
		}
		return err
	}

	// Parse dashboard URL from response if present
	dashboardURL := extractDashboardURL(response)

	agentResponse := &models.AgentResponse{
		MessageID:    msg.MessageID,
		Query:        msg.Body,
		Insight:      response,
		DashboardURL: dashboardURL,
	}

	if err := waClient.SendResponse(msg.PhoneNumber, agentResponse); err != nil {
		logrus.Errorf("Failed to send response: %v", err)
		return err
	}

	logrus.Infof("Successfully processed message %s", msg.MessageID)
	return nil
}

func buildLLMClient(cfg *config.Config) interfaces.LLM {
	opts := []openai.Option{}

	if cfg.LLMModel != "" {
		opts = append(opts, openai.WithModel(cfg.LLMModel))
	}
	if cfg.LLMBaseURL != "" {
		opts = append(opts, openai.WithBaseURL(cfg.LLMBaseURL))
	}

	return openai.NewClient(cfg.LLMAPIKey, opts...)
}

func buildMemory(cfg *config.Config) interfaces.Memory {
	if cfg.RedisURL != "" {
		mem, err := memory.NewRedisMemoryFromConfig(memory.RedisConfig{
			URL:      cfg.RedisURL,
			Password: cfg.RedisPassword,
			DB:       0,
		})
		if err != nil {
			logrus.Warnf("Failed to create Redis memory, falling back to buffer: %v", err)
			return memory.NewConversationBuffer(memory.WithMaxSize(20))
		}
		logrus.Info("Using Redis-backed conversation memory")
		return mem
	}
	logrus.Info("Using in-memory conversation buffer")
	return memory.NewConversationBuffer(memory.WithMaxSize(20))
}

func buildSystemPrompt(schemaManager *metadata.SchemaManager) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schemaInfo := ""
	schema, err := schemaManager.GetSchema(ctx, "default", false)
	if err != nil {
		logrus.Warnf("Failed to load schema for system prompt: %v", err)
		schemaInfo = "\nDatabase Schema: Unable to retrieve schema at startup. Use the get_schema tool.\n"
	} else {
		schemaInfo = fmt.Sprintf("\n%s\n", schemaManager.ToPromptFormat(schema))
	}

	return fmt.Sprintf(`You are an expert data analyst helping business owners understand their metrics.

You have access to the following tools:
- get_schema: Retrieve database schema information (tables, columns, relationships)
- run_sql: Execute read-only PostgreSQL SELECT queries
- create_visualization: Create a Metabase card (question) from a SQL query. Returns card_id and chart_type.
- create_dashboard: Combine multiple cards into a single Metabase dashboard with a shareable public URL.

%s

CRITICAL GUIDELINES:
1. Use ONLY the table and column names from the schema above.
2. NEVER hallucinate or invent table names that don't exist.
3. Always use get_schema if you need to verify table/column names.
4. Generate valid PostgreSQL SQL with appropriate aggregations and filters.
5. When the user asks for charts, graphs, or dashboards: first call create_visualization for each card, then call create_dashboard ONCE with all the card_ids to produce a single shareable link.
6. ALWAYS use create_dashboard after creating cards — never return individual card IDs to the user.
7. Be friendly, concise, and include specific numbers in your answers.
8. When uncertain about date ranges, use the get_schema tool first.
9. Limit results to reasonable sizes (LIMIT 100) unless explicitly asked for all data.`, schemaInfo)
}

func extractDashboardURL(response string) string {
	if idx := strings.Index(response, "http"); idx >= 0 {
		end := strings.IndexAny(response[idx:], " \n\t\"')")
		if end == -1 {
			return response[idx:]
		}
		url := response[idx : idx+end]
		if strings.Contains(url, "dashboard") || strings.Contains(url, "metabase") {
			return url
		}
	}
	return ""
}
