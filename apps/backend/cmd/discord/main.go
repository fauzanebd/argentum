// Argentum discord gateway: owns one discordgo.Session per enabled tenant,
// routes inbound DM/mention/reply messages into the chat pipeline, and
// subscribes to the Redis outbound channel to deliver final assistant
// replies through the matching tenant session.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/discord"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}
	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.Info("Argentum discord gateway starting")

	if !cfg.DiscordEnabled {
		logrus.Info("DISCORD_ENABLED=false; exiting without opening any sessions")
		return
	}

	// --- Control DB ---
	controlDB, err := pgctl.New(cfg.DatabaseURL())
	if err != nil {
		logrus.Fatalf("control DB: %v", err)
	}
	defer controlDB.Close()

	companyRepo := pgctl.NewCompanyRepo(controlDB)
	threadRepo := pgctl.NewThreadRepo(controlDB)
	messageRepo := pgctl.NewMessageRepo(controlDB)
	llmCredRepo := pgctl.NewCompanyLLMCredentialRepo(controlDB)
	usageRepo := pgctl.NewUsageRepo(controlDB)
	creditsRepo := pgctl.NewCreditsRepo(controlDB)
	discordCredRepo := pgctl.NewCompanyDiscordCredentialRepo(controlDB)
	allowedUsersRepo := pgctl.NewAllowedDiscordUserRepo(controlDB)

	// --- Crypto + Redis ---
	dsnCipher, err := crypto.NewKeyring(cfg.DSNEncryptionKeyHex, cfg.DSNRetiredKeysHex)
	if err != nil {
		logrus.Fatalf("DSN cipher: %v", err)
	}

	rdb := buildRedisClient(cfg)
	if rdb == nil {
		logrus.Fatal("redis client is required (REDIS_URL)")
	}
	defer func() { _ = rdb.Close() }()
	bus := eventbus.NewRedisBus(rdb)
	_ = bus // not used directly; cmd/api owns the reload publisher

	// --- Chat pipeline (enqueue only; worker does the agent run) ---
	asynqOpt, err := queue.BuildRedisOpt(cfg.ResolvedAsynqRedisURL(), cfg.RedisPassword)
	if err != nil {
		logrus.Fatalf("asynq redis opt: %v", err)
	}
	enq := queue.NewEnqueuer(asynqOpt)
	defer func() { _ = enq.Close() }()

	// Discord threads don't run the topic classifier or rolling summary at
	// resolve time (idle threshold gating is still in place — failure to
	// classify is fail-open). Pass nil LLM/classifier deps; ThreadService
	// only uses them inside refreshSummary which is fire-and-forget.
	threadSvc := app.NewThreadService(threadRepo, messageRepo, nil, nil, app.ThreadServiceConfig{
		IdleMinutes:        cfg.ThreadIdleMinutes,
		SummaryEveryNTurns: cfg.SummaryEveryNTurns,
	})
	// This process never runs the agent, so its UsageService exists only to
	// answer CheckBudget — records are written by the worker. It is built
	// from the same config as the other two so a Discord user hits the same
	// wall a dashboard user does (T-03).
	usageSvc := app.NewUsageService(usageRepo, creditsRepo, app.DefaultPricing).
		WithCredits(app.CreditPolicy{
			Enforce:       cfg.CreditsEnforcementEnabled,
			WarnPct:       cfg.CreditsWarningThresholdPct,
			GrantMicroUSD: cfg.CreditsDefaultGrantMicroUSD(),
		}, llmCredRepo, app.NewRedisBudgetCache(rdb))
	// Same roster wiring as cmd/api (T-S2): this process enqueues turns too, so
	// a Discord message resolves to the company's default agent rather than
	// leaving the worker to guess. And the same bindings (T-S4) — the gateway
	// is the *other* Discord call site the ticket warned about, and a binding
	// honoured by the webhook and not by the bot is a channel that answers as
	// two different agents depending on how the message reached us.
	chatEnq := app.NewChatEnqueuer(threadSvc, messageRepo, companyRepo, enq).
		WithBudget(usageSvc).
		WithRoster(pgctl.NewAgentRepo(controlDB)).
		WithChannelBindings(pgctl.NewAgentBindingRepo(controlDB))

	// --- Discord session manager ---
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	dispatcher := newDispatcher(chatEnq, allowedUsersRepo, discordCredRepo)
	manager := discord.NewSessionManager(discordCredRepo, dsnCipher, dispatcher)
	if err := manager.Start(rootCtx); err != nil {
		logrus.Fatalf("session manager start: %v", err)
	}
	defer manager.Close()
	dispatcher.sender = manager

	// --- Outbound subscriber: worker -> this process -> discord ---
	outboundSub := rdb.PSubscribe(rootCtx, eventbus.OutboundPattern(string(domain.ChannelDiscord)))
	defer outboundSub.Close()

	// --- Reload subscriber: api -> this process ---
	reloadSub := rdb.Subscribe(rootCtx, eventbus.DiscordReloadChannel)
	defer reloadSub.Close()

	go consumeOutbound(rootCtx, outboundSub, manager)
	go consumeReloads(rootCtx, reloadSub, manager)

	// --- Wait for shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrus.Info("Shutting down discord gateway…")
}

