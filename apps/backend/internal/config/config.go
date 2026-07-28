// Package config loads every runtime setting from the environment, applies
// the documented defaults, and validates the combinations that cannot be
// caught later — an unknown LLM interface, a missing secret, a WhatsApp
// provider without its credentials.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

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

	// LLM
	LLMInterface string // LLM_INTERFACE: optional override (openai | anthropic | gemini); falls back to LLMProvider
	LLMProvider  string
	LLMAPIKey    string
	LLMModel     string
	LLMBaseURL   string

	LightLLMInterface string // LIGHT_LLM_INTERFACE: optional override (openai | anthropic | gemini); falls back to LightLLMProvider
	LightLLMProvider  string
	LightLLMAPIKey    string
	LightLLMModel     string
	LightLLMBaseURL   string

	// Embedding-based table picker. Opt-in per-source via the
	// db_connections.enable_table_embedding flag; this group sets the
	// provider + model + dimensions for the embeddings themselves.
	EmbeddingEnabled   bool   // master kill switch; per-source toggle still wins
	EmbeddingProvider  string // currently only "openai"
	EmbeddingAPIKey    string // falls back to LLMAPIKey when LLMInterface == openai
	EmbeddingBaseURL   string // optional Azure/proxy host
	EmbeddingModel     string // default "text-embedding-3-small"
	EmbeddingDim       int    // default 1536; MUST match the migration's vector(N)
	EmbeddingTopK      int    // top-K tables injected per source per chat turn
	EmbeddingBatchSize int    // OpenAI request fan-in (default 96, hard cap 2048)

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

	// Discord
	// Per-tenant bot tokens are stored encrypted in company_discord_credentials;
	// this flag is the global kill switch (disable to stop cmd/discord from
	// opening any sessions and to keep the API webhook returning 503).
	DiscordEnabled bool

	// Lark (Feishu)
	// Per-tenant app secrets are stored encrypted in company_lark_credentials.
	// LarkEnabled gates the API webhook + worker outbound; LarkAPIBaseURL
	// defaults to the global endpoint and only needs to change for Feishu
	// (China region) deployments.
	LarkEnabled    bool
	LarkAPIBaseURL string

	// Metabase
	MetabaseURL           string
	MetabasePublicURL     string
	MetabaseAPIKey        string
	MetabaseAdminEmail    string
	MetabaseAdminPassword string

	// Agent SDK Configuration
	AgentConfigPath      string // path to agents.yaml
	GuardrailsConfigPath string // path to guardrails.yaml

	// Argentum control plane
	JWTSecret            string
	DSNEncryptionKeyHex  string // 64 hex chars => 32 bytes for AES-256-GCM
	ControlMigrationsDir string
	CookieSecure         bool
	CORSOrigins          []string

	// Conversation threading
	ThreadIdleMinutes  int    // gap that triggers a topic-relevance check
	ClassifierModel    string // cheap model for topic classification, defaults to LLM_MODEL
	SummaryEveryNTurns int    // how often to refresh the rolling summary

	// Asynq queue + worker
	AsynqRedisURL     string // falls back to RedisURL when empty
	WorkerConcurrency int    // simultaneous tasks per worker process
	WorkerQueues      string // CSV of queue:weight pairs (e.g. "default:10")

	// Application Settings
	MaxQueriesPerMinute int
	ContextMaxTurns     int
	HistoryHydrateLimit int // max prior messages re-loaded into agent memory per turn
	CacheTTLShort       int // seconds
	CacheTTLLong        int // seconds
	MaxQueryRows        int // hard ceiling on rows returned by run_sql per query
	MaxQueryResultBytes int // hard ceiling on serialized JSON size for a run_sql result

	// Per-turn agent budget (T-16). Replaces the hard 3-iteration cap that
	// made the agent fabricate a figure rather than admit it ran out of
	// steps (finding C-1). Go is authoritative for all four: config/agents.yaml
	// no longer carries max_iterations, because two sources of truth for a
	// safety limit is how the limit ends up being the wrong one.
	AgentMaxIterations  int // tool-calling round trips per turn
	AgentMaxToolCalls   int // tool executions per turn
	AgentMaxTurnTokens  int // cumulative provider-reported tokens per turn
	AgentTurnBudgetSecs int // wall-clock ceiling per turn

	// Credit enforcement (T-03). Finding B-1: the balance was decremented and
	// never read. CreditsEnforcementEnabled is the kill switch that restores
	// that behaviour; the grant exists because nothing else in the system has
	// ever credited a company, so without it "balance <= 0" is true for every
	// tenant on the day enforcement is switched on.
	CreditsEnforcementEnabled  bool
	CreditsWarningThresholdPct int     // remaining % of the grant that triggers a warning
	CreditsDefaultGrantUSD     float64 // provisioned once per company, in dollars

	// Object storage (MinIO / S3-compatible). Used by the generate_document
	// tool to persist generated PDF/XLSX/CSV files and to issue presigned
	// download URLs.
	MinIOEndpoint          string
	MinIOAccessKeyID       string
	MinIOSecretAccessKey   string
	MinIOBucket            string
	MinIOUseSSL            bool
	DocumentPresignTTLSecs int
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
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "demo_analytics"),
		DBSSLMode:  getEnv("DB_SSL_MODE", "disable"),

		// Redis
		RedisURL:      getEnv("REDIS_URL", "localhost:6380"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),

		// LLM
		LLMInterface: getEnv("LLM_INTERFACE", "openai"),
		LLMProvider:  getEnv("LLM_PROVIDER", "custom"),
		LLMAPIKey:    getEnv("LLM_API_KEY", ""),
		LLMModel:     getEnv("LLM_MODEL", "anthropic/claude-haiku-4.5"),
		LLMBaseURL:   getEnv("LLM_BASE_URL", ""),

		LightLLMInterface: getEnv("LIGHT_LLM_INTERFACE", "openai"),
		LightLLMProvider:  getEnv("LIGHT_LLM_PROVIDER", "custom"),
		LightLLMAPIKey:    getEnv("LIGHT_LLM_API_KEY", ""),
		LightLLMModel:     getEnv("LIGHT_LLM_MODEL", "gpt-5-mini"),
		LightLLMBaseURL:   getEnv("LIGHT_LLM_BASE_URL", ""),

		// Embedding-based table picker
		EmbeddingEnabled:   getEnv("EMBEDDING_ENABLED", "true") == "true",
		EmbeddingProvider:  getEnv("EMBEDDING_PROVIDER", "openai"),
		EmbeddingAPIKey:    getEnv("EMBEDDING_API_KEY", ""),
		EmbeddingBaseURL:   getEnv("EMBEDDING_BASE_URL", ""),
		EmbeddingModel:     getEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingDim:       getEnvAsInt("EMBEDDING_DIM", 1536),
		EmbeddingTopK:      getEnvAsInt("EMBEDDING_TOPK", 8),
		EmbeddingBatchSize: getEnvAsInt("EMBEDDING_BATCH_SIZE", 96),

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
		TwilioFromNumber: getEnv("TWILIO_WHATSAPP_NUMBER", "+14155238886"),

		DiscordEnabled: getEnv("DISCORD_ENABLED", "true") == "true",

		LarkEnabled:    getEnv("LARK_ENABLED", "true") == "true",
		LarkAPIBaseURL: getEnv("LARK_API_BASE_URL", ""),

		// Metabase
		MetabaseURL:           getEnv("METABASE_URL", "http://localhost:3000"),
		MetabasePublicURL:     getEnv("METABASE_PUBLIC_URL", ""),
		MetabaseAPIKey:        getEnv("METABASE_API_KEY", ""),
		MetabaseAdminEmail:    getEnv("METABASE_ADMIN_EMAIL", "admin@argentum.local"),
		MetabaseAdminPassword: getEnv("METABASE_ADMIN_PASSWORD", ""),

		// Agent SDK Configuration
		AgentConfigPath:      getEnv("AGENT_CONFIG_PATH", "config/agents.yaml"),
		GuardrailsConfigPath: getEnv("GUARDRAILS_CONFIG_PATH", "config/guardrails.yaml"),

		// Control plane
		JWTSecret:            getEnv("ARGENTUM_JWT_SECRET", ""),
		DSNEncryptionKeyHex:  getEnv("ARGENTUM_DSN_KEY", ""),
		ControlMigrationsDir: getEnv("CONTROL_MIGRATIONS_DIR", "migrations/control"),
		CookieSecure:         getEnv("COOKIE_SECURE", "false") == "true",
		CORSOrigins:          splitCSV(getEnv("CORS_ORIGINS", "http://localhost:5173")),

		// Conversation threading
		ThreadIdleMinutes:  getEnvAsInt("THREAD_IDLE_MINUTES", 30),
		ClassifierModel:    getEnv("LLM_CLASSIFIER_MODEL", "gpt-5-nano"),
		SummaryEveryNTurns: getEnvAsInt("SUMMARY_EVERY_N_TURNS", 8),

		// Asynq
		AsynqRedisURL:     getEnv("ASYNQ_REDIS_URL", ""),
		WorkerConcurrency: getEnvAsInt("WORKER_CONCURRENCY", 10),
		WorkerQueues:      getEnv("WORKER_QUEUES", "default:10"),

		// Application Settings
		MaxQueriesPerMinute: getEnvAsInt("MAX_QUERIES_PER_MINUTE", 10),
		ContextMaxTurns:     getEnvAsInt("CONTEXT_MAX_TURNS", 3),
		HistoryHydrateLimit: getEnvAsInt("HISTORY_HYDRATE_LIMIT", 20),
		CacheTTLShort:       getEnvAsInt("CACHE_TTL_SHORT", 300),
		CacheTTLLong:        getEnvAsInt("CACHE_TTL_LONG", 86400),
		MaxQueryRows:        getEnvAsInt("MAX_QUERY_ROWS", 100),
		MaxQueryResultBytes: getEnvAsInt("MAX_QUERY_RESULT_BYTES", 200000),

		// Per-turn agent budget
		AgentMaxIterations:  getEnvAsInt("AGENT_MAX_ITERATIONS", 8),
		AgentMaxToolCalls:   getEnvAsInt("AGENT_MAX_TOOL_CALLS", 12),
		AgentMaxTurnTokens:  getEnvAsInt("AGENT_MAX_TURN_TOKENS", 200000),
		AgentTurnBudgetSecs: getEnvAsInt("AGENT_TURN_BUDGET_SECS", 150),

		// Credit enforcement
		CreditsEnforcementEnabled:  getEnv("CREDITS_ENFORCEMENT_ENABLED", "true") == "true",
		CreditsWarningThresholdPct: getEnvAsInt("CREDITS_WARNING_THRESHOLD_PCT", 20),
		CreditsDefaultGrantUSD:     getEnvAsFloat("CREDITS_DEFAULT_GRANT_USD", 25),

		// Object storage (MinIO / S3-compatible)
		MinIOEndpoint:          getEnv("MINIO_ENDPOINT", ""),
		MinIOAccessKeyID:       getEnv("MINIO_ACCESS_KEY", ""),
		MinIOSecretAccessKey:   getEnv("MINIO_SECRET_KEY", ""),
		MinIOBucket:            getEnv("MINIO_BUCKET", "argentum-documents"),
		MinIOUseSSL:            getEnv("MINIO_USE_SSL", "false") == "true",
		DocumentPresignTTLSecs: getEnvAsInt("DOCUMENT_PRESIGN_TTL_SECS", 3600),
	}

	// Validate required configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Supported LLM wire protocols for agent-sdk-go clients.
