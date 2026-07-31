package main

import (
	"context"
	"database/sql"

	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/apiobs"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// apiDeps holds infra and services needed to build the HTTP router and run health checks.
type apiDeps struct {
	cfg *config.Config

	controlDB *sql.DB
	cancelTen context.CancelFunc
	tenant    *db.TenantConnPool
	rdb       *redis.Client
	enqueuer  *queue.Enqueuer

	signer       *auth.TokenSigner
	authSvc      *app.AuthService
	teamSvc      *app.TeamService
	companySvc   *app.CompanyService
	embeddingSvc *app.EmbeddingService
	usageSvc     *app.UsageService
	chatEnq      *app.ChatEnqueuer
	threadRepo   *pgctl.ThreadRepo
	msgRepo      *pgctl.MessageRepo
	userRepo     *pgctl.UserRepo
	companyRepo  *pgctl.CompanyRepo
	actionRepo   *pgctl.AgentActionRepo
	// usageRepo is read directly by `/v1/chat` for one thing UsageService does
	// not expose: what a single turn cost, over a window bounded by time.Time
	// rather than by the dashboard's string dates.
	usageRepo    *pgctl.UsageRepo
	dashboardSvc *app.DashboardService
	scheduledSvc *app.ScheduledTaskService
	discordSvc   *app.DiscordService
	larkSvc      *app.LarkService
	brandingSvc  *branding.Service
	apiKeySvc    *app.APIKeyService
	// The tenant agent roster (T-S1). It holds this deployment's tool registry
	// by name, which is why it is built from the same tools.Registry the
	// worker runs rather than from a list maintained beside the handler.
	agentSvc *app.AgentService
	// Channel bindings (T-S4): which agent answers in which Discord channel,
	// Lark chat or WhatsApp number. Separate from agentSvc because the enqueuer
	// reads the same table on every inbound message and must not be handed a
	// service that can write to it.
	agentBindingSvc *app.AgentBindingService
	// The `/v1` report surface (T-A2). docGen and storageSvc are nil on a
	// deployment without object storage — the same condition that leaves
	// generate_document unregistered in the worker — and the routes that need
	// them answer a typed 503 rather than being absent, so an integrator gets
	// a reason instead of a 404.
	docGen       *docgen.Service
	storageSvc   *storage.StorageService
	reportRepo   *pgctl.APIReportRepo
	documentRepo *pgctl.DocumentRepo
	idemStore    idempotency.Store
	// apiKeyAuth overrides what authenticates `/v1`. Nil in production, where
	// apiKeySvc is used; see apiKeyAuthOf in router.go for why the seam exists.
	apiKeyAuth middleware.APIKeyAuthenticator
	// larkReplier lets the webhook answer a turn it refuses before enqueueing
	// (T-03). Nil when Lark is disabled.
	larkReplier lark.Provider

	wa whatsapp.Provider

	llmCache   *llmtenant.ClientCache
	embedCache *llmtenant.EmbeddingCache

	metrics *metrics.Collector
	// Integrator-facing observability over `/v1` (T-A5). requestObs buffers
	// samples off the request path and flushes batches; requestRepo is the same
	// store read back by the dashboard's API Keys tab. stopObs ends the flush
	// loop — cleanup calls it, then flushes what the loop had not.
	requestObs  *apiobs.Recorder
	requestRepo *pgctl.APIRequestRepo
	stopObs     context.CancelFunc
}

// cleanup releases resources in reverse order of creation (same as the original defer stack).
func (d *apiDeps) cleanup() {
	// First, because it writes to the control DB this function later closes —
	// and because the records it is holding cover the minutes immediately
	// before a shutdown, which is when somebody is most likely to be looking.
	if d.stopObs != nil {
		d.stopObs()
	}
	if d.requestObs != nil {
		d.requestObs.Close()
	}
	if d.embedCache != nil {
		d.embedCache.CloseAll()
	}
	if d.llmCache != nil {
		d.llmCache.CloseAll()
	}
	if d.enqueuer != nil {
		_ = d.enqueuer.Close()
	}
	if d.rdb != nil {
		_ = d.rdb.Close()
	}
	if d.tenant != nil {
		d.tenant.CloseAll()
	}
	if d.cancelTen != nil {
		d.cancelTen()
	}
	if d.controlDB != nil {
		d.controlDB.Close()
	}
}