// dispatcher routes an inbound discord message into ChatEnqueuer after
// verifying the user is on the company allowlist. unauthorized users get a
// one-shot rejection DM through the same gateway session.
type dispatcher struct {
	chat   *app.ChatEnqueuer
	users  domain.AllowedDiscordUserRepository
	creds  domain.CompanyDiscordCredentialRepository
	sender discord.Provider // set after manager is built
}

func newDispatcher(chat *app.ChatEnqueuer, users domain.AllowedDiscordUserRepository, creds domain.CompanyDiscordCredentialRepository) *dispatcher {
	return &dispatcher{chat: chat, users: users, creds: creds}
}

func (d *dispatcher) Dispatch(ctx context.Context, in discord.InboundMessage) error {
	allowed, err := d.users.IsAllowed(ctx, in.CompanyID, in.UserID)
	if err != nil {
		return err
	}
	if !allowed {
		logrus.WithFields(logrus.Fields{
			"company_id":     in.CompanyID,
			"discord_user":   in.UserID,
			"application_id": in.ApplicationID,
		}).Info("discord inbound: user not on allowlist; dropping")
		if d.sender != nil {
			_ = d.sender.Send(in.CompanyID, in.ChannelID,
				"You're not authorised to chat with this bot. Ask the workspace admin to add your Discord user id in the Argentum dashboard.")
		}
		return nil
	}
	_, err = d.chat.Enqueue(ctx, app.ChatInput{
		Channel:          domain.ChannelDiscord,
		CompanyID:        in.CompanyID,
		DiscordUserID:    in.UserID,
		DiscordChannelID: in.ChannelID,
		Message:          in.Content,
	})
	// A tenant out of credit gets the sentence, not a dropped message. The
	// error is swallowed after sending because returning it would surface in
	// the gateway log as a failure, and nothing failed.
	if errors.Is(err, domain.ErrInsufficientCredits) {
		if d.sender != nil {
			_ = d.sender.Send(in.CompanyID, in.ChannelID, app.CreditsExhaustedMessage)
		}
		return nil
	}
	return err
}

// consumeOutbound reads finalized assistant messages off the outbound pub/sub
// channel and writes them through the matching tenant session.
func consumeOutbound(ctx context.Context, sub *redis.PubSub, sender discord.Provider) {
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var evt app.OutboundEvent
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				logrus.WithError(err).Warn("outbound: malformed payload")
				continue
			}
			if evt.Channel != string(domain.ChannelDiscord) {
				continue
			}
			if err := sender.Send(evt.CompanyID, evt.ChannelRef, evt.Content); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"company_id": evt.CompanyID,
					"channel":    evt.ChannelRef,
				}).Error("outbound: discord send failed")
			}
		}
	}
}

// consumeReloads picks up "credential rotated" signals from cmd/api and
// re-opens the corresponding tenant session.
func consumeReloads(ctx context.Context, sub *redis.PubSub, manager *discord.SessionManager) {
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			companyID := strings.TrimSpace(msg.Payload)
			if companyID == "" {
				continue
			}
			reloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := manager.Reload(reloadCtx, companyID); err != nil {
				logrus.WithError(err).WithField("company_id", companyID).
					Error("discord reload failed")
			}
			cancel()
		}
	}
}

func buildRedisClient(cfg *config.Config) *redis.Client {
	if cfg.RedisURL == "" {
		return nil
	}
	url := cfg.RedisURL
	if !strings.Contains(url, "://") {
		return redis.NewClient(&redis.Options{Addr: url, Password: cfg.RedisPassword})
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		logrus.WithError(err).Warn("redis: invalid REDIS_URL; using bare addr")
		return redis.NewClient(&redis.Options{Addr: url, Password: cfg.RedisPassword})
	}
	if cfg.RedisPassword != "" {
		opt.Password = cfg.RedisPassword
	}
	return redis.NewClient(opt)
}
