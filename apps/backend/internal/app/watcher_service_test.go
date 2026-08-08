package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
	"github.com/fauzanebd/argentum/internal/queue"
	pkgmodels "github.com/fauzanebd/argentum/pkg/models"
)

// --- fakes ---

type fakeWatcherRepo struct {
	watchers map[string]*domain.Watcher
	events   []*domain.WatcherEvent
	fired    map[string]time.Time
	dryRuns  map[string]time.Time
	count    int
}

func newFakeWatcherRepo() *fakeWatcherRepo {
	return &fakeWatcherRepo{
		watchers: map[string]*domain.Watcher{},
		fired:    map[string]time.Time{},
		dryRuns:  map[string]time.Time{},
	}
}

func (r *fakeWatcherRepo) Create(_ context.Context, w *domain.Watcher) error {
	if w.ID == "" {
		w.ID = "w-created"
	}
	cp := *w
	r.watchers[w.ID] = &cp
	r.count++
	return nil
}
func (r *fakeWatcherRepo) GetByID(_ context.Context, companyID, id string) (*domain.Watcher, error) {
	w, ok := r.watchers[id]
	if !ok || w.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	cp := *w
	return &cp, nil
}
func (r *fakeWatcherRepo) GetForFire(_ context.Context, id string) (*domain.Watcher, error) {
	w, ok := r.watchers[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *w
	return &cp, nil
}
func (r *fakeWatcherRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.Watcher, error) {
	var out []*domain.Watcher
	for _, w := range r.watchers {
		if w.CompanyID == companyID {
			out = append(out, w)
		}
	}
	return out, nil
}
func (r *fakeWatcherRepo) CountByCompany(_ context.Context, _ string) (int, error) {
	return r.count, nil
}
func (r *fakeWatcherRepo) Update(_ context.Context, w *domain.Watcher) error {
	if _, ok := r.watchers[w.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *w
	r.watchers[w.ID] = &cp
	return nil
}
func (r *fakeWatcherRepo) Delete(_ context.Context, companyID, id string) error {
	w, ok := r.watchers[id]
	if !ok || w.CompanyID != companyID {
		return domain.ErrNotFound
	}
	delete(r.watchers, id)
	return nil
}
func (r *fakeWatcherRepo) TouchFired(_ context.Context, id string, at time.Time) error {
	r.fired[id] = at
	if w, ok := r.watchers[id]; ok {
		w.LastFiredAt = &at
	}
	return nil
}
func (r *fakeWatcherRepo) TouchDryRun(_ context.Context, id string, at time.Time) error {
	r.dryRuns[id] = at
	return nil
}
func (r *fakeWatcherRepo) ListEnabledForScheduler(_ context.Context) ([]*domain.Watcher, error) {
	var out []*domain.Watcher
	for _, w := range r.watchers {
		if w.Enabled {
			out = append(out, w)
		}
	}
	return out, nil
}
func (r *fakeWatcherRepo) AppendEvent(_ context.Context, e *domain.WatcherEvent) error {
	if e.ID == "" {
		e.ID = "ev-1"
	}
	cp := *e
	r.events = append(r.events, &cp)
	return nil
}
func (r *fakeWatcherRepo) SetEventDelivery(_ context.Context, eventID, messageID string, d []domain.WatcherDelivery) error {
	for _, e := range r.events {
		if e.ID == eventID {
			e.MessageID = &messageID
			e.DeliveryStatus = d
		}
	}
	return nil
}
func (r *fakeWatcherRepo) GetEvent(_ context.Context, id string) (*domain.WatcherEvent, error) {
	for _, e := range r.events {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *fakeWatcherRepo) ListEventsByWatcher(_ context.Context, _, watcherID string, _, _ int, firedOnly bool) ([]*domain.WatcherEvent, error) {
	var out []*domain.WatcherEvent
	for _, e := range r.events {
		if e.WatcherID != watcherID {
			continue
		}
		if firedOnly && (!e.Breached || e.SuppressedReason != "") {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (r *fakeWatcherRepo) lastEvent() *domain.WatcherEvent {
	if len(r.events) == 0 {
		return nil
	}
	return r.events[len(r.events)-1]
}

// fakeMetricEval returns a metric def and a queryFn-driven result.
type fakeMetricEval struct {
	def     *domain.MetricDefinition
	queryFn func(from, to time.Time, compare metric.Comparison) (*metric.Result, error)
}

func (m *fakeMetricEval) Get(_ context.Context, companyID, id string) (*domain.MetricDefinition, error) {
	if m.def == nil || m.def.ID != id || m.def.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return m.def, nil
}
func (m *fakeMetricEval) Query(_ context.Context, _, _ string, from, to time.Time, compare metric.Comparison) (*metric.Result, error) {
	return m.queryFn(from, to, compare)
}

type fakeThreads struct {
	appended []string
}

func (t *fakeThreads) CreateDashboardThread(_ context.Context, _, _, _, _ string) (*domain.ConversationThread, error) {
	return &domain.ConversationThread{ID: "thread-1"}, nil
}
func (t *fakeThreads) AppendUserMessage(_ context.Context, _, content string) (*domain.Message, error) {
	t.appended = append(t.appended, content)
	return &domain.Message{ID: "msg-user-1"}, nil
}

type fakeCompanies struct{}

func (fakeCompanies) GetByID(_ context.Context, id string) (*domain.Company, error) {
	return &domain.Company{ID: id, Name: "Acme", DefaultCurrency: "IDR"}, nil
}

type fakeEnqueuer struct {
	last  *queue.ChatRunPayload
	calls int
}

func (e *fakeEnqueuer) EnqueueChatRun(_ context.Context, p queue.ChatRunPayload) (string, error) {
	e.calls++
	cp := p
	e.last = &cp
	return "task-1", nil
}

// fakeBudget is shared with business_inference_test.go: a struct with a
// `verdict` field.

// delivery fakes
type fakeWA struct{ sent []string }

func (w *fakeWA) SendMessage(_, message string) error                 { w.sent = append(w.sent, message); return nil }
func (w *fakeWA) SendResponse(string, *pkgmodels.AgentResponse) error { return nil }
func (w *fakeWA) ParseWebhook([]byte) (*pkgmodels.Message, error)     { return nil, nil }
func (w *fakeWA) VerifyWebhook([]byte, string, string) bool           { return true }
func (w *fakeWA) VerifyToken(string, string) bool                     { return true }

type fakeLark struct{ chats []string }

func (l *fakeLark) Reply(context.Context, string, string, string) error { return nil }
func (l *fakeLark) Send(_ context.Context, _, chatID, _ string) error {
	l.chats = append(l.chats, chatID)
	return nil
}

type fakeSlack struct {
	channels []string
	// threadTS records what Reply was given, so a test can prove watcher
	// delivery starts its own thread rather than replying into one.
	threadTS []string
}

func (s *fakeSlack) Reply(_ context.Context, _, channelID, threadTS, _ string) error {
	s.channels = append(s.channels, channelID)
	s.threadTS = append(s.threadTS, threadTS)
	return nil
}
func (s *fakeSlack) Send(ctx context.Context, companyID, channelID, content string) error {
	return s.Reply(ctx, companyID, channelID, "", content)
}

type fakeBus struct{ outbound []OutboundEvent }

func (b *fakeBus) Publish(string, ChatEvent) error { return nil }
func (b *fakeBus) PublishOutbound(e OutboundEvent) error {
	b.outbound = append(b.outbound, e)
	return nil
}

// --- helpers ---

func fixedNow(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05Z07:00", s)
	if err != nil {
		panic(err)
	}
	return t
}

func testMetricDef() *domain.MetricDefinition {
	return &domain.MetricDefinition{
		ID: "metric-1", CompanyID: "co-1", Key: "revenue", Label: "Revenue",
		Grain: domain.MetricGrainMonth, Unit: domain.MetricUnitCurrency, Enabled: true,
	}
}

func valueResult(v float64) *metric.Result {
	return &metric.Result{Primary: metric.Evaluation{Value: v}}
}

func deltaResult(v, cmp, deltaPct float64) *metric.Result {
	return &metric.Result{
		Primary:    metric.Evaluation{Value: v},
		Comparison: &metric.Evaluation{Value: cmp},
		DeltaPct:   &deltaPct,
	}
}

// --- window arithmetic ---

func TestCompletePeriod(t *testing.T) {
	loc := time.UTC
	now := fixedNow("2026-03-15T13:37:00Z") // a Sunday
	cases := []struct {
		grain    domain.WatcherGrain
		wantFrom string
		wantTo   string
	}{
		// Most recent complete day is the 14th.
		{domain.WatcherGrainDay, "2026-03-14", "2026-03-15"},
		// 2026-03-15 is a Sunday; the current week starts Mon 2026-03-09, so the
		// last complete week is Mon 2026-03-02 .. Mon 2026-03-09.
		{domain.WatcherGrainWeek, "2026-03-02", "2026-03-09"},
		// Last complete month is February.
		{domain.WatcherGrainMonth, "2026-02-01", "2026-03-01"},
	}
	for _, c := range cases {
		w := completePeriod(c.grain, loc, now)
		if got := w.From.Format("2006-01-02"); got != c.wantFrom {
			t.Errorf("%s: from = %s, want %s", c.grain, got, c.wantFrom)
		}
		if got := w.To.Format("2006-01-02"); got != c.wantTo {
			t.Errorf("%s: to = %s, want %s", c.grain, got, c.wantTo)
		}
	}
}

func TestTrailingPeriodsAbutAndStepBack(t *testing.T) {
	loc := time.UTC
	now := fixedNow("2026-03-15T00:00:00Z")
	periods := trailingPeriods(domain.WatcherGrainMonth, loc, now, 3)
	if len(periods) != 3 {
		t.Fatalf("want 3 periods, got %d", len(periods))
	}
	// Period 0 = Feb, 1 = Jan, 2 = Dec. Each abuts the next.
	if periods[0].From.Format("2006-01-02") != "2026-02-01" {
		t.Errorf("period 0 from = %s", periods[0].From.Format("2006-01-02"))
	}
	if periods[2].From.Format("2006-01-02") != "2025-12-01" {
		t.Errorf("period 2 from = %s", periods[2].From.Format("2006-01-02"))
	}
	for i := 0; i+1 < len(periods); i++ {
		if !periods[i].From.Equal(periods[i+1].To) {
			t.Errorf("period %d from %v does not abut period %d to %v", i, periods[i].From, i+1, periods[i+1].To)
		}
	}
}

func TestWindowRespectsTimezone(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta") // UTC+7
	// 2026-03-15T02:00Z is 09:00 in Jakarta on the 15th, so the last complete
	// Jakarta day is the 14th, beginning at 2026-03-14T00:00+07 = 2026-03-13T17:00Z.
	now := fixedNow("2026-03-15T02:00:00Z")
	w := completePeriod(domain.WatcherGrainDay, loc, now)
	if got := w.To.In(loc).Format("2006-01-02 15:04"); got != "2026-03-15 00:00" {
		t.Errorf("to in tz = %s, want 2026-03-15 00:00", got)
	}
	if got := w.From.In(loc).Format("2006-01-02 15:04"); got != "2026-03-14 00:00" {
		t.Errorf("from in tz = %s, want 2026-03-14 00:00", got)
	}
}

// --- comparator evaluation ---

func TestEvaluateComparators(t *testing.T) {
	def := testMetricDef()
	win := metric.Window{From: fixedNow("2026-02-01T00:00:00Z"), To: fixedNow("2026-03-01T00:00:00Z")}

	cases := []struct {
		name       string
		comparator domain.WatcherComparator
		threshold  float64
		result     *metric.Result
		queryErr   error
		want       bool
		wantErr    bool
	}{
		{"gt breaches", domain.WatcherComparatorGT, 100, valueResult(150), nil, true, false},
		{"gt holds", domain.WatcherComparatorGT, 200, valueResult(150), nil, false, false},
		{"lt breaches", domain.WatcherComparatorLT, 100, valueResult(50), nil, true, false},
		{"lt holds", domain.WatcherComparatorLT, 10, valueResult(50), nil, false, false},
		{"pct_change_gt breaches", domain.WatcherComparatorPctChangeGT, 10, deltaResult(120, 100, 20), nil, true, false},
		{"pct_change_gt holds", domain.WatcherComparatorPctChangeGT, 50, deltaResult(120, 100, 20), nil, false, false},
		{"pct_change_lt breaches", domain.WatcherComparatorPctChangeLT, -10, deltaResult(80, 100, -20), nil, true, false},
		{"no_data breaches on missing", domain.WatcherComparatorNoData, 0, nil, domain.ErrInvalidInput, true, false},
		{"no_data holds when present", domain.WatcherComparatorNoData, 0, valueResult(1), nil, false, false},
		{"gt on missing does not breach", domain.WatcherComparatorGT, 100, nil, domain.ErrInvalidInput, false, false},
		{"real error propagates", domain.WatcherComparatorGT, 100, nil, context.DeadlineExceeded, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			me := &fakeMetricEval{def: def, queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
				return c.result, c.queryErr
			}}
			s := NewWatcherService(newFakeWatcherRepo(), me, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20)
			w := &domain.Watcher{CompanyID: "co-1", MetricID: "metric-1", Comparator: c.comparator, Threshold: c.threshold, CompareTo: "previous_period"}
			br, err := s.evaluate(context.Background(), w, def, win)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if br.breached != c.want {
				t.Errorf("breached = %v, want %v", br.breached, c.want)
			}
		})
	}
}

// --- HandleFire ---

func newFireService(repo *fakeWatcherRepo, me *fakeMetricEval, enq *fakeEnqueuer, th *fakeThreads, budget fakeBudget) *WatcherService {
	s := NewWatcherService(repo, me, th, fakeCompanies{}, enq, 20).WithBudget(budget)
	s.now = func() time.Time { return fixedNow("2026-03-15T00:00:00Z") }
	return s
}

func enabledWatcher() *domain.Watcher {
	return &domain.Watcher{
		ID: "w-1", CompanyID: "co-1", MetricID: "metric-1", ThreadID: "thread-1",
		Name: "Revenue floor", WindowGrain: domain.WatcherGrainMonth,
		Comparator: domain.WatcherComparatorLT, Threshold: 999999999,
		CronExpression: "0 9 * * *", Timezone: "UTC", CooldownMinutes: 720,
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
		Enabled:  true,
	}
}

func TestHandleFireBreachEnqueues(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	repo.watchers[w.ID] = w
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(500), nil // below the 999999999 floor -> breach
	}}
	enq := &fakeEnqueuer{}
	th := &fakeThreads{}
	s := newFireService(repo, me, enq, th, fakeBudget{verdict: BudgetOK})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	ev := repo.lastEvent()
	if ev == nil || !ev.Breached {
		t.Fatalf("expected a breached event, got %+v", ev)
	}
	if ev.SuppressedReason != "" {
		t.Errorf("unexpected suppression: %q", ev.SuppressedReason)
	}
	if enq.calls != 1 {
		t.Errorf("expected one chat:run enqueued, got %d", enq.calls)
	}
	if enq.last.WatcherEventID != ev.ID {
		t.Errorf("payload WatcherEventID = %q, want %q", enq.last.WatcherEventID, ev.ID)
	}
	if _, ok := repo.fired[w.ID]; !ok {
		t.Errorf("expected TouchFired to record the fire")
	}
	if len(th.appended) != 1 {
		t.Errorf("expected the briefing appended once, got %d", len(th.appended))
	}
}

func TestHandleFireNoBreachIsSilent(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.Comparator = domain.WatcherComparatorGT
	w.Threshold = 1_000_000
	repo.watchers[w.ID] = w
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(500), nil // below threshold -> no breach
	}}
	enq := &fakeEnqueuer{}
	s := newFireService(repo, me, enq, &fakeThreads{}, fakeBudget{})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	ev := repo.lastEvent()
	if ev == nil || ev.Breached {
		t.Fatalf("expected a non-breaching event, got %+v", ev)
	}
	if enq.calls != 0 {
		t.Errorf("expected nothing enqueued, got %d", enq.calls)
	}
	if _, ok := repo.fired[w.ID]; ok {
		t.Errorf("a non-breach must not touch the cooldown")
	}
}

