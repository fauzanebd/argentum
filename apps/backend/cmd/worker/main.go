// Argentum worker: consumes asynq tasks (`chat:run`, `scheduled:run`) and
// runs the analytics agent. Stays offline-friendly: any number of replicas
// can share the same Redis-backed queue; failures retry automatically;
// chat events fan out to API replicas via Redis pub/sub. The same process
// also hosts the asynq.PeriodicTaskManager that emits `scheduled:run`
// ticks for enabled scheduled_tasks rows.
//
// The agent itself — repos, tenant pool, LLM caches, tools, guardrails,
// system prompt, agent factory — is built by internal/bootstrap, so the
// eval harness scores the same agent this process runs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"

	_ "github.com/fauzanebd/argentum/internal/adapters/db/mysql"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/sqlserver"
	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/bootstrap"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tracing"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
	"github.com/fauzanebd/argentum/internal/webhookout"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}
	level, _ := logrus.ParseLevel(cfg.LogLevel)
	logrus.SetLevel(level)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.Info("Argentum worker starting")

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	// OTel (T-17). A no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set, so a
	// deployment with no collector runs exactly as it did.
	shutdownTracing, err := tracing.Init(rootCtx, "argentum-worker", "1")
	if err != nil {
		logrus.WithError(err).Warn("otel: tracing not enabled")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(ctx); err != nil {
			logrus.WithError(err).Warn("otel: exporter did not flush cleanly")
		}
	}()

	stack, err := bootstrap.New(rootCtx, cfg)
	if err != nil {
		logrus.Fatalf("bootstrap: %v", err)
	}
	defer stack.Close()

	bus := eventbus.NewRedisBus(stack.Redis)

	// --- WhatsApp provider (worker sends final replies for WA threads) ---
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
		logrus.Fatalf("WhatsApp provider: %v", err)
	}

	// Report jobs and their callbacks (T-A2). Built before the runner because
	// the runner closes a report out when its turn ends — the API process only
	// creates these rows, and everything that moves one lives here.
	reportRepo := pgctl.NewAPIReportRepo(stack.ControlDB)
	deliveryRepo := pgctl.NewWebhookDeliveryRepo(stack.ControlDB)
	companyRepo := pgctl.NewCompanyRepo(stack.ControlDB)
	webhookSender := webhookout.NewSender(deliveryRepo, companyRepo, stack.Enqueuer(), cfg.APIV1CallbackAllowPrivate)
	// T-15: standing subscriptions, and the fan-out that feeds them. The sender
	// above is the delivery half and is not duplicated — this decides who gets
	// told, webhookout decides how it travels. The deliverer counts terminal
	// outcomes against the subscription that produced them, which is what
	// disables one after twenty consecutive failures.
	subscriptionRepo := pgctl.NewWebhookSubscriptionRepo(stack.ControlDB)
	webhookSubs := app.NewWebhookSubscriptionService(subscriptionRepo, webhookSender)
	webhookDeliverer := webhookout.NewDeliverer(deliveryRepo, companyRepo, cfg.APIV1CallbackAllowPrivate, webhookMaxAttempts).
		WithSubscriptions(subscriptionRepo, domain.WebhookAutoDisableAfter)
	reportSvc := app.NewAPIReportService(reportRepo, stack.Documents, stack.Docs, webhookSender)

	// The three publishers. All on the worker, because all three events happen
	// here: a watcher fires on a tick, an action executes after approval is
	// relayed to this process, and a scheduled run ends when its turn does.
	stack.Watchers.WithWebhooks(webhookSubs)
	stack.Actions.WithWebhooks(webhookSubs)
	stack.ScheduledSvc.WithWebhooks(webhookSubs)

	runner := stack.NewChatRunner(bus, waProvider).WithAPIReports(reportSvc)

	// Lark outbound: worker calls the Lark Open Platform REST API to post
	// replies on the bot's behalf. Token caching is per-company, on-demand.
	var larkProv lark.Provider
	if cfg.LarkEnabled {
		larkCredRepo := pgctl.NewCompanyLarkCredentialRepo(stack.ControlDB)
		larkProv = lark.NewClient(larkCredRepo, stack.DSNCipher, cfg.LarkAPIBaseURL)
		runner = runner.WithLark(larkProv)
	}

	// Watcher delivery (T-08) uses the same outbound providers as chat replies:
	// WhatsApp and Lark directly, Discord through the outbound bus cmd/discord
	// consumes. Installed on the very service the runner already holds as its
	// fire closer, so HandleFire and CompleteFire are one instance. larkProv is a
	// true nil interface when Lark is disabled, which deliver() treats as
	// "skipped" rather than dialling a nil client.
	stack.Watchers.WithDelivery(waProvider, larkProv, bus)

	// --- asynq.Server ---
	srv := asynq.NewServer(stack.AsynqOpt, asynq.Config{
		Concurrency: cfg.WorkerConcurrency,
		Queues:      cfg.WorkerQueueMap(),
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			logrus.WithError(err).WithField("task", t.Type()).Error("task failed")
		}),
		Logger: &logrusAsynqLogger{},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeChatRun, makeChatRunHandler(runner, reportSvc))
	mux.HandleFunc(queue.TypeScheduledTaskRun, makeScheduledRunHandler(stack.ScheduledSvc))
	mux.HandleFunc(queue.TypeReportRender, makeReportRenderHandler(reportSvc))
	mux.HandleFunc(queue.TypeWebhookDeliver, makeWebhookDeliverHandler(webhookDeliverer))
	mux.HandleFunc(queue.TypeBusinessInfer, makeBusinessInferHandler(stack.Inference))
	mux.HandleFunc(queue.TypeWatcherEval, makeWatcherEvalHandler(stack.Watchers))

	// --- Periodic task manager ---
	// Polls scheduled_tasks every SyncInterval and registers/refreshes one
	// asynq Scheduler entry per enabled row. Newly created tasks become
	// live within ~SyncInterval without a worker restart.
	pm, err := asynq.NewPeriodicTaskManager(asynq.PeriodicTaskManagerOpts{
		PeriodicTaskConfigProvider: queue.NewDBConfigProvider(stack.ScheduledRepo),
		RedisConnOpt:               stack.AsynqOpt,
		SyncInterval:               30 * time.Second,
	})
	if err != nil {
		logrus.Fatalf("periodic task manager: %v", err)
	}
	if err := pm.Start(); err != nil {
		logrus.Fatalf("periodic task manager start: %v", err)
	}
	defer pm.Shutdown()

	// --- Watcher periodic task manager (T-08) ---
	// A second manager over a second provider, not a second scheduler: the
	// ticket's exact shape. It polls enabled `watchers` rows and emits one
	// `watcher:eval` per cron tick. Gated by the kill switch — off means no
	// evaluation ticks fire at all, without touching any tenant's rows.
	if cfg.WatcherEnabled {
		wpm, err := asynq.NewPeriodicTaskManager(asynq.PeriodicTaskManagerOpts{
			PeriodicTaskConfigProvider: queue.NewWatcherConfigProvider(stack.WatcherRepo),
			RedisConnOpt:               stack.AsynqOpt,
			SyncInterval:               30 * time.Second,
		})
		if err != nil {
			logrus.Fatalf("watcher periodic task manager: %v", err)
		}
		if err := wpm.Start(); err != nil {
			logrus.Fatalf("watcher periodic task manager start: %v", err)
		}
		defer wpm.Shutdown()
		logrus.Info("watcher subsystem enabled")
	} else {
		logrus.Info("watcher subsystem disabled (WATCHER_ENABLED=false)")
	}

	// Run blocks until OS signal. Capture signals here so we can shut
	// the asynq server down gracefully (it will let in-flight tasks
	// finish before exiting).
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logrus.Info("Shutting down worker…")
		srv.Shutdown()
	}()

	if err := srv.Run(mux); err != nil {
		logrus.Fatalf("asynq server: %v", err)
	}
	logrus.Info("Bye")
}

