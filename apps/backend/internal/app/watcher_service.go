// Package app — watchers (T-08), the wedge.
//
// A watcher evaluates a defined metric (T-06) on a cron and, when the number
// breaches a condition, fires a real agent turn to explain the move and
// delivers the explanation to WhatsApp, Discord, Lark, or the dashboard —
// unprompted. It is the shift from "answer when asked" to "tell you first".
//
// WatcherService owns every part of that: CRUD, the dry-run that must pass
// before a watcher can be enabled, the per-fire evaluation the worker's
// watcher:eval handler runs, and the multi-channel delivery of the turn's
// answer once it completes. It shares one evaluation path with query_metric
// (through MetricEvaluator), so a watcher fires off the same number the admin
// validated and a chat turn would report — never one the LLM re-derived.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "time/tzdata" // see scheduled_task_service.go: the deployed images ship no zoneinfo

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/lark"
	"github.com/fauzanebd/argentum/internal/metric"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/slack"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// watcherDryRunPeriods is how many trailing periods a dry-run evaluates. Wide
// enough to show a watcher's behaviour over recent history, narrow enough that
// same_period_last_year does not reach back past most demo data.
const watcherDryRunPeriods = 14

// enableRequiresDryRunWithin is the freshness a dry-run must have for a watcher
// to be enabled. The guard against the trust-destroying false alarm: nothing
// goes live until it has been shown, against real trailing data, how often it
// would have fired.
const enableRequiresDryRunWithin = 24 * time.Hour

// MetricEvaluator is the half of the metric registry a watcher reads (T-07):
// resolve a metric by id, and evaluate it over a window exactly as query_metric
// does. *MetricService satisfies it, which is what makes a watcher fire off the
// same number a chat turn would — the ticket's central property.
type MetricEvaluator interface {
	Get(ctx context.Context, companyID, id string) (*domain.MetricDefinition, error)
	Query(ctx context.Context, companyID, key string, from, to time.Time, compare metric.Comparison) (*metric.Result, error)
}

// WatcherThreads is the half of ThreadService a watcher needs: mint its
// dedicated thread, and append the breach briefing that becomes the turn's user
// message. Narrowed at the consumer like the runner's loaders, which also lets
// the fire path be exercised without a real thread stack. *ThreadService
// satisfies it.
type WatcherThreads interface {
	CreateDashboardThread(ctx context.Context, companyID, userID, firstMessage, agentID string) (*domain.ConversationThread, error)
	AppendUserMessage(ctx context.Context, threadID, content string) (*domain.Message, error)
}

// WatcherEnqueuer is the one queue call the fire path makes. *queue.Enqueuer
// satisfies it; declared here so HandleFire can be tested without Redis.
type WatcherEnqueuer interface {
	EnqueueChatRun(ctx context.Context, p queue.ChatRunPayload) (string, error)
}

// WatcherCompanies is the one company read the fire path makes: the name and
// currency the briefing turn is composed with. domain.CompanyRepository
// satisfies it.
type WatcherCompanies interface {
	GetByID(ctx context.Context, id string) (*domain.Company, error)
}

// WatcherService is the CRUD surface, the eval loop, and the delivery path for
// watchers.
type WatcherService struct {
	repo          domain.WatcherRepository
	metrics       MetricEvaluator
	threads       WatcherThreads
	companies     WatcherCompanies
	enqueuer      WatcherEnqueuer
	maxPerCompany int

	// Delivery providers, set only on the worker's instance (WithDelivery). The
	// API's instance serves CRUD and dry-runs and never delivers, so it leaves
	// these nil — a channel with no provider records "skipped" rather than
	// pretending it sent.
	wa    whatsapp.Provider
	lark  lark.Provider
	slack slack.Provider
	bus   EventBus

	// budget refuses an unattended fire on an exhausted tenant, the same second
	// integration point ScheduledTaskService needs and for the same reason: a
	// watcher:eval tick never passes through ChatEnqueuer.
	budget BudgetChecker

	// webhooks fans a breach out to the tenant's own subscribers (T-15). Nil on
	// the API's instance, which never fires, and on any deployment without the
	// subscription model — Publish is nil-safe either way.
	webhooks *WebhookSubscriptionService

	now func() time.Time
}