func TestHandleFireCooldownSuppresses(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	recent := fixedNow("2026-03-14T23:30:00Z") // 30m before the fixed now, inside 720m
	w.LastFiredAt = &recent
	repo.watchers[w.ID] = w
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(500), nil
	}}
	enq := &fakeEnqueuer{}
	s := newFireService(repo, me, enq, &fakeThreads{}, fakeBudget{})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	ev := repo.lastEvent()
	if ev == nil || !ev.Breached || ev.SuppressedReason != "cooldown" {
		t.Fatalf("expected a cooldown-suppressed breach, got %+v", ev)
	}
	if enq.calls != 0 {
		t.Errorf("cooldown must not enqueue, got %d", enq.calls)
	}
}

func TestHandleFireBudgetExhaustedSuppresses(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	repo.watchers[w.ID] = w
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(500), nil
	}}
	enq := &fakeEnqueuer{}
	s := newFireService(repo, me, enq, &fakeThreads{}, fakeBudget{verdict: BudgetExhausted})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	ev := repo.lastEvent()
	if ev == nil || !ev.Breached || ev.SuppressedReason != "credits_exhausted" {
		t.Fatalf("expected a credits-suppressed breach, got %+v", ev)
	}
	if enq.calls != 0 {
		t.Errorf("an exhausted tenant must not enqueue, got %d", enq.calls)
	}
}

