package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/freshness"
)

type fakeWatchSources struct {
	src *domain.DBConnection
	err error
}

func (f fakeWatchSources) GetByID(context.Context, string) (*domain.DBConnection, error) {
	return f.src, f.err
}

// fakeFreshnessProber is the watcher's view of internal/freshness. Named apart
// from mcp_server_service_test.go's fakeProber, which probes something else.
type fakeFreshnessProber struct {
	rep   freshness.Report
	calls int
}

func (f *fakeFreshnessProber) For(context.Context, string, string) freshness.Report {
	f.calls++
	return f.rep
}

func watchedSource() *domain.DBConnection {
	return &domain.DBConnection{
		ID: "src-1", CompanyID: "co-1", Label: "Sales warehouse", DBType: "postgres",
		Freshness: domain.SourceFreshness{
			SQL: "SELECT max(loaded_at) FROM etl_runs", WarnAfterMins: 120, StaleAfterMins: 1440,
		},
	}
}

func freshnessWatcher() *domain.Watcher {
	return &domain.Watcher{
		ID: "w-f1", CompanyID: "co-1", Kind: domain.WatcherKindFreshness,
		SourceID: "src-1", ThreadID: "thread-1", Name: "Sales load",
		CronExpression: "0 9 * * *", Timezone: "UTC", CooldownMinutes: 720, Enabled: true,
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	}
}

func freshnessSvc(repo *fakeWatcherRepo, src *domain.DBConnection, rep freshness.Report) (*WatcherService, *fakeThreads, *fakeFreshnessProber) {
	th := &fakeThreads{}
	p := &fakeFreshnessProber{rep: rep}
	s := NewWatcherService(repo, &fakeMetricEval{def: testMetricDef()}, th, fakeCompanies{}, &fakeEnqueuer{}, 20).
		WithFreshness(fakeWatchSources{src: src}, p)
	s.now = func() time.Time { return fixedNow("2026-03-15T00:00:00Z") }
	return s, th, p
}

// --- validation ---

// The row this refuses to create is the worst one the table can hold: enabled,
// green, and structurally incapable of ever firing.
func TestAWatcherOnAnUnconfiguredSourceIsRefused(t *testing.T) {
	src := watchedSource()
	src.Freshness = domain.SourceFreshness{}
	s, _, _ := freshnessSvc(newFakeWatcherRepo(), src, freshness.Report{})

	_, err := s.Create(context.Background(), "co-1", "u-1", WatcherInput{
		Kind: domain.WatcherKindFreshness, SourceID: "src-1", Name: "Sales load",
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v; want ErrInvalidInput", err)
	}
	// The message names the fix, not the fault.
	if !strings.Contains(err.Error(), "Settings") {
		t.Errorf("err = %q; an admin cannot act on this", err)
	}
}

func TestAWatcherOnAnotherTenantsSourceIsRefusedIndistinguishably(t *testing.T) {
	other := watchedSource()
	other.CompanyID = "someone-else"
	s, _, _ := freshnessSvc(newFakeWatcherRepo(), other, freshness.Report{})
	_, err := s.Create(context.Background(), "co-1", "u-1", WatcherInput{
		Kind: domain.WatcherKindFreshness, SourceID: "src-1", Name: "x",
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	})
	if err == nil || !strings.Contains(err.Error(), "no source with that id") {
		t.Fatalf("err = %v; want the same answer a missing id gets", err)
	}
}

func TestAFreshnessWatcherNeedsASource(t *testing.T) {
	s, _, _ := freshnessSvc(newFakeWatcherRepo(), watchedSource(), freshness.Report{})
	_, err := s.Create(context.Background(), "co-1", "u-1", WatcherInput{
		Kind: domain.WatcherKindFreshness, Name: "x",
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v; want ErrInvalidInput", err)
	}
}

// A client written before 080 sends no kind at all and must keep working.
func TestAnEmptyKindIsStillAMetricWatcher(t *testing.T) {
	repo := newFakeWatcherRepo()
	s, _, _ := freshnessSvc(repo, watchedSource(), freshness.Report{})
	w, err := s.Create(context.Background(), "co-1", "u-1", WatcherInput{
		MetricID: "metric-1", Name: "Revenue floor",
		WindowGrain: domain.WatcherGrainMonth, Comparator: domain.WatcherComparatorLT, Threshold: 1,
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if w.Kind != domain.WatcherKindMetric {
		t.Errorf("Kind = %q; an absent kind must mean metric", w.Kind)
	}
}

// --- the fire ---

func TestAStaleSourceBreachesAndIsDeliveredWithoutAModelTurn(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	repo.watchers[w.ID] = w
	at := fixedNow("2026-03-12T02:00:00Z")
	s, th, _ := freshnessSvc(repo, watchedSource(), freshness.Report{
		Verdict: freshness.Stale, ObservedAt: at, Age: 70 * time.Hour,
	})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire() = %v", err)
	}
	if len(repo.events) != 1 || !repo.events[0].Breached {
		t.Fatalf("events = %+v; want one breach", repo.events)
	}
	if len(th.appended) != 1 {
		t.Fatalf("appended %d messages; want 1", len(th.appended))
	}
	msg := th.appended[0]
	// The three things somebody woken by this needs: which source, when it last
	// loaded, and what it means for the answers they are about to read.
	for _, want := range []string{"Sales warehouse", "2026-03-12", "has not refreshed", "2 days ago"} {
		if !strings.Contains(msg, want) && !strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
			t.Errorf("briefing is missing %q:\n%s", want, msg)
		}
	}
}

// Decision 6, in the one place it costs something to hold: a probe that broke
// at 03:00 must not page anybody.
func TestAnUnknownVerdictDoesNotBreach(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	repo.watchers[w.ID] = w
	s, th, _ := freshnessSvc(repo, watchedSource(), freshness.Report{Verdict: freshness.Unknown})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire() = %v", err)
	}
	if len(repo.events) != 1 || repo.events[0].Breached {
		t.Fatalf("events = %+v; a broken probe must not fire an alert", repo.events)
	}
	if len(th.appended) != 0 {
		t.Errorf("a briefing was written for an unknown verdict: %v", th.appended)
	}
}

func TestAFreshSourceIsQuiet(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	repo.watchers[w.ID] = w
	s, th, _ := freshnessSvc(repo, watchedSource(), freshness.Report{
		Verdict: freshness.Fresh, ObservedAt: fixedNow("2026-03-14T23:30:00Z"), Age: 30 * time.Minute,
	})
	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire() = %v", err)
	}
	if repo.events[0].Breached || len(th.appended) != 0 {
		t.Errorf("a fresh source produced a fire")
	}
}

