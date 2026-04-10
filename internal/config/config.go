package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

// Config holds all application configuration
type Config struct {
	// Environment
	Env      string
	LogLevel string
	Port     int

	// Database
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// Redis
	RedisURL      string
	RedisPassword string

	// RabbitMQ
	RabbitMQURL string

	// LLM
	LLMProvider string
	LLMAPIKey   string
	LLMModel    string
	LLMBaseURL  string

	// WhatsApp Provider
	WhatsAppProvider string // "whatsapp_business" or "twilio"

	// WhatsApp Business API
	WhatsAppAPIVersion         string
	WhatsAppPhoneNumberID      string
	WhatsAppBusinessAcctID     string
	WhatsAppAccessToken        string
	WhatsAppAppSecret          string
	WhatsAppWebhookVerifyToken string

	// Twilio WhatsApp
	TwilioAccountSID string
	TwilioAuthToken  string
	TwilioFromNumber string

	// Metabase
	MetabaseURL           string
	MetabasePublicURL     string
	MetabaseAPIKey        string
	MetabaseAdminEmail    string
	MetabaseAdminPassword string

	// Agent SDK Configuration
	AgentConfigPath      string // path to agents.yaml
	GuardrailsConfigPath string // path to guardrails.yaml

	// Application Settings
	MaxQueriesPerMinute int
	ContextMaxTurns     int
	CacheTTLShort       int // seconds
	CacheTTLLong        int // seconds
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		logrus.Info("No .env file found, using environment variables")
	}

	cfg := &Config{
		// Environment
		Env:      getEnv("ENV", "development"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
		Port:     getEnvAsInt("PORT", 8080),

		// Database
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnvAsInt("DB_PORT", 5432),
		DBUser:     getEnv("DB_USER", "analytics"),
		DBPassword: getEnv("DB_PASSWORD", "analytics123"),
		DBName:     getEnv("DB_NAME", "analytics_db"),
		DBSSLMode:  getEnv("DB_SSL_MODE", "disable"),

		// Redis
		RedisURL:      getEnv("REDIS_URL", "localhost:6380"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),

		// RabbitMQ
		RabbitMQURL: getEnv("RABBITMQ_URL", "amqp://admin:admin123@localhost:5672/"),

		// LLM
		LLMProvider: getEnv("LLM_PROVIDER", "openai"),
		LLMAPIKey:   getEnv("LLM_API_KEY", ""),
		LLMModel:    getEnv("LLM_MODEL", "gpt-4o-mini"),
		LLMBaseURL:  getEnv("LLM_BASE_URL", ""),

		// WhatsApp Provider
		WhatsAppProvider: getEnv("WHATSAPP_PROVIDER", "whatsapp_business"),

		// WhatsApp Business API
		WhatsAppAPIVersion:         getEnv("WHATSAPP_API_VERSION", "v18.0"),
		WhatsAppPhoneNumberID:      getEnv("WHATSAPP_PHONE_NUMBER_ID", ""),
		WhatsAppBusinessAcctID:     getEnv("WHATSAPP_BUSINESS_ACCOUNT_ID", ""),
		WhatsAppAccessToken:        getEnv("WHATSAPP_ACCESS_TOKEN", ""),
		WhatsAppAppSecret:          getEnv("WHATSAPP_APP_SECRET", ""),
		WhatsAppWebhookVerifyToken: getEnv("WHATSAPP_WEBHOOK_VERIFY_TOKEN", ""),

		// Twilio WhatsApp
		TwilioAccountSID: getEnv("TWILIO_ACCOUNT_SID", ""),
		TwilioAuthToken:  getEnv("TWILIO_AUTH_TOKEN", ""),
		TwilioFromNumber: getEnv("TWILIO_WHATSAPP_NUMBER", ""),

		// Metabase
		MetabaseURL:           getEnv("METABASE_URL", "http://localhost:3000"),
		MetabasePublicURL:     getEnv("METABASE_PUBLIC_URL", ""),
		MetabaseAPIKey:        getEnv("METABASE_API_KEY", ""),
		MetabaseAdminEmail:    getEnv("METABASE_ADMIN_EMAIL", ""),
		MetabaseAdminPassword: getEnv("METABASE_ADMIN_PASSWORD", ""),

		// Agent SDK Configuration
		AgentConfigPath:      getEnv("AGENT_CONFIG_PATH", "config/agents.yaml"),
		GuardrailsConfigPath: getEnv("GUARDRAILS_CONFIG_PATH", "config/guardrails.yaml"),

		// Application Settings
		MaxQueriesPerMinute: getEnvAsInt("MAX_QUERIES_PER_MINUTE", 10),
		ContextMaxTurns:     getEnvAsInt("CONTEXT_MAX_TURNS", 3),
		CacheTTLShort:       getEnvAsInt("CACHE_TTL_SHORT", 300),
		CacheTTLLong:        getEnvAsInt("CACHE_TTL_LONG", 86400),
	}

	// Validate required configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks if required configuration is present
func (c *Config) Validate() error {
	if c.LLMAPIKey == "" {
		return fmt.Errorf("LLM_API_KEY is required")
	}

	if c.WhatsAppAccessToken == "" {
		return fmt.Errorf("WHATSAPP_ACCESS_TOKEN is required")
	}

	if c.WhatsAppPhoneNumberID == "" {
		return fmt.Errorf("WHATSAPP_PHONE_NUMBER_ID is required")
	}

	return nil
}

// DatabaseURL returns the PostgreSQL connection string
func (c *Config) DatabaseURL() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode)
}

// IsDevelopment returns true if running in development mode
func (c *Config) IsDevelopment() bool {
	return c.Env == "development"
}

// IsProduction returns true if running in production mode
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