// NewWatcherService wires the CRUD/eval half. maxPerCompany caps how many
// watchers a tenant may define; <=0 falls back to a safe default.
func NewWatcherService(
	repo domain.WatcherRepository,
	metrics MetricEvaluator,
	threads WatcherThreads,
	companies WatcherCompanies,
	enqueuer WatcherEnqueuer,
	maxPerCompany int,
) *WatcherService {
	if maxPerCompany <= 0 {
		maxPerCompany = 20
	}
	return &WatcherService{
		repo:          repo,
		metrics:       metrics,
		threads:       threads,
		companies:     companies,
		enqueuer:      enqueuer,
		maxPerCompany: maxPerCompany,
		now:           time.Now,
	}
}

// WithDelivery installs the outbound providers the worker delivers a fire's
// answer through. The API constructs the service without them.
func (s *WatcherService) WithDelivery(wa whatsapp.Provider, larkProv lark.Provider, slackProv slack.Provider, bus EventBus) *WatcherService {
	s.wa = wa
	s.lark = larkProv
	s.slack = slackProv
	s.bus = bus
	return s
}

// WithBudget gates each fire on the tenant's credit balance.
func (s *WatcherService) WithBudget(b BudgetChecker) *WatcherService {
	s.budget = b
	return s
}

// WithWebhooks fans each breach out to the tenant's subscriptions (T-15).
// Optional: without it a breach behaves exactly as it did before, and Publish
// is nil-safe so nothing here has to branch.
func (s *WatcherService) WithWebhooks(w *WebhookSubscriptionService) *WatcherService {
	s.webhooks = w
	return s
}

// Repo exposes the repository for the periodic task manager's config provider.
func (s *WatcherService) Repo() domain.WatcherRepository { return s.repo }

// --- CRUD ---

// WatcherInput is the create/update shape shared by the handler.
type WatcherInput struct {
	MetricID        string                   `json:"metric_id"`
	Name            string                   `json:"name"`
	WindowGrain     domain.WatcherGrain      `json:"window_grain"`
	Comparator      domain.WatcherComparator `json:"comparator"`
	Threshold       float64                  `json:"threshold"`
	CompareTo       string                   `json:"compare_to"`
	CronExpression  string                   `json:"cron_expression"`
	Timezone        string                   `json:"timezone"`
	Channels        []domain.WatcherChannel  `json:"channels"`
	CooldownMinutes *int                     `json:"cooldown_minutes"`
	// Enabled is honoured only on Update, and only with a fresh dry-run. Create
	// always makes a silent watcher — there is no dry-run to vouch for it yet.
	Enabled *bool `json:"enabled"`
}

// Create validates the input, mints a dedicated thread, and stores a disabled
// watcher. Disabled by construction: a watcher cannot be enabled until a
// dry-run has passed, and none has at creation.
func (s *WatcherService) Create(ctx context.Context, companyID, createdBy string, in WatcherInput) (*domain.Watcher, error) {
	w, err := s.validated(ctx, companyID, in, nil)
	if err != nil {
		return nil, err
	}
	if createdBy == "" {
		return nil, fmt.Errorf("%w: a watcher must be created by a dashboard user", domain.ErrInvalidInput)
	}
	n, err := s.repo.CountByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if n >= s.maxPerCompany {
		return nil, fmt.Errorf("%w: this workspace already has the maximum of %d watchers", domain.ErrConflict, s.maxPerCompany)
	}
	thread, err := s.threads.CreateDashboardThread(ctx, companyID, createdBy, "Watcher: "+w.Name, "")
	if err != nil {
		return nil, fmt.Errorf("create dedicated thread: %w", err)
	}
	w.ThreadID = thread.ID
	w.CreatedBy = createdBy
	w.Enabled = false
	if err := s.repo.Create(ctx, w); err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "watcher_id": w.ID, "metric_id": w.MetricID,
	}).Info("watcher created")
	return w, nil
}

// Get returns one watcher, 404 for another company's.
func (s *WatcherService) Get(ctx context.Context, companyID, id string) (*domain.Watcher, error) {
	return s.repo.GetByID(ctx, companyID, id)
}

