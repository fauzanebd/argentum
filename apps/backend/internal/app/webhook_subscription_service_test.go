package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-15: who gets told, and what the body says. How it travels is webhookout's
// and is not re-tested here — the ticket's instruction is that this package
// does not become a second sender, so a test that mocked delivery would be
// testing a thing that should not exist.

type fakeSubRepo struct {
	subs      []*domain.WebhookSubscription
	listErr   error
	created   *domain.WebhookSubscription
	forEvent  string
	listCalls int
}

func (r *fakeSubRepo) Create(_ context.Context, s *domain.WebhookSubscription) error {
	s.ID = "sub-new"
	r.created = s
	r.subs = append(r.subs, s)
	return nil
}

func (r *fakeSubRepo) GetByID(_ context.Context, companyID, id string) (*domain.WebhookSubscription, error) {
	for _, s := range r.subs {
		if s.ID == id && s.CompanyID == companyID {
			return s, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *fakeSubRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.WebhookSubscription, error) {
	var out []*domain.WebhookSubscription
	for _, s := range r.subs {
		if s.CompanyID == companyID {
			out = append(out, s)
		}
	}
	return out, r.listErr
}

func (r *fakeSubRepo) Update(_ context.Context, _ *domain.WebhookSubscription) error { return nil }
func (r *fakeSubRepo) Delete(_ context.Context, _, _ string) error                   { return nil }

func (r *fakeSubRepo) ListForEvent(_ context.Context, companyID, event string) ([]*domain.WebhookSubscription, error) {
	r.listCalls++
	r.forEvent = event
	if r.listErr != nil {
		return nil, r.listErr
	}
	var out []*domain.WebhookSubscription
	for _, s := range r.subs {
		if s.CompanyID == companyID && s.WantsEvent(event) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *fakeSubRepo) RecordSuccess(context.Context, string, time.Time) error { return nil }
func (r *fakeSubRepo) RecordFailure(context.Context, string, time.Time, int) (bool, error) {
	return false, nil
}

type sentWebhook struct {
	companyID, subscriptionID, event, url string
	payload                               any
}

type fakeDispatcher struct {
	sent         []sentWebhook
	err          error
	allowPrivate bool
}

func (d *fakeDispatcher) SendFrom(_ context.Context, companyID, subscriptionID, event, url string, payload any) (string, error) {
	d.sent = append(d.sent, sentWebhook{companyID, subscriptionID, event, url, payload})
	return "delivery-1", d.err
}

func (d *fakeDispatcher) AllowPrivate() bool { return d.allowPrivate }

func subService(repo *fakeSubRepo, disp *fakeDispatcher) *WebhookSubscriptionService {
	return NewWebhookSubscriptionService(repo, disp)
}

func TestCreateRefusesAnEmptyEventList(t *testing.T) {
	svc := subService(&fakeSubRepo{}, &fakeDispatcher{})

	_, err := svc.Create(context.Background(), "co-1", "https://hooks.example/argentum", nil)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
	// The opposite rule to an agent's tool allowlist, and the message has to say
	// why: there, empty means everything.
	if !strings.Contains(err.Error(), "at least one event") {
		t.Errorf("err = %q, want it to say a subscription needs an event", err)
	}
}

func TestCreateRefusesAnUnknownEvent(t *testing.T) {
	svc := subService(&fakeSubRepo{}, &fakeDispatcher{})

	_, err := svc.Create(context.Background(), "co-1", "https://hooks.example/argentum", []string{"watcher.exploded"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want invalid input", err)
	}
	if !strings.Contains(err.Error(), "watcher.breached") {
		t.Errorf("err = %q, want it to name the events that do exist", err)
	}
}

// The URL is checked against the rule the worker will apply, so a tenant learns
// now rather than through deliveries that fail forever.
func TestCreateRefusesAnAddressWeWillNotPostTo(t *testing.T) {
	svc := subService(&fakeSubRepo{}, &fakeDispatcher{allowPrivate: false})

	for _, url := range []string{"http://169.254.169.254/latest/meta-data", "https://127.0.0.1/hook", "not-a-url"} {
		if _, err := svc.Create(context.Background(), "co-1", url, []string{domain.WebhookWatcherBreached}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", url, err)
		}
	}
}

func TestCreateDeduplicatesAndSortsEvents(t *testing.T) {
	repo := &fakeSubRepo{}
	svc := subService(repo, &fakeDispatcher{})

	sub, err := svc.Create(context.Background(), "co-1", "https://hooks.example/argentum", []string{
		domain.WebhookScheduledTaskCompleted, domain.WebhookWatcherBreached, domain.WebhookWatcherBreached, " ",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := []string{domain.WebhookScheduledTaskCompleted, domain.WebhookWatcherBreached}
	if len(sub.Events) != 2 || sub.Events[0] != want[0] || sub.Events[1] != want[1] {
		t.Errorf("events = %v, want %v", sub.Events, want)
	}
	if !sub.Enabled {
		t.Error("a created subscription is disabled; a tenant filling in this form wants deliveries")
	}
}

// The fan-out: only the subscriptions that asked, each with its own id so a
// failure can be counted against it.
func TestPublishReachesOnlyTheSubscriptionsThatWantTheEvent(t *testing.T) {
	repo := &fakeSubRepo{subs: []*domain.WebhookSubscription{
		{ID: "s1", CompanyID: "co-1", URL: "https://a.example/hook", Events: []string{domain.WebhookWatcherBreached}, Enabled: true},
		{ID: "s2", CompanyID: "co-1", URL: "https://b.example/hook", Events: []string{domain.WebhookActionExecuted}, Enabled: true},
		{ID: "s3", CompanyID: "co-1", URL: "https://c.example/hook", Events: []string{domain.WebhookWatcherBreached}, Enabled: false},
		{ID: "s4", CompanyID: "co-2", URL: "https://d.example/hook", Events: []string{domain.WebhookWatcherBreached}, Enabled: true},
	}}
	disp := &fakeDispatcher{}
	svc := subService(repo, disp)

	svc.Publish(context.Background(), "co-1", domain.WebhookWatcherBreached, map[string]string{"watcher_id": "w-1"})

	if len(disp.sent) != 1 {
		t.Fatalf("sent %d deliveries, want 1: %+v", len(disp.sent), disp.sent)
	}
	got := disp.sent[0]
	if got.subscriptionID != "s1" || got.url != "https://a.example/hook" {
		t.Errorf("delivered to %+v, want the enabled subscription that named the event", got)
	}
	if got.event != domain.WebhookWatcherBreached || got.companyID != "co-1" {
		t.Errorf("delivery = %+v", got)
	}
}

// An event nobody subscribed to costs one indexed read and nothing else.
func TestPublishWithNoSubscribersSendsNothing(t *testing.T) {
	repo := &fakeSubRepo{}
	disp := &fakeDispatcher{}
	subService(repo, disp).Publish(context.Background(), "co-1", domain.WebhookActionExecuted, nil)

	if len(disp.sent) != 0 {
		t.Errorf("sent %d deliveries with no subscriptions", len(disp.sent))
	}
	if repo.listCalls != 1 {
		t.Errorf("read subscriptions %d times, want exactly one per event", repo.listCalls)
	}
}

// The rule the whole fan-out rests on: publishing never fails the thing that
// produced the event. A watcher that breached, breached.
func TestPublishSurvivesEveryFailure(t *testing.T) {
	t.Run("subscription read fails", func(t *testing.T) {
		repo := &fakeSubRepo{listErr: errors.New("control database is down")}
		subService(repo, &fakeDispatcher{}).Publish(context.Background(), "co-1", domain.WebhookWatcherBreached, nil)
	})
	t.Run("delivery cannot be queued", func(t *testing.T) {
		repo := &fakeSubRepo{subs: []*domain.WebhookSubscription{
			{ID: "s1", CompanyID: "co-1", URL: "https://a.example/hook", Events: []string{domain.WebhookWatcherBreached}, Enabled: true},
		}}
		disp := &fakeDispatcher{err: errors.New("redis is unreachable")}
		subService(repo, disp).Publish(context.Background(), "co-1", domain.WebhookWatcherBreached, nil)
		if len(disp.sent) != 1 {
			t.Error("the delivery was not attempted")
		}
	})
	t.Run("no service at all", func(t *testing.T) {
		var svc *WebhookSubscriptionService
		svc.Publish(context.Background(), "co-1", domain.WebhookWatcherBreached, nil)
	})
	t.Run("no company", func(t *testing.T) {
		repo := &fakeSubRepo{}
		subService(repo, &fakeDispatcher{}).Publish(context.Background(), "", domain.WebhookWatcherBreached, nil)
		if repo.listCalls != 0 {
			t.Error("a tenant-less event read the subscription table")
		}
	})
}

// The payload is what somebody else writes code against, so its shape is
// asserted rather than assumed — and `event` and `occurred_at` are in the body,
// not only the headers, because a proxy can drop a header.
func TestWatcherBreachedPayloadShape(t *testing.T) {
	value, comparison := 42.5, 61.0
	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	w := &domain.Watcher{
		ID: "w-1", CompanyID: "co-1", MetricID: "m-1", Name: "Revenue floor",
		Comparator: domain.WatcherComparator("lt"), Threshold: 50, WindowGrain: domain.WatcherGrain("day"),
	}
	ev := &domain.WatcherEvent{ID: "ev-1", FiredAt: at, MetricValue: &value, ComparisonValue: &comparison}

	raw, err := json.Marshal(newWatcherBreachedPayload(w, ev, at))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"event", "occurred_at", "company_id", "watcher_id", "metric_id", "event_id", "value", "threshold", "comparator"} {
		if _, ok := got[key]; !ok {
			t.Errorf("payload is missing %q: %s", key, raw)
		}
	}
	if got["event"] != domain.WebhookWatcherBreached {
		t.Errorf("event = %v", got["event"])
	}
	if got["value"] != 42.5 {
		t.Errorf("value = %v, want the measured number", got["value"])
	}
}

// A no-data breach has no number, and a zero would be a different claim.
func TestWatcherBreachedPayloadOmitsAnAbsentValue(t *testing.T) {
	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	w := &domain.Watcher{ID: "w-1", CompanyID: "co-1", MetricID: "m-1"}
	ev := &domain.WatcherEvent{ID: "ev-1", FiredAt: at}

	raw, err := json.Marshal(newWatcherBreachedPayload(w, ev, at))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"value"`) {
		t.Errorf("payload asserts a value it does not have: %s", raw)
	}
}

func TestActionExecutedPayloadCarriesTheFailure(t *testing.T) {
	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	inv := &domain.ActionInvocation{
		ID: "inv-1", CompanyID: "co-1", Kind: "http_action",
		Status: domain.InvocationFailed, ErrorText: "endpoint answered 500", DecidedBy: "user-1",
	}

	raw, err := json.Marshal(newActionExecutedPayload(inv, `Call the registered HTTP endpoint "ops_ticket"`, at))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["status"] != string(domain.InvocationFailed) {
		t.Errorf("status = %v, want the failure to be reported as one", got["status"])
	}
	if got["error_text"] != "endpoint answered 500" {
		t.Errorf("error_text = %v", got["error_text"])
	}
	if got["decided_by"] != "user-1" {
		t.Errorf("decided_by = %v, want the human who approved it", got["decided_by"])
	}
}
