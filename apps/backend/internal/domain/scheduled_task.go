package domain

import (
	"context"
	"time"
)

// ScheduledRunStatus is how one firing of a scheduled task ended.
//
// A named type rather than three untyped constants (T-02b): the constants and
// the `Status` field were declared independently, so nothing stopped a fourth
// spelling reaching the column — and the generated TypeScript inherited the
// weakness as a bare `string` while the dashboard's hand-written type had the
// three-value union right. The union is the truth; this is where it is now
// written down.
type ScheduledRunStatus string

// Values written to scheduled_task_runs.status.
const (
	ScheduledRunStatusRunning   ScheduledRunStatus = "running"
	ScheduledRunStatusSucceeded ScheduledRunStatus = "succeeded"
	ScheduledRunStatusFailed    ScheduledRunStatus = "failed"
)

// ScheduledTask is a cron-driven saved prompt. Each fire reuses the same
// dedicated thread (ThreadID) so the dashboard can render the run history
// as a normal conversation.
type ScheduledTask struct {
	ID             string     `json:"id"`
	CompanyID      string     `json:"company_id"`
	UserID         string     `json:"user_id,omitempty"`
	ThreadID       string     `json:"thread_id"`
	Name           string     `json:"name"`
	Prompt         string     `json:"prompt"`
	CronExpression string     `json:"cron_expression"`
	Timezone       string     `json:"timezone"`
	Enabled        bool       `json:"enabled"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ScheduledTaskRun is one execution of a ScheduledTask. AssistantMsgID
// is populated once the agent reply has been persisted to the dedicated
// thread; it stays nil for failed runs that never produced one.
type ScheduledTaskRun struct {
	ID             string             `json:"id"`
	TaskID         string             `json:"task_id"`
	CompanyID      string             `json:"company_id"`
	Status         ScheduledRunStatus `json:"status"`
	AssistantMsgID *string            `json:"assistant_msg_id,omitempty"`
	ErrorMessage   string             `json:"error_message,omitempty"`
	StartedAt      time.Time          `json:"started_at"`
	FinishedAt     *time.Time         `json:"finished_at,omitempty"`
}

// ScheduledTaskRepository is the persistence contract for cron-scheduled
// agent tasks and their per-fire runs.
type ScheduledTaskRepository interface {
	// Tasks
	CreateTask(ctx context.Context, t *ScheduledTask) error
	GetTask(ctx context.Context, id string) (*ScheduledTask, error)
	ListTasksByCompany(ctx context.Context, companyID string) ([]*ScheduledTask, error)
	ListTasksByUser(ctx context.Context, companyID, userID string) ([]*ScheduledTask, error)
	UpdateTask(ctx context.Context, t *ScheduledTask) error
	DeleteTask(ctx context.Context, id string) error
	SetTaskEnabled(ctx context.Context, id string, enabled bool) error
	TouchTaskRunTimes(ctx context.Context, id string, lastRun, nextRun time.Time) error

	// All enabled tasks across all companies. Used by the worker-side
	// PeriodicTaskManager config provider; returns a snapshot.
	ListEnabledForScheduler(ctx context.Context) ([]*ScheduledTask, error)

	// Runs
	AppendRun(ctx context.Context, r *ScheduledTaskRun) error
	UpdateRun(ctx context.Context, r *ScheduledTaskRun) error
	GetRun(ctx context.Context, id string) (*ScheduledTaskRun, error)
	ListRunsByTask(ctx context.Context, taskID string, limit, offset int) ([]*ScheduledTaskRun, error)
}
