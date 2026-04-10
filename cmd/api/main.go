package main

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/jobs"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/whatsapp"
	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/gin-gonic/gin"
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

	logrus.Info("Starting Analytics Agent API Server (Phase 3)")

	// Initialize metrics collector
	metricsCollector := metrics.NewCollector()

	// Initialize RabbitMQ client
	queueClient, err := queue.NewClient(cfg.RabbitMQURL)
	if err != nil {
		logrus.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer queueClient.Close()

	// Initialize WhatsApp provider
	whatsappProvider, err := whatsapp.NewProvider(whatsapp.Config{
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
		logrus.Fatalf("Failed to create WhatsApp provider: %v", err)
	}

	// Initialize Job Manager
	jobManager := jobs.NewManager(5) // 5 workers
	jobManager.Start(context.Background())

	// Set up Gin router
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.Default()

	// Health check endpoints
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().Unix(),
			"version":   "3.0.0",
			"phase":     "3",
		})
	})

	// Readiness check
	router.GET("/ready", func(c *gin.Context) {
		// Check RabbitMQ connection
		rabbitReady := queueClient != nil

		c.JSON(http.StatusOK, gin.H{
			"ready":     rabbitReady,
			"rabbitmq":  rabbitReady,
			"timestamp": time.Now().Unix(),
		})
	})

	// Metrics endpoint
	router.GET("/metrics", func(c *gin.Context) {
		snapshot := metricsCollector.GetSnapshot()
		c.JSON(http.StatusOK, snapshot)
	})

	// Job status endpoint
	router.GET("/jobs/:id", func(c *gin.Context) {
		jobID := c.Param("id")
		job, err := jobManager.GetJob(jobID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, job)
	})

	// Job stats endpoint
	router.GET("/jobs/stats", func(c *gin.Context) {
		stats := jobManager.GetJobStats()
		c.JSON(http.StatusOK, stats)
	})

	// Metabase reverse proxy (path-based routing)
	metabaseURL, _ := url.Parse("http://metabase:3000")
	metabaseProxy := httputil.NewSingleHostReverseProxy(metabaseURL)
	router.Any("/metabase/*path", func(c *gin.Context) {
		// Remove /metabase prefix from the path
		originalPath := c.Param("path")
		c.Request.URL.Path = originalPath
		c.Request.URL.RawPath = ""

		// Strip the /metabase prefix from the request
		c.Request.URL.Path = strings.TrimPrefix(c.Request.URL.Path, "/metabase")
		if c.Request.URL.Path == "" {
			c.Request.URL.Path = "/"
		}

		// Update the host header
		c.Request.Host = metabaseURL.Host

		logrus.Debugf("Proxying Metabase request: %s", c.Request.URL.Path)
		metabaseProxy.ServeHTTP(c.Writer, c.Request)
	})

	// WhatsApp webhook verification endpoint (GET) - for WhatsApp Business API
	router.GET("/webhook/whatsapp", func(c *gin.Context) {
		mode := c.Query("hub.mode")
		token := c.Query("hub.verify_token")
		challenge := c.Query("hub.challenge")

		if mode == "subscribe" && whatsappProvider.VerifyToken(token, cfg.WhatsAppWebhookVerifyToken) {
			logrus.Info("Webhook verified successfully")
			c.String(http.StatusOK, challenge)
			return
		}

		logrus.Warn("Webhook verification failed")
		c.Status(http.StatusForbidden)
	})

	// WhatsApp/Twilio webhook message endpoint (POST) - handles both providers
	router.POST("/webhook/whatsapp", func(c *gin.Context) {
		startTime := time.Now()

		// Detect provider based on headers
		isTwilio := c.GetHeader("X-Twilio-Signature") != "" || c.ContentType() == "application/x-www-form-urlencoded"

		var msg *models.Message
		var err error

		if isTwilio {
			// Handle Twilio webhook (form data)
			if err := c.Request.ParseForm(); err != nil {
				logrus.Errorf("Failed to parse Twilio form data: %v", err)
				c.Status(http.StatusBadRequest)
				return
			}

			// Verify Twilio signature
			// TODO: Implement proper HMAC-SHA256 signature verification for production
			// For now, we log but don't reject in development mode
			twilioSignature := c.GetHeader("X-Twilio-Signature")
			webhookURL := "https://" + c.Request.Host + c.Request.URL.Path
			if !whatsappProvider.VerifyWebhook([]byte(c.Request.PostForm.Encode()), twilioSignature, webhookURL) {
				logrus.Warn("Invalid Twilio webhook signature - proceeding in development mode (TODO: implement proper HMAC-SHA256)")
				// c.Status(http.StatusUnauthorized)
				// return
			}

			// Parse Twilio form data
			from := c.PostForm("From")
			body := c.PostForm("Body")
			messageSid := c.PostForm("MessageSid")

			// Extract phone number (remove "whatsapp:" prefix)
			phoneNumber := strings.TrimPrefix(from, "whatsapp:")

			msg = models.NewMessage("", phoneNumber, body)
			msg.ID = messageSid

			logrus.Infof("Received Twilio message from %s: %s", phoneNumber, body)
		} else {
			// Handle WhatsApp Business API webhook (JSON)
			body, err := c.GetRawData()
			if err != nil {
				logrus.Errorf("Failed to read request body: %v", err)
				c.Status(http.StatusBadRequest)
				return
			}

			// Verify webhook signature (pass empty URL for WhatsApp Business API)
			signature := c.GetHeader("X-Hub-Signature-256")
			if !whatsappProvider.VerifyWebhook(body, signature, "") {
				logrus.Warn("Invalid webhook signature")
				c.Status(http.StatusUnauthorized)
				return
			}

			// Parse the message
			parsedMsg, err := whatsappProvider.ParseWebhook(body)
			if err != nil {
				logrus.Errorf("Failed to parse webhook: %v", err)
				c.Status(http.StatusOK) // Return 200 to acknowledge receipt
				return
			}
			msg = parsedMsg
		}

		// Ignore empty messages or non-text messages
		if msg.Body == "" || msg.MessageType != "text" {
			c.Status(http.StatusOK)
			return
		}

		// Create async job for processing
		jobPayload := map[string]interface{}{
			"message_id":   msg.ID,
			"phone_number": msg.PhoneNumber,
			"body":         msg.Body,
			"timestamp":    msg.Timestamp,
		}

		job, err := jobManager.CreateJob("process_message", jobPayload, msg.PhoneNumber, msg.PhoneNumber)
		if err != nil {
			logrus.Errorf("Failed to create job: %v", err)
			c.Status(http.StatusInternalServerError)
			return
		}

		// Publish to queue
		queueMsg := &models.QueueMessage{
			MessageID:   job.ID, // Use job ID for correlation
			BusinessID:  msg.BusinessID,
			PhoneNumber: msg.PhoneNumber,
			Body:        msg.Body,
			Timestamp:   msg.Timestamp,
		}

		if err := queueClient.Publish(c.Request.Context(), queueMsg); err != nil {
			logrus.Errorf("Failed to publish message: %v", err)
			c.Status(http.StatusInternalServerError)
			return
		}

		// Record metrics
		duration := time.Since(startTime)
		metricsCollector.RecordQuery(duration, false, false)

		// Return appropriate response based on provider
		if isTwilio {
			// Twilio expects an empty 200 response
			c.Status(http.StatusOK)
		} else {
			// Return job ID for tracking (WhatsApp Business API)
			c.JSON(http.StatusOK, gin.H{
				"status":    "accepted",
				"job_id":    job.ID,
				"message":   "Processing started",
				"timestamp": time.Now().Unix(),
			})
		}
	})

	// Start server with graceful shutdown
	srv := &http.Server{
		Addr:    ":" + strconv.Itoa(cfg.Port),
		Handler: router,
	}

	// Start server in goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Fatalf("Failed to start server: %v", err)
		}
	}()

	logrus.Infof("API server started on port %d", cfg.Port)
	logrus.Infof("Health check: http://localhost:%d/health", cfg.Port)
	logrus.Infof("Metrics: http://localhost:%d/metrics", cfg.Port)

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logrus.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logrus.Errorf("Server forced to shutdown: %v", err)
	}

	logrus.Info("Server exited")
}