func TestHandleFireDisabledMetricSkips(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	repo.watchers[w.ID] = w
	def := testMetricDef()
	def.Enabled = false
	me := &fakeMetricEval{def: def, queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		t.Fatal("Query must not be called when the metric is disabled")
		return nil, nil
	}}
	enq := &fakeEnqueuer{}
	s := newFireService(repo, me, enq, &fakeThreads{}, fakeBudget{})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	if repo.lastEvent() != nil {
		t.Errorf("a disabled metric should record no event")
	}
	if enq.calls != 0 {
		t.Errorf("nothing should fire, got %d", enq.calls)
	}
}

// --- delivery (CompleteFire) ---

func TestCompleteFireDeliversToEveryChannel(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.Channels = []domain.WatcherChannel{
		{Channel: domain.ChannelDashboard},
		{Channel: domain.ChannelWhatsApp, Ref: "+62811"},
		{Channel: domain.ChannelDiscord, Ref: "chan-9"},
		{Channel: domain.ChannelLark, Ref: "oc_chat"},
		{Channel: domain.ChannelSlack, Ref: "C0SLACK"},
	}
	repo.watchers[w.ID] = w
	ev := &domain.WatcherEvent{ID: "ev-1", WatcherID: w.ID, CompanyID: w.CompanyID, Breached: true}
	repo.events = append(repo.events, ev)

	wa, lk, sl, bus := &fakeWA{}, &fakeLark{}, &fakeSlack{}, &fakeBus{}
	s := NewWatcherService(repo, &fakeMetricEval{def: testMetricDef()}, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20).
		WithDelivery(wa, lk, sl, bus)

	s.CompleteFire(context.Background(), "ev-1", "msg-assistant", "Revenue is down 12%.")

	stored := repo.events[0]
	if stored.MessageID == nil || *stored.MessageID != "msg-assistant" {
		t.Errorf("assistant message not recorded: %+v", stored.MessageID)
	}
	if len(stored.DeliveryStatus) != 5 {
		t.Fatalf("expected 5 delivery outcomes, got %d", len(stored.DeliveryStatus))
	}
	for _, d := range stored.DeliveryStatus {
		if d.Status != "delivered" {
			t.Errorf("%s: status = %s (%s), want delivered", d.Channel, d.Status, d.Error)
		}
	}
	if len(wa.sent) != 1 {
		t.Errorf("whatsapp send count = %d", len(wa.sent))
	}
	if len(bus.outbound) != 1 || bus.outbound[0].ChannelRef != "chan-9" {
		t.Errorf("discord outbound = %+v", bus.outbound)
	}
	if len(lk.chats) != 1 || lk.chats[0] != "oc_chat" {
		t.Errorf("lark send = %+v", lk.chats)
	}
	// Slack: the breach opens its own thread in the channel, so the post
	// carries no thread_ts. Replying into a thread would bury the alert under
	// whatever conversation happened to be there last.
	if len(sl.channels) != 1 || sl.channels[0] != "C0SLACK" {
		t.Errorf("slack send = %+v", sl.channels)
	}
	if len(sl.threadTS) != 1 || sl.threadTS[0] != "" {
		t.Errorf("slack thread_ts = %+v, want one empty string", sl.threadTS)
	}
}

