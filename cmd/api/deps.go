package main

import (
	"context"
	"database/sql"

	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/config"
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

	signer      *auth.TokenSigner
	authSvc     *app.AuthService
	companySvc      *app.CompanyService
	usageSvc        *app.UsageService
	chatEnq         *app.ChatEnqueuer
	threadRepo      *pgctl.ThreadRepo
	msgRepo         *pgctl.MessageRepo
	userRepo        *pgctl.UserRepo
	companyRepo     *pgctl.CompanyRepo
	dashboardSvc    *app.DashboardService

	wa whatsapp.Provider

	metrics *metrics.Collector
}

// cleanup releases resources in reverse order of creation (same as the original defer stack).
func (d *apiDeps) cleanup() {
	if d.enqueuer != nil {
		d.enqueuer.Close()
	}
	if d.rdb != nil {
		d.rdb.Close()
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
