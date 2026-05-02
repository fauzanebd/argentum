package main

import (
	"context"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/migrate"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// bootstrap wires control-plane DB, tenant pool, Redis, queue, services, and WhatsApp.
// On failure, partial resources are torn down before returning.
func bootstrap(ctx context.Context, cfg *config.Config) (_ *apiDeps, err error) {
	deps := &apiDeps{cfg: cfg}
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	defer func() {
		if err != nil {
			rollback()
		}
	}()

	if err := migrate.Up(cfg.DatabaseURL(), cfg.ControlMigrationsDir); err != nil {
		return nil, fmt.Errorf("control migrations: %w", err)
	}

	controlDB, err := pgctl.New(cfg.DatabaseURL())
	if err != nil {
		return nil, fmt.Errorf("control DB: %w", err)
	}
	deps.controlDB = controlDB
	undo = append(undo, func() { controlDB.Close() })

	companyRepo := pgctl.NewCompanyRepo(controlDB)
	userRepo := pgctl.NewUserRepo(controlDB)
	connRepo := pgctl.NewConnectionRepo(controlDB)
	phoneRepo := pgctl.NewPhoneRepo(controlDB)
	threadRepo := pgctl.NewThreadRepo(controlDB)
	messageRepo := pgctl.NewMessageRepo(controlDB)
	usageRepo := pgctl.NewUsageRepo(controlDB)
	creditsRepo := pgctl.NewCreditsRepo(controlDB)
	deps.threadRepo = threadRepo
	deps.msgRepo = messageRepo

	dsnCipher, err := crypto.NewFromHex(cfg.DSNEncryptionKeyHex)
	if err != nil {
		return nil, fmt.Errorf("DSN cipher: %w", err)
	}

	signer, err := auth.NewTokenSigner(cfg.JWTSecret, 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("JWT signer: %w", err)
	}
	deps.signer = signer

	resolver := pgctl.NewConnectionResolver(connRepo, dsnCipher)
	deps.tenant = db.NewTenantConnPool(resolver, 200, 30*time.Minute)
	tenCtx, cancelTen := context.WithCancel(ctx)
	deps.cancelTen = cancelTen
	deps.tenant.Start(tenCtx)
	undo = append(undo, func() {
		deps.tenant.CloseAll()
		cancelTen()
	})

	rdb := buildRedisClient(cfg)
	if rdb == nil {
		return nil, fmt.Errorf("redis client is required (REDIS_URL)")
	}
	deps.rdb = rdb
	undo = append(undo, func() { rdb.Close() })

	asynqOpt, err := queue.BuildRedisOpt(cfg.ResolvedAsynqRedisURL(), cfg.RedisPassword)
	if err != nil {
		return nil, fmt.Errorf("asynq redis opt: %w", err)
	}
	deps.enqueuer = queue.NewEnqueuer(asynqOpt)
	undo = append(undo, func() { deps.enqueuer.Close() })

	llmClient := buildLLM(cfg)

	deps.authSvc = app.NewAuthService(companyRepo, userRepo, signer)

	var metabaseWarehouse *app.MetabaseWarehouseSync
	if cfg.MetabaseURL != "" && cfg.MetabaseAdminEmail != "" && cfg.MetabaseAdminPassword != "" {
		mbCli := metabase.NewClient(cfg.MetabaseURL, cfg.MetabasePublicURL,
			cfg.MetabaseAdminEmail, cfg.MetabaseAdminPassword)
		metabaseWarehouse = app.NewMetabaseWarehouseSync(mbCli)
	}
	deps.companySvc = app.NewCompanyService(connRepo, phoneRepo, dsnCipher, deps.tenant, metabaseWarehouse)
	deps.usageSvc = app.NewUsageService(usageRepo, creditsRepo, app.DefaultPricing)
	classifier := app.NewTopicClassifier(llmClient)
	threadSvc := app.NewThreadService(threadRepo, messageRepo, classifier, llmClient,
		app.ThreadServiceConfig{
			IdleMinutes:        cfg.ThreadIdleMinutes,
			SummaryEveryNTurns: cfg.SummaryEveryNTurns,
		})
	deps.chatEnq = app.NewChatEnqueuer(threadSvc, messageRepo, deps.enqueuer)

	waProvider, err := whatsapp.NewProvider(whatsapp.Config{
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
		return nil, fmt.Errorf("WhatsApp provider: %w", err)
	}
	deps.wa = waProvider

	deps.metrics = metrics.NewCollector()
	return deps, nil
}