// List returns the company's watchers.
func (s *WatcherService) List(ctx context.Context, companyID string) ([]*domain.Watcher, error) {
	return s.repo.ListByCompany(ctx, companyID)
}

// ListEvents returns a watcher's recent evaluations. firedOnly narrows to the
// ones that delivered — see the repository's own note for why that is a query
// and not something the caller can filter after the fact.
func (s *WatcherService) ListEvents(ctx context.Context, companyID, watcherID string, limit, offset int, firedOnly bool) ([]*domain.WatcherEvent, error) {
	if _, err := s.repo.GetByID(ctx, companyID, watcherID); err != nil {
		return nil, err
	}
	return s.repo.ListEventsByWatcher(ctx, companyID, watcherID, limit, offset, firedOnly)
}

// Update mutates a watcher. Enabling is gated on a recent dry-run; a change to
// the condition itself clears the dry-run, because a dry-run over the old
// condition says nothing about the new one.
func (s *WatcherService) Update(ctx context.Context, companyID, id string, in WatcherInput) (*domain.Watcher, error) {
	current, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	w, err := s.validated(ctx, companyID, in, current)
	if err != nil {
		return nil, err
	}
	w.ID = current.ID
	w.ThreadID = current.ThreadID
	w.CreatedBy = current.CreatedBy
	w.CreatedAt = current.CreatedAt
	w.LastFiredAt = current.LastFiredAt
	w.LastDryRunAt = current.LastDryRunAt

	// A condition change invalidates the standing dry-run.
	if conditionChanged(current, w) {
		w.LastDryRunAt = nil
	}

	// Enable only with a dry-run fresh enough to vouch for the current condition.
	enable := false
	if in.Enabled != nil {
		enable = *in.Enabled
	} else {
		enable = current.Enabled
	}
	if enable && !current.Enabled || (enable && conditionChanged(current, w)) {
		if w.LastDryRunAt == nil || s.now().Sub(*w.LastDryRunAt) > enableRequiresDryRunWithin {
			return nil, fmt.Errorf("%w: run a dry-run within the last 24h before enabling this watcher", domain.ErrInvalidInput)
		}
	}
	w.Enabled = enable

	if err := s.repo.Update(ctx, w); err != nil {
		return nil, err
	}
	return w, nil
}

// Delete removes a watcher (and, by cascade, its events).
func (s *WatcherService) Delete(ctx context.Context, companyID, id string) error {
	if _, err := s.repo.GetByID(ctx, companyID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, companyID, id)
}

// validated turns input into a watcher, checked structurally and against the
// metric it names. current is nil on create, the stored row on update.
func (s *WatcherService) validated(ctx context.Context, companyID string, in WatcherInput, current *domain.Watcher) (*domain.Watcher, error) {
	name := strings.TrimSpace(in.Name)
	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case name == "":
		return nil, fmt.Errorf("%w: a watcher needs a name", domain.ErrInvalidInput)
	case in.MetricID == "":
		return nil, fmt.Errorf("%w: a watcher needs a metric", domain.ErrInvalidInput)
	case !in.WindowGrain.Valid():
		return nil, fmt.Errorf("%w: window_grain must be one of day, week, month", domain.ErrInvalidInput)
	case !in.Comparator.Valid():
		return nil, fmt.Errorf("%w: comparator must be one of gt, lt, pct_change_gt, pct_change_lt, no_data", domain.ErrInvalidInput)
	}
	if in.Comparator.NeedsComparison() {
		if c := metric.Comparison(strings.TrimSpace(in.CompareTo)); !c.Valid() {
			return nil, fmt.Errorf("%w: %s needs compare_to set to previous_period or same_period_last_year", domain.ErrInvalidInput, in.Comparator)
		}
	}
	// The metric must exist and belong to the company — a watcher on a stranger's
	// number is a cross-tenant read waiting to happen.
	if _, err := s.metrics.Get(ctx, companyID, in.MetricID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: no metric with that id in this workspace", domain.ErrInvalidInput)
		}
		return nil, err
	}
	tz, err := normalizeTimezone(in.Timezone)
	if err != nil {
		return nil, err
	}
	if err := validateCron(in.CronExpression, tz); err != nil {
		return nil, err
	}
	if err := validateChannels(in.Channels); err != nil {
		return nil, err
	}

	cooldown := 720
	if in.CooldownMinutes != nil {
		if *in.CooldownMinutes < 0 {
			return nil, fmt.Errorf("%w: cooldown_minutes cannot be negative", domain.ErrInvalidInput)
		}
		cooldown = *in.CooldownMinutes
	} else if current != nil {
		cooldown = current.CooldownMinutes
	}

	compareTo := ""
	if in.Comparator.NeedsComparison() {
		compareTo = strings.TrimSpace(in.CompareTo)
	}

	return &domain.Watcher{
		CompanyID:       companyID,
		MetricID:        in.MetricID,
		Name:            name,
		WindowGrain:     in.WindowGrain,
		Comparator:      in.Comparator,
		Threshold:       in.Threshold,
		CompareTo:       compareTo,
		CronExpression:  strings.TrimSpace(in.CronExpression),
		Timezone:        tz,
		Channels:        in.Channels,
		CooldownMinutes: cooldown,
	}, nil
}

