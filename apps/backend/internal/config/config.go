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
	"time"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/video"
	"github.com/fauzanebd/argentum/internal/report/videoplan"
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

	// Slack
	// Per-tenant bot tokens are stored encrypted in company_slack_credentials.
	// SlackEnabled gates the API webhook + worker outbound; SlackAPIBaseURL
	// defaults to the public Web API root and only needs to change when
	// pointing at a proxy or a test double.
	SlackEnabled    bool
	SlackAPIBaseURL string

	// Metabase
	MetabaseURL           string
	MetabasePublicURL     string
	MetabaseAPIKey        string
	MetabaseAdminEmail    string
	MetabaseAdminPassword string

	// Agent SDK Configuration
	AgentConfigPath      string // path to agents.yaml
	GuardrailsConfigPath string // path to guardrails.yaml
	// AgentTemplatesPath is the create-an-agent gallery (T-B3). Unlike the two
	// above, an unreadable or malformed file here fails the boot rather than
	// degrading: it is authored in this repo, so a bad one is a change somebody
	// just made, and a deployment that started anyway would show every tenant an
	// empty create screen with the reason in a log line nobody reads.
	AgentTemplatesPath string // path to agent_templates.yaml

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
	// PriorWorkTurns is how many previous turns' tool digests a turn reads
	// (T-Q6). Three because the block competes with the user's own question for
	// the model's attention, and a conversation's fourth-to-last turn is rarely
	// what a follow-up is following up on. Zero disables the read but not the
	// write, which is the setting for measuring whether it helps.
	PriorWorkTurns int
	// CookbookTopK is how many worked examples a turn is shown (T-Q8). Zero
	// switches retrieval off without stopping the harvest, which is how the
	// feature is measured: the same deployment, the same examples accumulating,
	// with only the injection off.
	CookbookTopK int
	// CookbookHarvestCron is when the worker mines finished turns into
	// examples. Empty disables the harvest without disabling retrieval, so a
	// deployment can stop learning and keep using what it has already learned.
	CookbookHarvestCron string
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

	// Public `/v1` API (T-13, extended by T-A1). APIV1RatePerMin is the
	// per-key request budget; it is deliberately the name T-A1 reserves, so
	// the limiter T-13 ships and the one that ticket configures are the same
	// setting rather than two that drift.
	APIV1RatePerMin int
	// APIV1Enabled is the kill switch. False answers 503 on every `/v1`
	// route including `/v1/me`, without touching `/api` — the dashboard must
	// keep working while a public surface is being taken off the air.
	APIV1Enabled bool
	// APIV1SyncTimeoutSeconds caps the synchronous door on `/v1/chat`
	// (T-A3). Read here so the value is one setting rather than a constant
	// in a handler nobody can change without a deploy.
	APIV1SyncTimeoutSeconds int
	// APIV1MaxBodyBytes bounds a `/v1` request body. A report spec arriving
	// over HTTP is untrusted in a way the agent's own spec never was, and the
	// first line of defence is refusing to read it, not validating it after.
	APIV1MaxBodyBytes int64
	// APIV1MaxSpecRows / APIV1MaxSpecCols bound what a spec may ask the
	// renderer to lay out (T-A2). The body cap above is not enough on its own:
	// 50 000 rows of small integers fit comfortably inside a megabyte, and
	// maroto will attempt every one of them.
	APIV1MaxSpecRows int
	APIV1MaxSpecCols int
	// Embedding (T-19). EmbedEnabled is the kill switch: false answers 503 on
	// the session mint and on every `/api/embed` route, and touches neither
	// `/api` nor `/v1`. Separate from APIV1Enabled because the two surfaces
	// fail for different reasons and a tenant's own website going quiet should
	// not require taking their integrations down with it.
	EmbedEnabled bool
	// EmbedSessionTTLMinutes is how long a minted session lives. Short by
	// design — the host page re-signs rather than us issuing a refresh cookie —
	// and configurable because the right number depends on how long a visitor
	// sits on one page, which differs per tenant.
	EmbedSessionTTLMinutes int
	// EmbedMaxTurnsPerHour is the per-`(company, embed_user_ref)` budget. It
	// bounds one visitor of one tenant's site, which is the only unit that
	// makes sense here: a refill keyed on anything wider lets one visitor spend
	// the workspace's credits and lock out everybody else on the same page.
	EmbedMaxTurnsPerHour int
	// APIV1SyncRenderTimeoutSecs is how long `POST /v1/reports/render` will
	// hold a connection open before the render becomes a job and the caller
	// gets a 202. Distinct from APIV1SyncTimeoutSeconds, which is a chat turn:
	// a render that takes twenty seconds is pathological, and a chat turn that
	// takes twenty seconds is Tuesday.
	APIV1SyncRenderTimeoutSecs int
	// APIV1CallbackAllowPrivate permits a `callback_url` pointing at an
	// address only this deployment can reach — loopback, RFC1918, link-local.
	// False everywhere it matters: the URL is chosen by the caller, and a POST
	// to 169.254.169.254 on their behalf is how an outbound webhook becomes
	// somebody else's credentials. True is for local development and for this
	// ticket's own gate, where the receiver is a script on localhost.
	APIV1CallbackAllowPrivate bool
	// MCPAllowPrivateEgress permits a tenant MCP server URL that resolves to an
	// address only this deployment can reach, and permits plaintext http to it
	// (T-M1). It is the same escape hatch APIV1CallbackAllowPrivate is, for the
	// same two users — a developer with a server on their laptop, and this
	// ticket's gate — and it is refused outside development for a stronger
	// reason than the callback flag: the tenant types this URL, we hold their
	// token, and we make the request from inside our own network.
	MCPAllowPrivateEgress bool
	// MCPAllowInsecureHTTP permits a tenant MCP server URL on plaintext http
	// (T-M1). Separate from MCPAllowPrivateEgress and honoured in every
	// environment, because "my MCP server has no TLS" and "my MCP server is on
	// your private network" are different requests: this one keeps every
	// address rule in force and only accepts the cost of the bearer token and
	// the tool results crossing the network in the clear. Off by default; the
	// boot log says so when it is on.
	MCPAllowInsecureHTTP bool
	// MCPProbeTimeoutSecs bounds one discovery attempt. An admin is waiting on
	// it, so it is shorter than a turn and longer than a healthy round trip.
	MCPProbeTimeoutSecs int
	// MCPCallTimeoutSecs bounds one tool call at turn time (T-M2). Separate from
	// the probe timeout because the two have different waiters — an admin
	// pressing Save versus an agent mid-turn — and a turn that spends its whole
	// wall-clock budget on one slow MCP server is a turn that answers nothing.
	MCPCallTimeoutSecs int
	// MCPMaxResponseBytes caps one MCP tool result before it enters the context
	// (T-M2). A server that answers with 40 MB of JSON is a context-window
	// incident and a bill; past this the call is a tool error the agent recovers
	// from. Generous relative to a run_sql result, because an MCP tool's output
	// is not a table this deployment shaped.
	MCPMaxResponseBytes int
	// MCPMaxCallsPerTurn caps how many MCP calls one turn may make across all its
	// MCP tools (T-M2). A backstop beside T-16's token and tool-call budgets,
	// aimed at the failure they do not catch cheaply: a fast, small tool called
	// in a tight loop, spending a turn on round trips.
	MCPMaxCallsPerTurn int

	// RenderBaseURL is where `apps/render` answers, e.g.
	// http://argentum-render:8090 (T-V3). **Empty is a supported deployment**,
	// not a broken one: `mp4` is then refused with a plain sentence and every
	// other format works exactly as before. The Helm chart ships the render
	// deployment disabled by default, so empty is also the common case.
	RenderBaseURL string
	// RenderSharedSecret is sent as `x-render-secret`. It is not the security
	// boundary — a ClusterIP Service and a NetworkPolicy are — it is what stops
	// a misconfiguration being silently exploitable.
	RenderSharedSecret string
	// RenderTimeoutSecs bounds one whole render from this side. It sits above
	// the service's own ten-minute wall clock so a service that gives up
	// answers us, rather than being cut off and reported as unreachable.
	RenderTimeoutSecs int
	// RenderMaxScenes / RenderMaxFrames bound what a spec may turn into
	// (T-V1's videoplan.Limits). They are not in APIV1MaxSpec* because they
	// apply to the agent's own videos too: the thing being protected is minutes
	// of a render pod that does one job at a time, and the agent can spend
	// those as easily as an integrator can.
	RenderMaxScenes int
	RenderMaxFrames int

	// WatcherEnabled is the kill switch for the whole watcher subsystem (T-08).
	// When off, the worker does not start the watcher periodic manager, so no
	// evaluation ticks fire — the panic button for a deployment whose watchers
	// are misbehaving, without editing every tenant's rows.
	WatcherEnabled bool
	// WatcherMaxPerCompany caps how many watchers one tenant may define (T-08).
	// A watcher is a standing SQL evaluation on a cron; an unbounded count is an
	// unbounded background load and, once each fires an agent turn, an unbounded
	// bill.
	WatcherMaxPerCompany int
	// APIV1ObsFlushSeconds is how often the request recorder writes what it has
	// buffered (T-A5). It is the staleness of the tenant's own error list and
	// the write rate of the rollup, traded against each other: the acceptance
	// criterion is "within a minute", and the default leaves room for a failed
	// flush and the next one to carry the difference.
	APIV1ObsFlushSeconds int
	// APIV1ObsRetentionDays bounds how long `/v1` request counters and failures
	// are kept. Longer than any debugging session, short enough that the tables
	// stay small.
	APIV1ObsRetentionDays int

	// MetricsToken gates `/metrics` when set: `Authorization: Bearer <token>`.
	//
	// Unset serves the endpoint as it always has, minus the per-key block —
	// T-A5 labels request counters by API key id, and a key id is a tenant's
	// identifier for a credential they hold, so it does not go out on an
	// endpoint with no credential of its own. Moving `/metrics` off the public
	// router entirely is T-17's job; this is the smaller thing that stops this
	// ticket adding a tenant identifier to an open endpoint on the way.
	MetricsToken string

	// MCPServerAddr is where cmd/mcp listens (T-14). A separate process on a
	// separate port: an MCP session is long-lived and speaks its own protocol,
	// and one that hangs must not be able to exhaust the dashboard's connection
	// budget. Authentication is the same API key `/v1` takes.
	MCPServerAddr string

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

		SlackEnabled:    getEnv("SLACK_ENABLED", "true") == "true",
		SlackAPIBaseURL: getEnv("SLACK_API_BASE_URL", ""),

		// Metabase
		MetabaseURL:           getEnv("METABASE_URL", "http://localhost:3000"),
		MetabasePublicURL:     getEnv("METABASE_PUBLIC_URL", ""),
		MetabaseAPIKey:        getEnv("METABASE_API_KEY", ""),
		MetabaseAdminEmail:    getEnv("METABASE_ADMIN_EMAIL", "admin@argentum.local"),
		MetabaseAdminPassword: getEnv("METABASE_ADMIN_PASSWORD", ""),

		// Agent SDK Configuration
		AgentConfigPath:      getEnv("AGENT_CONFIG_PATH", "config/agents.yaml"),
		GuardrailsConfigPath: getEnv("GUARDRAILS_CONFIG_PATH", "config/guardrails.yaml"),
		AgentTemplatesPath:   getEnv("AGENT_TEMPLATES_PATH", "config/agent_templates.yaml"),

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
		PriorWorkTurns:      getEnvAsInt("PRIOR_WORK_TURNS", 3),
		CookbookTopK:        getEnvAsInt("COOKBOOK_TOP_K", 3),
		CookbookHarvestCron: getEnv("COOKBOOK_HARVEST_CRON", "17 * * * *"),
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

		// Public API
		APIV1RatePerMin:         getEnvAsInt("API_V1_RATE_PER_MIN", 120),
		APIV1Enabled:            getEnv("API_V1_ENABLED", "true") == "true",
		APIV1SyncTimeoutSeconds: getEnvAsInt("API_V1_SYNC_TIMEOUT_SECONDS", 120),
		APIV1MaxBodyBytes:       int64(getEnvAsInt("API_V1_MAX_BODY_BYTES", 1<<20)),

		EmbedEnabled:           getEnv("EMBED_ENABLED", "true") == "true",
		EmbedSessionTTLMinutes: getEnvAsInt("EMBED_SESSION_TTL_MINUTES", 15),
		EmbedMaxTurnsPerHour:   getEnvAsInt("EMBED_MAX_TURNS_PER_HOUR", 60),

		APIV1MaxSpecRows:           getEnvAsInt("API_V1_MAX_SPEC_ROWS", 50_000),
		APIV1MaxSpecCols:           getEnvAsInt("API_V1_MAX_SPEC_COLS", 40),
		APIV1SyncRenderTimeoutSecs: getEnvAsInt("API_V1_SYNC_RENDER_TIMEOUT", 20),
		APIV1CallbackAllowPrivate:  getEnv("API_V1_CALLBACK_ALLOW_PRIVATE", "false") == "true",
		MCPAllowPrivateEgress:      getEnv("MCP_ALLOW_PRIVATE_EGRESS", "false") == "true",
		MCPAllowInsecureHTTP:       getEnv("MCP_ALLOW_INSECURE_HTTP", "false") == "true",
		MCPProbeTimeoutSecs:        getEnvAsInt("MCP_PROBE_TIMEOUT_SECS", 15),
		MCPCallTimeoutSecs:         getEnvAsInt("MCP_CALL_TIMEOUT_SECS", 30),
		MCPMaxResponseBytes:        getEnvAsInt("MCP_MAX_RESPONSE_BYTES", 262144),
		MCPMaxCallsPerTurn:         getEnvAsInt("MCP_MAX_CALLS_PER_TURN", 20),
		RenderBaseURL:              getEnv("RENDER_BASE_URL", ""),
		RenderSharedSecret:         getEnv("RENDER_SHARED_SECRET", ""),
		RenderTimeoutSecs:          getEnvAsInt("RENDER_TIMEOUT_SECS", 900),
		RenderMaxScenes:            getEnvAsInt("RENDER_MAX_SCENES", 60),
		RenderMaxFrames:            getEnvAsInt("RENDER_MAX_FRAMES", 18_000),
		WatcherEnabled:             getEnv("WATCHER_ENABLED", "true") == "true",
		WatcherMaxPerCompany:       getEnvAsInt("WATCHER_MAX_PER_COMPANY", 20),
		APIV1ObsFlushSeconds:       getEnvAsInt("API_V1_OBS_FLUSH_SECONDS", 15),
		APIV1ObsRetentionDays:      getEnvAsInt("API_V1_OBS_RETENTION_DAYS", 30),

		MetricsToken:  getEnv("METRICS_TOKEN", ""),
		MCPServerAddr: getEnv("MCP_SERVER_ADDR", ":8081"),

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
		// The app secret is the entirety of /webhook/whatsapp's authentication
		// (T-H1/T-H3): the route is mounted outside middleware.Auth because the
		// caller is Meta and holds no Argentum credential, so an unset secret
		// is an unauthenticated path into a tenant's agent. VerifyWebhook now
		// answers false without one rather than true, so a development box
		// boots and returns 401; production must not be in that state at all.
		//
		// Warned rather than fatal, by owner's decision 2026-08-11: a boot that
		// dies on a setting the previous release tolerated turns a security fix
		// into an outage on the rollout that carries it. The refusal that
		// actually protects the endpoint is at request time and is unaffected —
		// VerifyWebhook answers false without a secret, so every callback is
		// 401 whether or not anyone reads this line.
		if c.IsProduction() && c.WhatsAppAppSecret == "" {
			logrus.Warn("WHATSAPP_APP_SECRET is unset in production: /webhook/whatsapp is the tenant's agent behind one signature check, and without the secret every callback is refused 401 — inbound WhatsApp is effectively off until it is set")
		}
	case "twilio":
		// TWILIO_AUTH_TOKEN is in this triple, and it is also the webhook
		// signing key — which is why the Twilio half of the rule above needs no
		// separate check here.
		if c.TwilioAccountSID == "" || c.TwilioAuthToken == "" || c.TwilioFromNumber == "" {
			return fmt.Errorf("TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_WHATSAPP_NUMBER are required for twilio provider")
		}
	}

	// An empty CORS_ORIGINS makes the middleware reflect whatever Origin it is
	// handed, and the dashboard authenticates with a cookie — reflection plus
	// Access-Control-Allow-Credentials is a browser's permission for any site
	// to read an authenticated response. Survivable on a laptop where the only
	// session is the developer's own; not in production.
	//
	// Warned rather than fatal, same decision and same date. This one keeps a
	// real hole open, so say what it is rather than filing it as a nag: with
	// the list empty, any site a logged-in user visits can read their
	// authenticated dashboard responses. Set CORS_ORIGINS.
	if c.IsProduction() && len(c.CORSOrigins) == 0 {
		logrus.Warn("CORS_ORIGINS is empty in production: the middleware reflects every Origin back with Access-Control-Allow-Credentials, which lets any site a logged-in user visits read their authenticated responses — set it to the dashboard host")
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

// VideoClient builds the render-service client, or nil when no render service
// is configured (T-V3).
//
// It lives here, beside WorkerQueueMap, because **both processes build one**:
// the worker's stack for the agent's `generate_document`, and `cmd/api` for
// `/v1`. Those two already keep their own copies of the object-storage wiring,
// and the copies have not drifted yet only because nobody has edited one. A
// client built two ways is a deployment where the agent can produce a video
// and the API cannot, with nothing to see in either log.
func (c *Config) VideoClient() *video.Client {
	return video.New(video.Options{
		BaseURL: c.RenderBaseURL,
		Secret:  c.RenderSharedSecret,
		Timeout: time.Duration(c.RenderTimeoutSecs) * time.Second,
	})
}

// VideoLimits bounds what a spec may turn into as scenes and frames.
func (c *Config) VideoLimits() videoplan.Limits {
	return videoplan.Limits{
		MaxScenes:      c.RenderMaxScenes,
		MaxTotalFrames: c.RenderMaxFrames,
	}
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
	// The video lane, added rather than configured (T-V3). A worker that does
	// not consume it leaves every video queued forever with nothing in a log to
	// say why — and `WORKER_QUEUES` is set per deployment, so shipping the lane
	// in the default string would reach no existing one. A low weight is the
	// point: a render pod takes one job at a time, so a second video waits on
	// the pod regardless, and there is nothing to gain by racing PDFs for the
	// worker's attention.
	if _, ok := out[queue.QueueVideo]; !ok {
		out[queue.QueueVideo] = 1
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