func TestValidateChannelsAcceptsSlackWithARef(t *testing.T) {
	if err := validateChannels([]domain.WatcherChannel{
		{Channel: domain.ChannelSlack, Ref: "C0SLACK"},
	}); err != nil {
		t.Fatalf("slack with a ref must be accepted: %v", err)
	}
	err := validateChannels([]domain.WatcherChannel{{Channel: domain.ChannelSlack}})
	if err == nil || !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("slack without a ref must be refused, got %v", err)
	}
}

func TestCompleteFireSkipsChannelsWithoutProviders(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.Channels = []domain.WatcherChannel{{Channel: domain.ChannelWhatsApp, Ref: "+62811"}}
	repo.watchers[w.ID] = w
	ev := &domain.WatcherEvent{ID: "ev-1", WatcherID: w.ID, CompanyID: w.CompanyID, Breached: true}
	repo.events = append(repo.events, ev)

	// No WithDelivery: the API-shaped service.
	s := NewWatcherService(repo, &fakeMetricEval{def: testMetricDef()}, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20)
	s.CompleteFire(context.Background(), "ev-1", "msg-1", "text")

	d := repo.events[0].DeliveryStatus
	if len(d) != 1 || d[0].Status != "skipped" {
		t.Errorf("expected a skipped whatsapp delivery, got %+v", d)
	}
}