// conditionChanged reports whether an edit changed what the watcher watches, as
// opposed to its name or delivery. A changed condition means the standing
// dry-run no longer describes it.
func conditionChanged(a, b *domain.Watcher) bool {
	return a.MetricID != b.MetricID ||
		a.WindowGrain != b.WindowGrain ||
		a.Comparator != b.Comparator ||
		a.Threshold != b.Threshold ||
		a.CompareTo != b.CompareTo
}

func validateChannels(channels []domain.WatcherChannel) error {
	if len(channels) == 0 {
		return fmt.Errorf("%w: a watcher needs at least one delivery channel", domain.ErrInvalidInput)
	}
	for _, ch := range channels {
		switch ch.Channel {
		case domain.ChannelDashboard:
			// No ref: the dedicated thread is the destination.
		case domain.ChannelWhatsApp, domain.ChannelDiscord, domain.ChannelLark, domain.ChannelSlack:
			if strings.TrimSpace(ch.Ref) == "" {
				return fmt.Errorf("%w: the %s channel needs a ref (phone, channel id, or chat id)", domain.ErrInvalidInput, ch.Channel)
			}
		default:
			return fmt.Errorf("%w: %q is not a channel a watcher can deliver to", domain.ErrInvalidInput, ch.Channel)
		}
	}
	return nil
}

// --- evaluation ---

// breachResult is one window's evaluation.
type breachResult struct {
	breached                               bool
	metricValue, comparisonValue, deltaPct *float64
	// noData is true when the metric produced no usable value this window.
	noData bool
}

// evaluate runs the metric over one window and applies the comparator. It
// assumes the metric is enabled (HandleFire and DryRun check that first), so an
// ErrInvalidInput from Query means the SQL returned no usable row — which is a
// no_data breach for the no_data comparator and a can't-evaluate for the rest.
func (s *WatcherService) evaluate(ctx context.Context, w *domain.Watcher, m *domain.MetricDefinition, window metric.Window) (breachResult, error) {
	var compare metric.Comparison
	if w.Comparator.NeedsComparison() {
		compare = metric.Comparison(w.CompareTo)
	}
	res, err := s.metrics.Query(ctx, w.CompanyID, m.Key, window.From, window.To, compare)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidInput) {
			// No usable value this window.
			return breachResult{noData: true, breached: w.Comparator == domain.WatcherComparatorNoData}, nil
		}
		return breachResult{}, err
	}
	// A NULL value used to arrive here as ErrInvalidInput and take the branch
	// above. It is now a successful evaluation carrying Empty (T-Q9's metric
	// half), so this is the same decision moved to where the fact now lives —
	// and it has to be made, because Value is 0 on an empty evaluation: a
	// `lt` watcher would breach on every period the warehouse holds no data
	// for, and `no_data` would stop breaching on exactly the ones it is for.
	if res.Primary.Empty {
		return breachResult{noData: true, breached: w.Comparator == domain.WatcherComparatorNoData}, nil
	}

	br := breachResult{}
	v := res.Primary.Value
	br.metricValue = &v
	// Left unset when the comparison window matched nothing: the briefing renders
	// this figure, and 0 there is a number the period does not have. The
	// percentage comparators are already safe — Query leaves DeltaPct nil on an
	// empty side, and both branches below require it.
	if res.Comparison != nil && !res.Comparison.Empty {
		cv := res.Comparison.Value
		br.comparisonValue = &cv
	}
	if res.DeltaPct != nil {
		dp := *res.DeltaPct
		br.deltaPct = &dp
	}
	switch w.Comparator {
	case domain.WatcherComparatorGT:
		br.breached = v > w.Threshold
	case domain.WatcherComparatorLT:
		br.breached = v < w.Threshold
	case domain.WatcherComparatorPctChangeGT:
		br.breached = res.DeltaPct != nil && *res.DeltaPct > w.Threshold
	case domain.WatcherComparatorPctChangeLT:
		br.breached = res.DeltaPct != nil && *res.DeltaPct < w.Threshold
	case domain.WatcherComparatorNoData:
		br.breached = false // data present is exactly what no_data does not want
	}
	return br, nil
}

