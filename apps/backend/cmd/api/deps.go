package main

import (
	"context"
	"database/sql"

	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/queue"
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
	dashboardSvc *app.DashboardService
	scheduledSvc *app.ScheduledTaskService
	discordSvc   *app.DiscordService
	larkSvc      *app.LarkService
	brandingSvc  *branding.Service
	// larkReplier lets the webhook answer a turn it refuses before enqueueing
	// (T-03). Nil when Lark is disabled.
	larkReplier lark.Provider

	wa whatsapp.Provider

	llmCache   *llmtenant.ClientCache
	embedCache *llmtenant.EmbeddingCache

	metrics *metrics.Collector
}

// cleanup releases resources in reverse order of creation (same as the original defer stack).
func (d *apiDeps) cleanup() {
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