const (
	LLMInterfaceOpenAI    = "openai"
	LLMInterfaceAnthropic = "anthropic"
	LLMInterfaceGemini    = "gemini"
)

// EffectiveLLMInterface returns LLM_INTERFACE when set, otherwise LLM_PROVIDER (trimmed, lowercased).
func (c *Config) EffectiveLLMInterface() string {
	if s := strings.TrimSpace(strings.ToLower(c.LLMInterface)); s != "" {
		return s
	}
	return strings.TrimSpace(strings.ToLower(c.LLMProvider))
}

// EffectiveLightLLMInterface returns LIGHT_LLM_INTERFACE when set, otherwise LIGHT_LLM_PROVIDER.
func (c *Config) EffectiveLightLLMInterface() string {
	if s := strings.TrimSpace(strings.ToLower(c.LightLLMInterface)); s != "" {
		return s
	}
	return strings.TrimSpace(strings.ToLower(c.LightLLMProvider))
}

// EffectiveLightLLMModel returns LIGHT_LLM_MODEL when set, otherwise falls
// back to LLM_MODEL. The metering layer needs a real model string for
// pricing lookup, never empty.
func (c *Config) EffectiveLightLLMModel() string {
	if m := strings.TrimSpace(c.LightLLMModel); m != "" {
		return m
	}
	return strings.TrimSpace(c.LLMModel)
}

