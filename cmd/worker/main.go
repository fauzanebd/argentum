package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fauzanebd/argentum/internal/agent"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/database"
	"github.com/fauzanebd/argentum/internal/llm"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/metadata"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/whatsapp"
	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/sirupsen/logrus"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}

	// Set up logging
	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.JSONFormatter{})

	logrus.Info("Starting Analytics Agent Worker (Phase 2)")

	// Initialize database connection
	db, err := database.NewDB(cfg.DatabaseURL())
	if err != nil {
		logrus.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize metadata manager
	schemaManager := metadata.NewSchemaManager(db.DB)

	// Initialize LLM provider
	llmFactory := llm.NewFactory(cfg.LLMProvider, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMBaseURL)
	llmProvider, err := llmFactory.Create()
	if err != nil {
		logrus.Fatalf("Failed to create LLM provider: %v", err)
	}

	// Initialize Metabase client
	metabaseClient := metabase.NewClient(
		cfg.MetabaseURL,
		cfg.MetabaseAdminEmail,
		cfg.MetabaseAdminPassword,
	)

	// Initialize tool registry
	toolRegistry := tools.NewRegistry(schemaManager, db, metabaseClient)

	// Initialize agent with Tool Registry
	analyticsAgent := agent.NewAgent(llmProvider, db, toolRegistry, schemaManager, metabaseClient)

	// Initialize WhatsApp provider for sending responses
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

	// Initialize RabbitMQ client
	queueClient, err := queue.NewClient(cfg.RabbitMQURL)
	if err != nil {
		logrus.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer queueClient.Close()

	logrus.Info("Worker ready, starting to consume messages...")

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start consuming messages in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- queueClient.Consume(func(msg *models.QueueMessage) error {
			return processMessage(ctx, analyticsAgent, whatsappProvider, msg)
		})
	}()

	// Wait for shutdown signal or error
	select {
	case sig := <-sigChan:
		logrus.Infof("Received signal %s, shutting down...", sig)
		cancel()
	case err := <-errChan:
		if err != nil {
			logrus.Errorf("Consumer error: %v", err)
		}
	}

	// Give some time for ongoing processing to complete
	time.Sleep(2 * time.Second)
	logrus.Info("Worker exited")
}

// processMessage processes a single message from the queue
func processMessage(ctx context.Context, analyticsAgent *agent.Agent, waClient whatsapp.Provider, msg *models.QueueMessage) error {
	logrus.Infof("Processing message %s from %s", msg.MessageID, msg.PhoneNumber)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Process the query through the agent (with Tool Registry)
	response, err := analyticsAgent.ProcessQuery(ctx, msg.PhoneNumber, msg.Body)
	if err != nil {
		logrus.Errorf("Failed to process query: %v", err)

		// Send error message to user
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

	// Send response back to user
	if err := waClient.SendResponse(msg.PhoneNumber, response); err != nil {
		logrus.Errorf("Failed to send response: %v", err)
		return err
	}

	logrus.Infof("Successfully processed message %s", msg.MessageID)
	return nil
}
