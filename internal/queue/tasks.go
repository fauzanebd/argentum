// Package queue defines the asynq task contract for background work.
//
// Tasks today:
//   - `chat:run`           — process one user message through the agent.
//   - `scheduled:run`      — fired by asynq.PeriodicTaskManager for each
//                            cron tick of an enabled scheduled_tasks row;
//                            the worker resolves the task and re-enqueues
//                            a `chat:run` against the dedicated thread.
//
// Payloads are JSON-marshalled into the asynq task body.
package queue

import "github.com/fauzanebd/argentum/internal/domain"

// Task type constants. These are the values asynq uses to dispatch tasks
// to handlers; keep them stable across deploys.
const (
	TypeChatRun         = "chat:run"
	TypeScheduledTaskRun = "scheduled:run"
)

// ChatRunPayload carries everything the worker needs to process one chat
// turn. UserMsgID lets the worker re-derive the original message row in
// case retries fire after the user message has been persisted but before
// the assistant reply was written.
//
// ScheduledTaskID/ScheduledRunID are populated only when the run was
// triggered by a cron-scheduled task; ChatRunner uses them to update the
// matching scheduled_task_runs row with the assistant message id.
type ChatRunPayload struct {
	CompanyID       string         `json:"company_id"`
	ThreadID        string         `json:"thread_id"`
	UserID          string         `json:"user_id,omitempty"`
	PhoneNumber     string         `json:"phone_number,omitempty"`
	Channel         domain.Channel `json:"channel"`
	Message         string         `json:"message"`
	UserMsgID       string         `json:"user_msg_id"`
	CompanyName     string         `json:"company_name,omitempty"`
	DefaultCurrency string         `json:"default_currency,omitempty"` // ISO 4217
	ScheduledTaskID string         `json:"scheduled_task_id,omitempty"`
	ScheduledRunID  string         `json:"scheduled_run_id,omitempty"`
}

// ScheduledRunPayload is the body of a `scheduled:run` task. Only the
// task ID is sent; the worker reloads the full ScheduledTask from the
// database so cron firings always use the latest prompt/timezone/etc.
type ScheduledRunPayload struct {
	TaskID string `json:"task_id"`
}
