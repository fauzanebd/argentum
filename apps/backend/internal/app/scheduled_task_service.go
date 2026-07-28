// Package app — scheduled tasks service.
//
// ScheduledTaskService is the single owner of cron-scheduled agent prompts.
// It validates cron + IANA timezone inputs, persists ScheduledTask rows
// alongside a dedicated dashboard thread per task, and provides the
// per-fire orchestration (open run, enqueue chat:run, mark result) used by
// the worker's scheduled:run handler and by ChatRunner's completion path.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	// Embed the IANA timezone database in the binary. normalizeTimezone and
	// nextFire both go through time.LoadLocation, which otherwise reads
	// /usr/share/zoneinfo — a directory the deployed images do not have:
	// Dockerfile.{api,worker,discord} run on `alpine:latest` with only
	// ca-certificates installed. Without this, every scheduled task with a
	// non-UTC timezone is rejected at creation with "invalid timezone" and
	// every existing one loses its next-run time, while the same code works
	// on any developer machine. Found writing T-02's cron tests.
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tools"
)

// scheduledCronParser supports the standard 5-field cron syntax. Timezone
// is applied at firing time via cron.ParseStandard("CRON_TZ=… spec").
var scheduledCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// ScheduledTaskService coordinates scheduled-task CRUD, cron/timezone
// validation, and the per-fire run lifecycle.
type ScheduledTaskService struct {
	repo      domain.ScheduledTaskRepository
	threads   *ThreadService
	companies domain.CompanyRepository
	enqueuer  *queue.Enqueuer
	budget    BudgetChecker
}

// WithBudget gates each fire on the tenant's credit balance. This is a second
// integration point for T-03 and not a duplicate of ChatEnqueuer's: a cron
// tick never passes through ChatEnqueuer, and an unattended schedule on an
// exhausted tenant is precisely the unbounded spend the ticket exists to
// stop — nobody is watching it to notice the refusal.
func (s *ScheduledTaskService) WithBudget(b BudgetChecker) *ScheduledTaskService {
	s.budget = b
	return s
}

func NewScheduledTaskService(
	repo domain.ScheduledTaskRepository,
	threads *ThreadService,
	companies domain.CompanyRepository,
	enqueuer *queue.Enqueuer,
) *ScheduledTaskService {
	return &ScheduledTaskService{
		repo:      repo,
		threads:   threads,
		companies: companies,
		enqueuer:  enqueuer,
	}
}

// CreateInput is the shape used by both the agent tool and the REST
// handler. UserID is optional (only set for dashboard-originated creations
// via the agent tool); ThreadID is unused here because Create always mints
// a fresh dedicated thread for the task.
type CreateInput struct {
	CompanyID      string
	UserID         string
	Name           string
	Prompt         string
	CronExpression string
	Timezone       string
}