// webhookMaxAttempts is the delivery budget, and it matches the MaxRetry the
// enqueuer sets. Two numbers that disagree would produce a row still marked
// pending after asynq has given up, or one marked failed with an attempt still
// queued behind it.
const webhookMaxAttempts = 5

// makeChatRunHandler adapts ChatRunner.Run to asynq's HandlerFunc signature.
// Returning a non-nil error triggers asynq's retry/backoff machinery.
//
// The report half is here rather than inside ChatRunner because only this
// layer knows whether asynq will try again. Marking a report failed on the
// first error would be wrong — Complete is one-way, so a retry that then
// succeeded could not undo it — and never marking it would leave a caller
// polling a job that has already exhausted its retries.
func makeChatRunHandler(runner *app.ChatRunner, reports *app.APIReportService) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.ChatRunPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			// Malformed payload: SkipRetry so the bad task is archived
			// instead of looping forever.
			return asynq.SkipRetry
		}
		err := runner.Run(ctx, p)
		if err != nil && p.APIReportID != "" && reports != nil {
			retried, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			if retried >= maxRetry {
				reports.CompleteReport(ctx, p.APIReportID, p.ThreadID, err)
			}
		}
		return err
	}
}

// makeReportRenderHandler runs a spec whose synchronous render overran its
// window. The service decides what is retryable; a returned error here is one
// it asked for.
func makeReportRenderHandler(reports *app.APIReportService) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.ReportRenderPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		if p.ReportID == "" {
			return asynq.SkipRetry
		}
		return reports.RunRenderJob(ctx, p)
	}
}