// DryRunResult is what the dashboard's Dry-run button shows.
type DryRunResult struct {
	PeriodsEvaluated int            `json:"periods_evaluated"`
	WouldHaveFired   int            `json:"would_have_fired"`
	Samples          []DryRunSample `json:"samples"`
}

// DryRunSample is one trailing period's outcome.
type DryRunSample struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Value    *float64  `json:"value,omitempty"`
	DeltaPct *float64  `json:"delta_pct,omitempty"`
	Breached bool      `json:"breached"`
	NoData   bool      `json:"no_data"`
}

// DryRun evaluates the watcher over the last N complete periods and reports how
// many times it would have fired, then records the dry-run so the watcher can be
// enabled. It does not deliver anything and does not spend an LLM call — it is
// the same SQL the fire path runs, over history.
func (s *WatcherService) DryRun(ctx context.Context, companyID, id string) (*DryRunResult, error) {
	w, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	m, err := s.metrics.Get(ctx, companyID, w.MetricID)
	if err != nil {
		return nil, err
	}
	if !m.Enabled {
		return nil, fmt.Errorf("%w: the metric this watcher uses is disabled — enable it first", domain.ErrInvalidInput)
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid timezone %q", domain.ErrInvalidInput, w.Timezone)
	}

	periods := trailingPeriods(w.WindowGrain, loc, s.now(), watcherDryRunPeriods)
	out := &DryRunResult{Samples: make([]DryRunSample, 0, len(periods))}
	for _, p := range periods {
		br, err := s.evaluate(ctx, w, m, p)
		if err != nil {
			return nil, fmt.Errorf("dry-run evaluation: %w", err)
		}
		out.PeriodsEvaluated++
		if br.breached {
			out.WouldHaveFired++
		}
		out.Samples = append(out.Samples, DryRunSample{
			From: p.From, To: p.To, Value: br.metricValue, DeltaPct: br.deltaPct,
			Breached: br.breached, NoData: br.noData,
		})
	}

	if err := s.repo.TouchDryRun(ctx, w.ID, s.now()); err != nil {
		logrus.WithError(err).WithField("watcher_id", w.ID).Warn("watcher dry-run recorded no timestamp")
	}
	return out, nil
}

// --- the fire path (worker) ---