// Create validates the input, mints a dedicated dashboard thread for the
// task, and inserts the scheduled_tasks row. The new task is returned
// fully populated (including ID, ThreadID, NextRunAt).
func (s *ScheduledTaskService) Create(ctx context.Context, in CreateInput) (*domain.ScheduledTask, error) {
	if in.CompanyID == "" {
		return nil, fmt.Errorf("company_id required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("name required")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, fmt.Errorf("prompt required")
	}
	tz, err := normalizeTimezone(in.Timezone)
	if err != nil {
		return nil, err
	}
	if err := validateCron(in.CronExpression, tz); err != nil {
		return nil, err
	}
	if in.UserID == "" {
		return nil, fmt.Errorf("user_id required (scheduled tasks are owned by a dashboard user)")
	}

	thread, err := s.threads.CreateDashboardThread(ctx, in.CompanyID, in.UserID, "Scheduled: "+in.Name)
	if err != nil {
		return nil, fmt.Errorf("create dedicated thread: %w", err)
	}

	t := &domain.ScheduledTask{
		CompanyID:      in.CompanyID,
		UserID:         in.UserID,
		ThreadID:       thread.ID,
		Name:           in.Name,
		Prompt:         in.Prompt,
		CronExpression: in.CronExpression,
		Timezone:       tz,
		Enabled:        true,
	}
	if err := s.repo.CreateTask(ctx, t); err != nil {
		return nil, err
	}
	if next, err := nextFire(t.CronExpression, t.Timezone, time.Now()); err == nil {
		_ = s.repo.TouchTaskRunTimes(ctx, t.ID, time.Time{}, next)
		t.NextRunAt = &next
	}
	return t, nil
}

// Get returns the task if it belongs to companyID; otherwise ErrUnauthorized.
func (s *ScheduledTaskService) Get(ctx context.Context, companyID, id string) (*domain.ScheduledTask, error) {
	t, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.CompanyID != companyID {
		return nil, domain.ErrUnauthorized
	}
	return t, nil
}

// ListByCompany lists every scheduled task in the company. UserID filter
// is applied client-side at the handler if needed.
func (s *ScheduledTaskService) ListByCompany(ctx context.Context, companyID string) ([]*domain.ScheduledTask, error) {
	return s.repo.ListTasksByCompany(ctx, companyID)
}

// UpdateInput carries the editable fields of a scheduled task.
type UpdateInput struct {
	Name           *string
	Prompt         *string
	CronExpression *string
	Timezone       *string
	Enabled        *bool
}

// Update mutates the task in place. Each pointer field is optional;
// nil leaves the existing value alone.
func (s *ScheduledTaskService) Update(ctx context.Context, companyID, id string, in UpdateInput) (*domain.ScheduledTask, error) {
	t, err := s.Get(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return nil, fmt.Errorf("name cannot be empty")
		}
		t.Name = *in.Name
	}
	if in.Prompt != nil {
		if strings.TrimSpace(*in.Prompt) == "" {
			return nil, fmt.Errorf("prompt cannot be empty")
		}
		t.Prompt = *in.Prompt
	}
	if in.Timezone != nil {
		tz, err := normalizeTimezone(*in.Timezone)
		if err != nil {
			return nil, err
		}
		t.Timezone = tz
	}
	if in.CronExpression != nil {
		if err := validateCron(*in.CronExpression, t.Timezone); err != nil {
			return nil, err
		}
		t.CronExpression = *in.CronExpression
	}
	if in.Enabled != nil {
		t.Enabled = *in.Enabled
	}
	if err := s.repo.UpdateTask(ctx, t); err != nil {
		return nil, err
	}
	if next, err := nextFire(t.CronExpression, t.Timezone, time.Now()); err == nil {
		var last time.Time
		if t.LastRunAt != nil {
			last = *t.LastRunAt
		}
		_ = s.repo.TouchTaskRunTimes(ctx, t.ID, last, next)
		t.NextRunAt = &next
	}
	return t, nil
}

// Delete removes the task and (via FK cascade) every run associated with
// it. The dedicated thread is intentionally left in place so the user
// keeps the run history.
func (s *ScheduledTaskService) Delete(ctx context.Context, companyID, id string) error {
	if _, err := s.Get(ctx, companyID, id); err != nil {
		return err
	}
	return s.repo.DeleteTask(ctx, id)
}

// ListRuns returns the most recent runs for a task.
func (s *ScheduledTaskService) ListRuns(ctx context.Context, companyID, taskID string, limit, offset int) ([]*domain.ScheduledTaskRun, error) {
	if _, err := s.Get(ctx, companyID, taskID); err != nil {
		return nil, err
	}
	return s.repo.ListRunsByTask(ctx, taskID, limit, offset)
}