// EffectiveClassifierModel returns LLM_CLASSIFIER_MODEL when set, otherwise
// falls back to LLM_MODEL.
func (c *Config) EffectiveClassifierModel() string {
	if m := strings.TrimSpace(c.ClassifierModel); m != "" {
		return m
	}
	return strings.TrimSpace(c.LLMModel)
}

// EffectiveEmbeddingAPIKey returns EMBEDDING_API_KEY when set, otherwise
// LLMAPIKey when the primary LLM is OpenAI (same credentials). Returns ""
// when no usable key is configured — caller should treat that as "disable".
func (c *Config) EffectiveEmbeddingAPIKey() string {
	if k := strings.TrimSpace(c.EmbeddingAPIKey); k != "" {
		return k
	}
	if c.EffectiveLLMInterface() == LLMInterfaceOpenAI {
		return strings.TrimSpace(c.LLMAPIKey)
	}
	return ""
}

// CreditsDefaultGrantMicroUSD converts the operator-facing dollar amount to
// the micro-USD unit every balance in the control plane is stored in. It is
// the only place that conversion happens, because a factor of a million
// applied twice is a grant nobody notices is wrong until it runs out.
// Negative input floors at zero rather than erroring — a grant is a ceiling
// on generosity, not a debt.
func (c *Config) CreditsDefaultGrantMicroUSD() int64 {
	if c.CreditsDefaultGrantUSD <= 0 {
		return 0
	}
	return int64(c.CreditsDefaultGrantUSD * 1_000_000)
}