// --- DryRun + enable guard ---

func TestDryRunCountsBreachesAndRecords(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.Enabled = false
	w.Comparator = domain.WatcherComparatorLT
	w.Threshold = 100
	repo.watchers[w.ID] = w
	// Every window returns 50 (< 100), so every period would have fired.
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(50), nil
	}}
	s := NewWatcherService(repo, me, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20)
	s.now = func() time.Time { return fixedNow("2026-03-15T00:00:00Z") }

	res, err := s.DryRun(context.Background(), "co-1", w.ID)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if res.PeriodsEvaluated != watcherDryRunPeriods || res.WouldHaveFired != watcherDryRunPeriods {
		t.Errorf("evaluated=%d fired=%d, want %d/%d", res.PeriodsEvaluated, res.WouldHaveFired, watcherDryRunPeriods, watcherDryRunPeriods)
	}
	if _, ok := repo.dryRuns[w.ID]; !ok {
		t.Errorf("dry-run timestamp not recorded")
	}
}

func TestEnableRequiresRecentDryRun(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.Enabled = false
	w.LastDryRunAt = nil
	repo.watchers[w.ID] = w
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(50), nil
	}}
	s := NewWatcherService(repo, me, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20)
	s.now = func() time.Time { return fixedNow("2026-03-15T00:00:00Z") }

	enable := true
	in := watcherInputFrom(w)
	in.Enabled = &enable
	if _, err := s.Update(context.Background(), "co-1", w.ID, in); err == nil {
		t.Fatalf("expected enable without a dry-run to be rejected")
	}

	// A fresh dry-run lets it enable.
	recent := fixedNow("2026-03-14T20:00:00Z") // 4h before now
	repo.watchers[w.ID].LastDryRunAt = &recent
	out, err := s.Update(context.Background(), "co-1", w.ID, in)
	if err != nil {
		t.Fatalf("enable with fresh dry-run: %v", err)
	}
	if !out.Enabled {
		t.Errorf("expected the watcher enabled")
	}
}