// A warn verdict is a dated answer, not an alert. Firing on it would page
// somebody for six hours of ordinary ETL lag.
func TestAWarnVerdictDoesNotBreach(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	repo.watchers[w.ID] = w
	s, _, _ := freshnessSvc(repo, watchedSource(), freshness.Report{
		Verdict: freshness.Warn, ObservedAt: fixedNow("2026-03-14T18:00:00Z"), Age: 6 * time.Hour,
	})
	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire() = %v", err)
	}
	if repo.events[0].Breached {
		t.Error("a warn verdict fired an alert")
	}
}

func TestTheCooldownAppliesToFreshnessToo(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	fired := fixedNow("2026-03-14T23:00:00Z")
	w.LastFiredAt = &fired
	repo.watchers[w.ID] = w
	s, th, _ := freshnessSvc(repo, watchedSource(), freshness.Report{
		Verdict: freshness.Stale, ObservedAt: fixedNow("2026-03-12T02:00:00Z"), Age: 70 * time.Hour,
	})
	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire() = %v", err)
	}
	if repo.events[0].SuppressedReason != "cooldown" {
		t.Errorf("suppressed_reason = %q; want cooldown", repo.events[0].SuppressedReason)
	}
	if len(th.appended) != 0 {
		t.Error("a suppressed fire still wrote a briefing")
	}
}

// A tenant out of credits still needs to know their pipeline is broken — and
// this path spends nothing, so there is nothing to refuse.
func TestAnExhaustedTenantIsStillToldTheirDataStopped(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	repo.watchers[w.ID] = w
	s, th, _ := freshnessSvc(repo, watchedSource(), freshness.Report{
		Verdict: freshness.Stale, ObservedAt: fixedNow("2026-03-12T02:00:00Z"), Age: 70 * time.Hour,
	})
	s.WithBudget(fakeBudget{verdict: BudgetExhausted})

	if err := s.HandleFire(context.Background(), w.ID); err != nil {
		t.Fatalf("HandleFire() = %v", err)
	}
	if repo.events[0].SuppressedReason != "" {
		t.Errorf("suppressed_reason = %q; this path spends nothing", repo.events[0].SuppressedReason)
	}
	if len(th.appended) != 1 {
		t.Error("the breach was suppressed on an exhausted tenant")
	}
}

// --- the dry run ---

func TestTheDryRunRefusesToVouchForASourceThatIsNotReporting(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	w.Enabled = false
	repo.watchers[w.ID] = w
	s, _, _ := freshnessSvc(repo, watchedSource(), freshness.Report{Verdict: freshness.Unknown})

	_, err := s.DryRun(context.Background(), "co-1", w.ID)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v; a dry-run that passes on `unknown` would enable a watcher that can never fire", err)
	}
}

func TestTheDryRunReportsOneSampleAndSaysSo(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	repo.watchers[w.ID] = w
	s, _, _ := freshnessSvc(repo, watchedSource(), freshness.Report{
		Verdict: freshness.Stale, ObservedAt: fixedNow("2026-03-12T02:00:00Z"), Age: 70 * time.Hour,
	})
	res, err := s.DryRun(context.Background(), "co-1", w.ID)
	if err != nil {
		t.Fatalf("DryRun() = %v", err)
	}
	// One, not five: a source's load time has exactly one value and nothing here
	// records what it was yesterday.
	if res.PeriodsEvaluated != 1 || len(res.Samples) != 1 {
		t.Fatalf("res = %+v; want a single sample", res)
	}
	if res.WouldHaveFired != 1 || !res.Samples[0].Breached {
		t.Errorf("a stale source would not have fired: %+v", res)
	}
	if repo.dryRuns[w.ID].IsZero() {
		t.Error("the dry-run was not recorded, so the watcher still cannot be enabled")
	}
}

// --- update ---

func TestAWatcherCannotChangeWhatKindOfThingItWatches(t *testing.T) {
	repo := newFakeWatcherRepo()
	w := freshnessWatcher()
	repo.watchers[w.ID] = w
	s, _, _ := freshnessSvc(repo, watchedSource(), freshness.Report{})

	_, err := s.Update(context.Background(), "co-1", w.ID, WatcherInput{
		Kind: domain.WatcherKindMetric, MetricID: "metric-1", Name: "now a metric",
		WindowGrain: domain.WatcherGrainMonth, Comparator: domain.WatcherComparatorLT,
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Channels: []domain.WatcherChannel{{Channel: domain.ChannelDashboard}},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v; want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "delete it and create") {
		t.Errorf("err = %q; the message should say what to do instead", err)
	}
}