// HandleFire is invoked by the worker's watcher:eval handler. It evaluates the
// watcher's metric over the current complete period, records an event, and — on
// an un-suppressed breach — appends a briefing to the dedicated thread and
// enqueues the agent turn that will explain it.
func (s *WatcherService) HandleFire(ctx context.Context, watcherID string) error {
	w, err := s.repo.GetForFire(ctx, watcherID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // deleted since the tick was scheduled; nothing to do
		}
		return err
	}
	if !w.Enabled {
		return nil
	}
	m, err := s.metrics.Get(ctx, w.CompanyID, w.MetricID)
	if err != nil {
		// The metric FK cascades on delete, so a missing metric is a race, not a
		// steady state. Either way retrying will not conjure it.
		logrus.WithError(err).WithFields(logrus.Fields{
			"watcher_id": w.ID, "metric_id": w.MetricID,
		}).Warn("watcher fire: metric lookup failed; skipping this tick")
		return nil
	}
	if !m.Enabled {
		logrus.WithFields(logrus.Fields{
			"watcher_id": w.ID, "metric_id": w.MetricID,
		}).Info("watcher fire: metric is disabled; skipping this tick")
		return nil
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return fmt.Errorf("watcher %s: invalid timezone %q: %w", w.ID, w.Timezone, err)
	}

	window := completePeriod(w.WindowGrain, loc, s.now())
	br, err := s.evaluate(ctx, w, m, window)
	if err != nil {
		return err // a real error (connection down); asynq retries
	}

	event := &domain.WatcherEvent{
		WatcherID:       w.ID,
		CompanyID:       w.CompanyID,
		MetricValue:     br.metricValue,
		ComparisonValue: br.comparisonValue,
		DeltaPct:        br.deltaPct,
		Breached:        br.breached,
	}

	if !br.breached {
		if err := s.repo.AppendEvent(ctx, event); err != nil {
			return fmt.Errorf("record watcher event: %w", err)
		}
		metrics.Default().RecordWatcherFire("quiet")
		return nil
	}

	// Breached — is it inside the cooldown?
	if s.inCooldown(w) {
		event.SuppressedReason = "cooldown"
		if err := s.repo.AppendEvent(ctx, event); err != nil {
			return fmt.Errorf("record suppressed watcher event: %w", err)
		}
		// Counted apart from a fire (T-17). A watcher firing constantly and a
		// watcher suppressing constantly are different problems, and the events
		// sheet had to be fixed to tell them apart for the same reason.
		metrics.Default().RecordWatcherFire("suppressed")
		return nil
	}

	// Breached and firing — refuse an unattended fire on an exhausted tenant.
	if s.budget != nil {
		st, err := s.budget.CheckBudget(ctx, w.CompanyID)
		if err != nil {
			logrus.WithError(err).WithField("company_id", w.CompanyID).
				Warn("watcher fire: budget check failed; firing anyway")
		} else if st.Blocked() {
			event.SuppressedReason = "credits_exhausted"
			if err := s.repo.AppendEvent(ctx, event); err != nil {
				return fmt.Errorf("record refused watcher event: %w", err)
			}
			metrics.Default().RecordWatcherFire("credits_exhausted")
			return nil
		}
	}

	tid := w.ThreadID
	event.ThreadID = &tid
	if err := s.repo.AppendEvent(ctx, event); err != nil {
		return fmt.Errorf("record watcher event: %w", err)
	}
	// Mark fired before the turn, so the cooldown covers the fire even if the
	// enqueue below fails — a watcher that failed to deliver should still wait
	// its cooldown before trying again, or a broken channel becomes a fire storm.
	if err := s.repo.TouchFired(ctx, w.ID, s.now()); err != nil {
		logrus.WithError(err).WithField("watcher_id", w.ID).Warn("watcher fire: cooldown timestamp not recorded")
	}

	briefing := watcherBriefing(w, m, br, window)
	userMsg, err := s.threads.AppendUserMessage(ctx, w.ThreadID, briefing)
	if err != nil {
		logrus.WithError(err).WithField("watcher_id", w.ID).Warn("watcher fire: briefing not appended; event recorded without a turn")
		return nil
	}

	var companyName, currency string
	if c, err := s.companies.GetByID(ctx, w.CompanyID); err == nil {
		companyName = c.Name
		currency = c.DefaultCurrency
	}

	if _, err := s.enqueuer.EnqueueChatRun(ctx, queue.ChatRunPayload{
		CompanyID:       w.CompanyID,
		ThreadID:        w.ThreadID,
		Channel:         domain.ChannelDashboard,
		Message:         briefing,
		UserMsgID:       userMsg.ID,
		CompanyName:     companyName,
		DefaultCurrency: currency,
		WatcherEventID:  event.ID,
	}); err != nil {
		logrus.WithError(err).WithField("watcher_id", w.ID).Warn("watcher fire: chat:run not enqueued; event recorded without a turn")
		return nil
	}
	logrus.WithFields(logrus.Fields{
		"watcher_id": w.ID, "event_id": event.ID, "company_id": w.CompanyID,
	}).Info("watcher breached; agent turn enqueued")
	metrics.Default().RecordWatcherFire("breached")

	// After the turn is enqueued, never before: the webhook says a breach
	// happened, and the thing that makes it true is the event row plus the turn
	// that will explain it. A subscriber told about a breach we then failed to
	// act on would be told something we did not do.
	s.webhooks.Publish(ctx, w.CompanyID, domain.WebhookWatcherBreached,
		newWatcherBreachedPayload(w, event, s.now()))
	return nil
}