func TestConditionChangeClearsDryRun(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	w.Enabled = false
	recent := fixedNow("2026-03-14T20:00:00Z")
	w.LastDryRunAt = &recent
	repo.watchers[w.ID] = w
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(50), nil
	}}
	s := NewWatcherService(repo, me, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20)
	s.now = func() time.Time { return fixedNow("2026-03-15T00:00:00Z") }

	// Changing the threshold without enabling succeeds and clears the standing
	// dry-run — it no longer describes the new condition.
	in := watcherInputFrom(w)
	in.Threshold = 42 // condition change, no enable
	if _, err := s.Update(context.Background(), "co-1", w.ID, in); err != nil {
		t.Fatalf("condition-only edit should succeed: %v", err)
	}
	if repo.watchers[w.ID].LastDryRunAt != nil {
		t.Errorf("a condition change should have cleared the dry-run")
	}

	// With the dry-run cleared, a follow-up enable is now rejected.
	enable := true
	in2 := watcherInputFrom(repo.watchers[w.ID])
	in2.Enabled = &enable
	if _, err := s.Update(context.Background(), "co-1", w.ID, in2); err == nil {
		t.Fatalf("expected enable after a cleared dry-run to be rejected")
	}
}

func TestCreateIsDisabledAndCapped(t *testing.T) {
	repo := newFakeWatcherRepo()
	me := &fakeMetricEval{def: testMetricDef(), queryFn: func(_, _ time.Time, _ metric.Comparison) (*metric.Result, error) {
		return valueResult(1), nil
	}}
	s := NewWatcherService(repo, me, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 2)

	in := WatcherInput{
		MetricID: "metric-1", Name: "w", WindowGrain: domain.WatcherGrainMonth,
		Comparator: domain.WatcherComparatorLT, Threshold: 10,
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	}
	w, err := s.Create(context.Background(), "co-1", "user-1", in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Enabled {
		t.Errorf("a new watcher must be born disabled")
	}
	// Fill to the cap (2), then the third is rejected.
	repo.count = 2
	if _, err := s.Create(context.Background(), "co-1", "user-1", in); err == nil {
		t.Fatalf("expected the per-company cap to reject the third watcher")
	}
}

func TestCreateRejectsPctChangeWithoutCompareTo(t *testing.T) {
	repo := newFakeWatcherRepo()
	me := &fakeMetricEval{def: testMetricDef()}
	s := NewWatcherService(repo, me, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20)
	in := WatcherInput{
		MetricID: "metric-1", Name: "w", WindowGrain: domain.WatcherGrainMonth,
		Comparator: domain.WatcherComparatorPctChangeGT, Threshold: 10,
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	}
	if _, err := s.Create(context.Background(), "co-1", "user-1", in); err == nil {
		t.Fatalf("expected pct_change without compare_to to be rejected")
	}
}

// watcherInputFrom builds an update input mirroring a stored watcher.
func watcherInputFrom(w *domain.Watcher) WatcherInput {
	cd := w.CooldownMinutes
	return WatcherInput{
		MetricID: w.MetricID, Name: w.Name, WindowGrain: w.WindowGrain,
		Comparator: w.Comparator, Threshold: w.Threshold, CompareTo: w.CompareTo,
		CronExpression: w.CronExpression, Timezone: w.Timezone,
		Channels: w.Channels, CooldownMinutes: &cd,
	}
}

// --- the events sheet's window (T-09 follow-up) ---

// A per-minute watcher inside a 12-hour cooldown writes an identical suppressed
// row every minute, so the last 50 evaluations were 50 copies of "not now" and
// the delivery that started the cooldown was not in the payload at all. The
// filter has to reach the query for that reason — filtering after the fact
// filters an empty set.
func TestListEventsCanNarrowToWhatDelivered(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := enabledWatcher()
	repo.watchers[w.ID] = w

	repo.events = append(repo.events,
		&domain.WatcherEvent{ID: "e-fired", WatcherID: w.ID, CompanyID: w.CompanyID, Breached: true},
		&domain.WatcherEvent{ID: "e-cooldown", WatcherID: w.ID, CompanyID: w.CompanyID,
			Breached: true, SuppressedReason: "cooldown"},
		&domain.WatcherEvent{ID: "e-quiet", WatcherID: w.ID, CompanyID: w.CompanyID},
	)
	s := NewWatcherService(repo, &fakeMetricEval{def: testMetricDef()}, &fakeThreads{}, fakeCompanies{}, &fakeEnqueuer{}, 20)

	all, err := s.ListEvents(context.Background(), w.CompanyID, w.ID, 50, 0, false)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("unfiltered = %d events, want 3 — suppressed rows answer \"why did it not message me?\"", len(all))
	}

	fired, err := s.ListEvents(context.Background(), w.CompanyID, w.ID, 50, 0, true)
	if err != nil {
		t.Fatalf("list fired: %v", err)
	}
	if len(fired) != 1 || fired[0].ID != "e-fired" {
		t.Errorf("fired-only = %+v, want just the delivery", fired)
	}
}