// makeWebhookDeliverHandler makes one delivery attempt. asynq owns the backoff
// between attempts; the deliverer decides whether there should be another.
func makeWebhookDeliverHandler(d *webhookout.Deliverer) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.WebhookDeliverPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		if p.DeliveryID == "" {
			return asynq.SkipRetry
		}
		return d.Deliver(ctx, p.DeliveryID)
	}
}

// makeBusinessInferHandler drafts what one connected source says the business
// is (T-B2).
//
// Two outcomes are deliberately not retried. A skip — the company's credits are
// exhausted — is the designed answer, not a failure: adding a data source must
// never fail because of a balance, and asynq retrying it three times would
// spend three log lines saying so. An unauthorized payload names a connection
// that is not the company's, which no amount of retrying will change.
//
// Everything else returns the error and takes asynq's backoff: a source that
// was unreachable for a minute is exactly the case a retry is for, and the
// connection stays perfectly usable in the meantime.
func makeBusinessInferHandler(svc *app.BusinessInferenceService) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.BusinessInferPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		if p.CompanyID == "" || p.ConnectionID == "" || svc == nil {
			return asynq.SkipRetry
		}
		// Force is the Re-scan button: re-introspect rather than read the
		// cached schema. Only that path sets it.
		infer := svc.InferSource
		if p.Force {
			infer = svc.RefreshSource
		}
		_, err := infer(ctx, p.CompanyID, p.ConnectionID)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, app.ErrInferenceSkipped), errors.Is(err, domain.ErrUnauthorized):
			return asynq.SkipRetry
		default:
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": p.CompanyID,
				"source_id":  p.ConnectionID,
			}).Warn("business inference failed; the connection is unaffected and the tenant can type their own profile")
			return err
		}
	}
}

// makeWatcherEvalHandler dispatches a periodic `watcher:eval` tick (T-08). The
// payload carries only the watcher id; the service reloads the row so a firing
// always uses the latest condition, evaluates the metric, and on an
// un-suppressed breach enqueues the chat:run that will explain it.
//
// A malformed or empty payload is archived rather than retried. Everything else
// returns the service's error and takes asynq's one retry — a transient DB blip
// between the tick and the query is exactly what that is for, and a watcher
// fires again on its next cron tick regardless.
func makeWatcherEvalHandler(svc *app.WatcherService) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.WatcherEvalPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		if p.WatcherID == "" || svc == nil {
			return asynq.SkipRetry
		}
		return svc.HandleFire(ctx, p.WatcherID)
	}
}

// makeScheduledRunHandler dispatches a periodic `scheduled:run` tick. The
// payload only carries a task ID; the service reloads the latest
// definition and enqueues a regular chat:run against the dedicated thread.
func makeScheduledRunHandler(svc *app.ScheduledTaskService) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.ScheduledRunPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return asynq.SkipRetry
		}
		if p.TaskID == "" {
			return asynq.SkipRetry
		}
		return svc.HandleFire(ctx, p.TaskID)
	}
}

// logrusAsynqLogger forwards asynq's internal log messages into the same
// logrus pipeline the rest of the worker uses.
type logrusAsynqLogger struct{}

func (l *logrusAsynqLogger) Debug(args ...interface{}) { logrus.Debug(args...) }
func (l *logrusAsynqLogger) Info(args ...interface{})  { logrus.Info(args...) }
func (l *logrusAsynqLogger) Warn(args ...interface{})  { logrus.Warn(args...) }
func (l *logrusAsynqLogger) Error(args ...interface{}) { logrus.Error(args...) }
func (l *logrusAsynqLogger) Fatal(args ...interface{}) { logrus.Fatal(args...) }