func (s *WatcherService) inCooldown(w *domain.Watcher) bool {
	if w.LastFiredAt == nil || w.CooldownMinutes <= 0 {
		return false
	}
	return s.now().Sub(*w.LastFiredAt) < time.Duration(w.CooldownMinutes)*time.Minute
}

// CompleteFire is called by ChatRunner when a watcher's briefing turn finishes
// (T-08). It records the assistant message on the event and delivers the answer
// to every configured channel. Runs on the worker, where the outbound providers
// live; the API's instance never sets them and never calls this.
func (s *WatcherService) CompleteFire(ctx context.Context, eventID, assistantMsgID, response string) {
	if s == nil || eventID == "" {
		return
	}
	// Detached, like the other completion writes: the turn this describes is over,
	// and a delivery to a slow channel must not be cancelled by the request
	// context unwinding.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	event, err := s.repo.GetEvent(writeCtx, eventID)
	if err != nil {
		logrus.WithError(err).WithField("event_id", eventID).Warn("watcher completion: event lookup failed")
		return
	}
	w, err := s.repo.GetForFire(writeCtx, event.WatcherID)
	if err != nil {
		logrus.WithError(err).WithField("watcher_id", event.WatcherID).Warn("watcher completion: watcher lookup failed")
		return
	}
	deliveries := s.deliver(writeCtx, w, response)
	if err := s.repo.SetEventDelivery(writeCtx, eventID, assistantMsgID, deliveries); err != nil {
		logrus.WithError(err).WithField("event_id", eventID).Warn("watcher completion: delivery status not recorded")
	}
}

// deliver pushes the turn's answer to each configured channel and returns the
// per-channel outcome. It is proactive delivery — there is no inbound message to
// reply to — which is why Lark and Slack need Send rather than Reply and the
// dashboard case is a no-op (the answer is already in the dedicated thread).
func (s *WatcherService) deliver(ctx context.Context, w *domain.Watcher, response string) []domain.WatcherDelivery {
	out := make([]domain.WatcherDelivery, 0, len(w.Channels))
	for _, ch := range w.Channels {
		d := domain.WatcherDelivery{Channel: ch.Channel, Ref: ch.Ref, Status: "delivered"}
		switch ch.Channel {
		case domain.ChannelDashboard:
			// Already delivered: the assistant message is in the thread and the
			// `final` event was published to it. Nothing to send.
		case domain.ChannelWhatsApp:
			if s.wa == nil || ch.Ref == "" {
				d.Status, d.Error = "skipped", "no whatsapp provider"
			} else if err := s.wa.SendMessage(ch.Ref, stripMarkdownLinks(response)); err != nil {
				d.Status, d.Error = "failed", err.Error()
			}
		case domain.ChannelDiscord:
			if s.bus == nil || ch.Ref == "" {
				d.Status, d.Error = "skipped", "no discord bus"
			} else if err := s.bus.PublishOutbound(OutboundEvent{
				Channel: string(domain.ChannelDiscord), CompanyID: w.CompanyID,
				ChannelRef: ch.Ref, Content: response,
			}); err != nil {
				d.Status, d.Error = "failed", err.Error()
			}
		case domain.ChannelLark:
			if s.lark == nil || ch.Ref == "" {
				d.Status, d.Error = "skipped", "no lark provider"
			} else if err := s.lark.Send(ctx, w.CompanyID, ch.Ref, response); err != nil {
				d.Status, d.Error = "failed", err.Error()
			}
		case domain.ChannelSlack:
			if s.slack == nil || ch.Ref == "" {
				d.Status, d.Error = "skipped", "no slack provider"
			} else if err := s.slack.Send(ctx, w.CompanyID, ch.Ref, response); err != nil {
				d.Status, d.Error = "failed", err.Error()
			}
		default:
			d.Status, d.Error = "skipped", "unknown channel"
		}
		if d.Status == "failed" {
			logrus.WithFields(logrus.Fields{
				"watcher_id": w.ID, "channel": ch.Channel, "error": d.Error,
			}).Warn("watcher delivery failed")
		}
		out = append(out, d)
	}
	return out
}