// Validate checks if required configuration is present
func (c *Config) Validate() error {
	if err := validateLLMInterfaceKind(c.EffectiveLLMInterface(), "LLM_INTERFACE", "LLM_PROVIDER"); err != nil {
		return err
	}
	if err := validateLLMInterfaceKind(c.EffectiveLightLLMInterface(), "LIGHT_LLM_INTERFACE", "LIGHT_LLM_PROVIDER"); err != nil {
		return err
	}
	if c.LLMAPIKey == "" {
		return fmt.Errorf("LLM_API_KEY is required")
	}
	if c.JWTSecret == "" {
		return fmt.Errorf("ARGENTUM_JWT_SECRET is required")
	}
	if c.DSNEncryptionKeyHex == "" {
		return fmt.Errorf("ARGENTUM_DSN_KEY is required (64 hex chars)")
	}
	if c.DBPassword == "" {
		return fmt.Errorf("DB_PASSWORD is required")
	}

	// WhatsApp credentials are only required when the corresponding provider
	// is selected. Twilio uses a different set of env vars.
	switch c.WhatsAppProvider {
	case "whatsapp_business", "":
		if c.WhatsAppAccessToken == "" {
			return fmt.Errorf("WHATSAPP_ACCESS_TOKEN is required for whatsapp_business provider")
		}
		if c.WhatsAppPhoneNumberID == "" {
			return fmt.Errorf("WHATSAPP_PHONE_NUMBER_ID is required for whatsapp_business provider")
		}
	case "twilio":
		if c.TwilioAccountSID == "" || c.TwilioAuthToken == "" || c.TwilioFromNumber == "" {
			return fmt.Errorf("TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_WHATSAPP_NUMBER are required for twilio provider")
		}
	}

	return nil
}

func validateLLMInterfaceKind(iface, envPrimary, envFallback string) error {
	switch iface {
	case LLMInterfaceOpenAI, LLMInterfaceAnthropic, LLMInterfaceGemini:
		return nil
	default:
		return fmt.Errorf("%s / %s must be %q, %q, or %q (got %q)",
			envPrimary, envFallback,
			LLMInterfaceOpenAI, LLMInterfaceAnthropic, LLMInterfaceGemini, iface)
	}
}

// DatabaseURL returns a postgres:// URI suitable for lib/pq, pgx, and
// golang-migrate (which parses the URL with net/url — a libpq keyword
// string must not be prefixed with postgres://).
func (c *Config) DatabaseURL() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   fmt.Sprintf("%s:%d", c.DBHost, c.DBPort),
		Path:   "/" + c.DBName,
	}
	q := url.Values{}
	q.Set("sslmode", c.DBSSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// IsDevelopment returns true if running in development mode
func (c *Config) IsDevelopment() bool {
	return c.Env == "development"
}

// IsProduction returns true if running in production mode
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

// ResolvedAsynqRedisURL returns AsynqRedisURL when set, otherwise RedisURL.
// asynq accepts a "redis://..." URI or a bare "host:port"; either is fine
// for asynq.RedisClientOpt parsing helpers downstream.
func (c *Config) ResolvedAsynqRedisURL() string {
	if c.AsynqRedisURL != "" {
		return c.AsynqRedisURL
	}
	return c.RedisURL
}

// RedisDialAddr returns host:port for redis.Client options that expect Addr,
// not a redis:// URI (e.g. agent-sdk-go memory uses go-redis that way).
func (c *Config) RedisDialAddr() string {
	return redisDialAddr(c.RedisURL)
}

func redisDialAddr(u string) string {
	if u == "" || !strings.Contains(u, "://") {
		return u
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return u
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = "6379"
	}
	return net.JoinHostPort(host, port)
}

// WorkerQueueMap parses the WORKER_QUEUES csv ("default:10,low:1") into
// the map asynq.Config expects. Falls back to {"default": 10} on error.
func (c *Config) WorkerQueueMap() map[string]int {
	out := map[string]int{}
	for _, part := range splitCSV(c.WorkerQueues) {
		name := part
		weight := 1
		for i := 0; i < len(part); i++ {
			if part[i] == ':' {
				name = trimSpace(part[:i])
				if v, err := strconv.Atoi(trimSpace(part[i+1:])); err == nil && v > 0 {
					weight = v
				}
				break
			}
		}
		if name != "" {
			out[name] = weight
		}
	}
	if len(out) == 0 {
		out["default"] = 10
	}
	return out
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

func getEnvAsFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			seg := s[start:i]
			seg = trimSpace(seg)
			if seg != "" {
				parts = append(parts, seg)
			}
			start = i + 1
		}
	}
	return parts
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
