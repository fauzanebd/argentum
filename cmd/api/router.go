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
	handlers.NewAuthHandler(d.authSvc, cfg.CookieSecure, d.signer.RefreshTTL()).Register(api.Group("/auth"))

	authed := api.Group("")
	authed.Use(middleware.Auth(d.signer))
	if rateLimiter := middleware.NewRateLimiter(d.rdb, 60, 1.0); rateLimiter != nil {
		authed.Use(rateLimiter.Middleware())
	}
	handlers.NewCompanyHandler(d.companySvc).Register(authed)
	handlers.NewChatHandler(d.chatEnq, d.threadRepo, d.msgRepo, d.dashboardSvc).Register(authed)
	handlers.NewUsageHandler(d.usageSvc).Register(authed)
	handlers.NewUserHandler(d.userRepo, d.companyRepo).Register(authed.Group("/users"))
	if d.dashboardSvc != nil {
		handlers.NewDashboardHandler(d.dashboardSvc).Register(authed)
	}
	if d.scheduledSvc != nil {
		handlers.NewScheduledTasksHandler(d.scheduledSvc).Register(authed)
	}
	authed.GET("/threads/:id/stream", ws.NewHandler(d.rdb, d.threadRepo, cfg.CORSOrigins).Stream)

	handlers.NewWebhookHandler(d.chatEnq, d.companySvc, d.wa, cfg.WhatsAppWebhookVerifyToken).
		Register(r.Group("/webhook"))

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