// GetRun returns one run record (auth-checked via task ownership).
func (s *ScheduledTaskService) GetRun(ctx context.Context, companyID, taskID, runID string) (*domain.ScheduledTaskRun, error) {
	if _, err := s.Get(ctx, companyID, taskID); err != nil {
		return nil, err
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.TaskID != taskID || run.CompanyID != companyID {
		return nil, domain.ErrUnauthorized
	}
	return run, nil
}

// HandleFire is invoked by the worker's scheduled:run handler. It opens a
// new ScheduledTaskRun, appends the saved prompt to the dedicated thread
// as a user message, and enqueues a chat:run task. The chat runner will
// later call MarkRunResult to close the loop.
func (s *ScheduledTaskService) HandleFire(ctx context.Context, taskID string) error {
	t, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !t.Enabled {
		return nil
	}

	run := &domain.ScheduledTaskRun{
		TaskID:    t.ID,
		CompanyID: t.CompanyID,
		Status:    domain.ScheduledRunStatusRunning,
	}
	if err := s.repo.AppendRun(ctx, run); err != nil {
		return fmt.Errorf("open run: %w", err)
	}

	// After the run row exists, so the refusal is visible in the task's run
	// history — a schedule that silently stops firing is indistinguishable
	// from one that broke — and before the user message, so a refused tick
	// does not append a prompt nobody will ever answer.
	if s.budget != nil {
		st, err := s.budget.CheckBudget(ctx, t.CompanyID)
		if err != nil {
			logrus.WithError(err).WithField("company_id", t.CompanyID).
				Warn("scheduled fire: budget check failed; running the task")
		} else if st.Blocked() {
			s.failRun(ctx, run, errors.New(CreditsExhaustedMessage))
			return nil // recorded on the run; an asynq retry would only refuse again
		}
	}

	userMsg, err := s.threads.AppendUserMessage(ctx, t.ThreadID, t.Prompt)
	if err != nil {
		s.failRun(ctx, run, fmt.Errorf("append user message: %w", err))
		return nil // already recorded; don't trigger asynq retry
	}

	var companyName, currency string
	if c, err := s.companies.GetByID(ctx, t.CompanyID); err == nil {
		companyName = c.Name
		currency = c.DefaultCurrency
	}

	if _, err := s.enqueuer.EnqueueChatRun(ctx, queue.ChatRunPayload{
		CompanyID:       t.CompanyID,
		ThreadID:        t.ThreadID,
		UserID:          t.UserID,
		Channel:         domain.ChannelDashboard,
		Message:         t.Prompt,
		UserMsgID:       userMsg.ID,
		CompanyName:     companyName,
		DefaultCurrency: currency,
		ScheduledTaskID: t.ID,
		ScheduledRunID:  run.ID,
	}); err != nil {
		s.failRun(ctx, run, fmt.Errorf("enqueue chat:run: %w", err))
		return nil
	}

	now := time.Now()
	if next, err := nextFire(t.CronExpression, t.Timezone, now); err == nil {
		_ = s.repo.TouchTaskRunTimes(ctx, t.ID, now, next)
	}
	return nil
}

// MarkRunResult closes a run with success or failure. If err is non-nil,
// status is "failed" and assistantMsgID is ignored. Called from
// ChatRunner.completeWith and ChatRunner.handleRunError.
func (s *ScheduledTaskService) MarkRunResult(ctx context.Context, runID, assistantMsgID string, runErr error) {
	if s == nil || runID == "" {
		return
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		logrus.WithError(err).WithField("run_id", runID).Warn("scheduled run lookup failed")
		return
	}
	now := time.Now()
	run.FinishedAt = &now
	if runErr != nil {
		run.Status = domain.ScheduledRunStatusFailed
		run.ErrorMessage = truncateErr(runErr.Error())
	} else {
		run.Status = domain.ScheduledRunStatusSucceeded
		if assistantMsgID != "" {
			id := assistantMsgID
			run.AssistantMsgID = &id
		}
	}
	if err := s.repo.UpdateRun(ctx, run); err != nil {
		logrus.WithError(err).WithField("run_id", runID).Warn("scheduled run update failed")
	}
}

func (s *ScheduledTaskService) failRun(ctx context.Context, run *domain.ScheduledTaskRun, err error) {
	now := time.Now()
	run.Status = domain.ScheduledRunStatusFailed
	run.ErrorMessage = truncateErr(err.Error())
	run.FinishedAt = &now
	if updErr := s.repo.UpdateRun(ctx, run); updErr != nil {
		logrus.WithError(updErr).WithField("run_id", run.ID).Warn("scheduled run failure update failed")
	}
}

// Repo exposes the underlying repository for components that need direct
// read access (e.g. the asynq DBConfigProvider that polls enabled tasks).
func (s *ScheduledTaskService) Repo() domain.ScheduledTaskRepository { return s.repo }

// CreateScheduledTask satisfies tools.ScheduledTaskCreator. The tool
// package can't import internal/app (would close an import cycle), so
// this adapter translates between tools' DTOs and the service's input.
func (s *ScheduledTaskService) CreateScheduledTask(ctx context.Context, in tools.CreateScheduledTaskInput) (*tools.ScheduledTaskResult, error) {
	t, err := s.Create(ctx, CreateInput{
		CompanyID:      in.CompanyID,
		UserID:         in.UserID,
		Name:           in.Name,
		Prompt:         in.Prompt,
		CronExpression: in.CronExpression,
		Timezone:       in.Timezone,
	})
	if err != nil {
		return nil, err
	}
	return &tools.ScheduledTaskResult{
		TaskID:         t.ID,
		Name:           t.Name,
		CronExpression: t.CronExpression,
		Timezone:       t.Timezone,
		NextRunAt:      t.NextRunAt,
		ThreadID:       t.ThreadID,
	}, nil
}

// --- helpers ---

func validateCron(spec, tz string) error {
	if strings.TrimSpace(spec) == "" {
		return fmt.Errorf("cron_expression required")
	}
	full := spec
	if tz != "" && tz != "UTC" {
		full = "CRON_TZ=" + tz + " " + spec
	}
	if _, err := scheduledCronParser.Parse(full); err != nil {
		return fmt.Errorf("invalid cron_expression: %w", err)
	}
	return nil
}

func normalizeTimezone(tz string) (string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return "UTC", nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return tz, nil
}

func nextFire(spec, tz string, after time.Time) (time.Time, error) {
	full := spec
	if tz != "" && tz != "UTC" {
		full = "CRON_TZ=" + tz + " " + spec
	}
	sched, err := scheduledCronParser.Parse(full)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(after), nil
}

func truncateErr(s string) string {
	const max = 1024
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