// watcherBriefing is the user-turn text a breach enqueues. It states the metric,
// the window, the value and the breach, then asks for a short, specific
// explanation — the ≤120-word driver analysis the ticket calls for. It is
// ordinary analytical text, not an instruction override, so it rides in the user
// message like a scheduled task's prompt rather than the T-A2b directive channel.
func watcherBriefing(w *domain.Watcher, m *domain.MetricDefinition, br breachResult, window metric.Window) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Watcher alert: %s]\n\n", w.Name)
	fmt.Fprintf(&b, "The metric \"%s\" (%s) for the period %s to %s has breached its condition.\n",
		m.Label, m.Key,
		window.From.Format("2006-01-02"), window.To.Format("2006-01-02"))
	if br.metricValue != nil {
		fmt.Fprintf(&b, "- Current value: %.2f\n", *br.metricValue)
	}
	if br.comparisonValue != nil {
		fmt.Fprintf(&b, "- Comparison value: %.2f\n", *br.comparisonValue)
	}
	if br.deltaPct != nil {
		fmt.Fprintf(&b, "- Change: %.1f%%\n", *br.deltaPct)
	}
	fmt.Fprintf(&b, "- Condition: %s %.2f", w.Comparator, w.Threshold)
	if w.CompareTo != "" {
		fmt.Fprintf(&b, " (vs %s)", w.CompareTo)
	}
	b.WriteString("\n\n")
	b.WriteString("Explain the likely drivers of this move in 120 words or fewer, and name what to check next. ")
	b.WriteString("Use the data tools to dig into the drivers if you need to; do not restate the numbers above without adding insight.")
	return b.String()
}

// --- window arithmetic ---
//
// A watcher evaluates the most recent *complete* period of its grain, in its own
// timezone. Complete rather than to-date so the number is stable within a period
// and a comparison is a whole period against a whole period; the cron cadence
// decides how often that stable number is checked, and the cooldown keeps a
// standing breach from firing on every tick.

func trailingPeriods(grain domain.WatcherGrain, loc *time.Location, now time.Time, n int) []metric.Window {
	base := completePeriod(grain, loc, now)
	out := make([]metric.Window, 0, n)
	for k := 0; k < n; k++ {
		out = append(out, shiftPeriod(base, grain, k))
	}
	return out
}

// completePeriod is the most recent whole period ending at or before now.
func completePeriod(grain domain.WatcherGrain, loc *time.Location, now time.Time) metric.Window {
	switch grain {
	case domain.WatcherGrainWeek:
		end := startOfWeek(now, loc)
		return metric.Window{From: end.AddDate(0, 0, -7), To: end}
	case domain.WatcherGrainMonth:
		end := startOfMonth(now, loc)
		return metric.Window{From: end.AddDate(0, -1, 0), To: end}
	default: // day
		end := startOfDay(now, loc)
		return metric.Window{From: end.AddDate(0, 0, -1), To: end}
	}
}

// shiftPeriod steps a window back by k whole periods of the grain.
func shiftPeriod(w metric.Window, grain domain.WatcherGrain, k int) metric.Window {
	switch grain {
	case domain.WatcherGrainWeek:
		return metric.Window{From: w.From.AddDate(0, 0, -7*k), To: w.To.AddDate(0, 0, -7*k)}
	case domain.WatcherGrainMonth:
		return metric.Window{From: w.From.AddDate(0, -k, 0), To: w.To.AddDate(0, -k, 0)}
	default:
		return metric.Window{From: w.From.AddDate(0, 0, -k), To: w.To.AddDate(0, 0, -k)}
	}
}

func startOfDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// startOfWeek returns midnight on the Monday of t's week.
func startOfWeek(t time.Time, loc *time.Location) time.Time {
	d := startOfDay(t, loc)
	offset := (int(d.Weekday()) + 6) % 7 // days since Monday; Sunday(0)->6
	return d.AddDate(0, 0, -offset)
}

func startOfMonth(t time.Time, loc *time.Location) time.Time {
	y, m, _ := t.In(loc).Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, loc)
}
