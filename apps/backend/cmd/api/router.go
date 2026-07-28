package main

import (
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/transport/http/handlers"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
	"github.com/fauzanebd/argentum/internal/transport/ws"
)

func newRouter(d *apiDeps) *gin.Engine {
	cfg := d.cfg
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogging())
	r.Use(middleware.CORS(cfg.CORSOrigins))

	registerHealthRoutes(r, d.metrics, d.controlDB)

	api := r.Group("/api")
	handlers.NewMetaHandler().Register(api.Group("/meta"))
	handlers.NewAuthHandler(d.authSvc, d.teamSvc, cfg.CookieSecure, d.signer.RefreshTTL()).
		Register(api.Group("/auth"))

	authed := api.Group("")
	authed.Use(middleware.Auth(d.signer))
	// RequireRole runs after Auth (it reads the role Auth sets) and before the
	// rate limiter, so a request a member is not allowed to make does not
	// consume their budget. apiPolicy in policy.go is the whole access model.
	authed.Use(middleware.RequireRole(apiPolicy))
	if rateLimiter := middleware.NewRateLimiter(d.rdb, 60, 1.0); rateLimiter != nil {
		authed.Use(rateLimiter.Middleware())
	}
	handlers.NewCompanyHandler(d.companySvc, d.embeddingSvc).Register(authed)
	handlers.NewChatHandler(d.chatEnq, d.threadRepo, d.msgRepo, d.dashboardSvc).Register(authed)
	handlers.NewUsageHandler(d.usageSvc).Register(authed)
	handlers.NewConfigHandler(cfg).Register(authed)
	handlers.NewUserHandler(d.userRepo, d.companyRepo, d.teamSvc).Register(authed.Group("/users"))
	handlers.NewReportsHandler(d.brandingSvc, d.companyRepo).Register(authed)
	handlers.NewAuditHandler(d.actionRepo).Register(authed)
	if d.dashboardSvc != nil {
		handlers.NewDashboardHandler(d.dashboardSvc).Register(authed)
	}
	if d.scheduledSvc != nil {
		handlers.NewScheduledTasksHandler(d.scheduledSvc).Register(authed)
	}
	if d.discordSvc != nil {
		handlers.NewDiscordHandler(d.discordSvc).Register(authed)
	}
	if d.larkSvc != nil {
		handlers.NewLarkHandler(d.larkSvc).Register(authed)
	}
	authed.GET("/threads/:id/stream", ws.NewHandler(d.rdb, d.threadRepo, cfg.CORSOrigins).Stream)

	webhookGroup := r.Group("/webhook")
	handlers.NewWebhookHandler(d.chatEnq, d.companySvc, d.wa, cfg.WhatsAppWebhookVerifyToken).
		Register(webhookGroup)
	if d.discordSvc != nil {
		handlers.NewDiscordWebhookHandler(d.discordSvc).Register(webhookGroup)
	}
	if d.larkSvc != nil {
		handlers.NewLarkWebhookHandler(d.larkSvc, d.chatEnq).
			WithReplier(d.larkReplier).
			Register(webhookGroup)
	}

	if cfg.MetabaseURL != "" {
		mbURL, _ := url.Parse(cfg.MetabaseURL)
		mbProxy := httputil.NewSingleHostReverseProxy(mbURL)
		r.Any("/metabase/*path", func(c *gin.Context) {
			c.Request.URL.Path = strings.TrimPrefix(c.Param("path"), "/metabase")
			if c.Request.URL.Path == "" {
				c.Request.URL.Path = "/"
			}
			c.Request.Host = mbURL.Host
			mbProxy.ServeHTTP(c.Writer, c.Request)
		})
	}

	return r
}
