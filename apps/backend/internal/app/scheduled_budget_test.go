package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// stubScheduledRepo implements only the three methods HandleFire touches on
// the refusal path. The rest return zero values: a test that needed them
// would be testing something else.
type stubScheduledRepo struct {
	task    *domain.ScheduledTask
	runs    []*domain.ScheduledTaskRun
	updated []*domain.ScheduledTaskRun
}

func (s *stubScheduledRepo) GetTask(context.Context, string) (*domain.ScheduledTask, error) {
	return s.task, nil
}
func (s *stubScheduledRepo) AppendRun(_ context.Context, r *domain.ScheduledTaskRun) error {
	r.ID = "run-1"
	s.runs = append(s.runs, r)
	return nil
}
func (s *stubScheduledRepo) UpdateRun(_ context.Context, r *domain.ScheduledTaskRun) error {
	s.updated = append(s.updated, r)
	return nil
}

func (s *stubScheduledRepo) CreateTask(context.Context, *domain.ScheduledTask) error { return nil }
func (s *stubScheduledRepo) ListTasksByCompany(context.Context, string) ([]*domain.ScheduledTask, error) {
	return nil, nil
}
func (s *stubScheduledRepo) ListTasksByUser(context.Context, string, string) ([]*domain.ScheduledTask, error) {
	return nil, nil
}
func (s *stubScheduledRepo) UpdateTask(context.Context, *domain.ScheduledTask) error { return nil }
func (s *stubScheduledRepo) DeleteTask(context.Context, string) error                { return nil }
func (s *stubScheduledRepo) SetTaskEnabled(context.Context, string, bool) error      { return nil }
func (s *stubScheduledRepo) TouchTaskRunTimes(context.Context, string, time.Time, time.Time) error {
	return nil
}
func (s *stubScheduledRepo) ListEnabledForScheduler(context.Context) ([]*domain.ScheduledTask, error) {
	return nil, nil
}
func (s *stubScheduledRepo) GetRun(context.Context, string) (*domain.ScheduledTaskRun, error) {
	return nil, nil
}
func (s *stubScheduledRepo) ListRunsByTask(context.Context, string, int, int) ([]*domain.ScheduledTaskRun, error) {
	return nil, nil
}

// TestScheduledFireRefusesAnExhaustedTenant covers the second integration
// point. A cron tick never passes through ChatEnqueuer, so without this the
// one caller nobody is watching would keep spending after every interactive
// path had been refused.
//
// As in the enqueuer test, the nil ThreadService is the assertion: reaching
// AppendUserMessage means the gate did not fire.
func TestScheduledFireRefusesAnExhaustedTenant(t *testing.T) {
	repo := &stubScheduledRepo{task: &domain.ScheduledTask{
		ID:        "t1",
		CompanyID: "c1",
		ThreadID:  "th1",
		Prompt:    "send me yesterday's numbers",
		Enabled:   true,
	}}
	credits := &stubCredits{record: &domain.CompanyCredits{
		BalanceMicroUSD:      -1,
		MonthlyGrantMicroUSD: testGrant,
	}}
	usageSvc := NewUsageService(&fakeUsageRepo{}, credits, DefaultPricing).
		WithCredits(enforcing(), &stubLLMCreds{}, nil)

	svc := NewScheduledTaskService(repo, nil, nil, nil).WithBudget(usageSvc)

	if err := svc.HandleFire(context.Background(), "t1"); err != nil {
		t.Fatalf("HandleFire = %v, want nil — the refusal is recorded, not retried", err)
	}

	// The run row exists and is marked failed, because a schedule that
	// silently stops firing is indistinguishable from one that broke.
	if len(repo.runs) != 1 {
		t.Fatalf("runs opened = %d, want 1", len(repo.runs))
	}
	if len(repo.updated) != 1 {
		t.Fatalf("runs closed = %d, want 1", len(repo.updated))
	}
	got := repo.updated[0]
	if got.Status != domain.ScheduledRunStatusFailed {
		t.Errorf("run status = %q, want %q", got.Status, domain.ScheduledRunStatusFailed)
	}
	if !strings.Contains(got.ErrorMessage, "credits") {
		t.Errorf("run error = %q, want it to name the credit balance", got.ErrorMessage)
	}
}

// TestScheduledFireAllowsAFundedTenant proves the gate is not always-on: with
// credit available HandleFire walks past it. The nil ThreadService means the
// call cannot complete, so the assertion is on what the gate did *not* do —
// no run was closed as failed.
func TestScheduledFireAllowsAFundedTenant(t *testing.T) {
	repo := &stubScheduledRepo{task: &domain.ScheduledTask{
		ID: "t1", CompanyID: "c1", ThreadID: "th1", Prompt: "go", Enabled: true,
	}}
	credits := &stubCredits{record: &domain.CompanyCredits{
		BalanceMicroUSD:      testGrant,
		MonthlyGrantMicroUSD: testGrant,
	}}
	usageSvc := NewUsageService(&fakeUsageRepo{}, credits, DefaultPricing).
		WithCredits(enforcing(), &stubLLMCreds{}, nil)
	svc := NewScheduledTaskService(repo, nil, nil, nil).WithBudget(usageSvc)

	defer func() {
		_ = recover() // the nil ThreadService, reached only because the gate let the tick through
		if len(repo.updated) != 0 {
			t.Errorf("runs closed = %d, want 0 — a funded tenant was refused", len(repo.updated))
		}
	}()
	_ = svc.HandleFire(context.Background(), "t1")
}
