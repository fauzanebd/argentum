// Package queue defines the asynq task contract for background work.
//
// Tasks today:
//   - `chat:run`           — process one user message through the agent.
//   - `scheduled:run`      — fired by asynq.PeriodicTaskManager for each
//     cron tick of an enabled scheduled_tasks row;
//     the worker resolves the task and re-enqueues
//     a `chat:run` against the dedicated thread.
//   - `report:render`      — a `POST /v1/reports/render` spec that overran the
//     synchronous window and became a job (T-A2).
//   - `webhook:deliver`    — one attempt at handing a tenant's server a signed
//     callback (T-A2).
//
// Payloads are JSON-marshalled into the asynq task body.
package queue

import (
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/spec"
)

// Task type constants. These are the values asynq uses to dispatch tasks
// to handlers; keep them stable across deploys.
const (
	TypeChatRun          = "chat:run"
	TypeScheduledTaskRun = "scheduled:run"
	TypeReportRender     = "report:render"
	TypeWebhookDeliver   = "webhook:deliver"
)

// ReportRenderPayload carries a spec whose synchronous render ran long
// (T-A2). The whole spec travels rather than a reference to a stored copy:
// the row it would otherwise be stored in is the report job, and putting a
// megabyte of caller-supplied JSON in a control-plane table to save a
// megabyte in Redis is the wrong trade — the task is transient and the row
// is not.
type ReportRenderPayload struct {
	ReportID  string        `json:"report_id"`
	CompanyID string        `json:"company_id"`
	APIKeyID  string        `json:"api_key_id,omitempty"`
	RequestID string        `json:"request_id,omitempty"`
	Spec      spec.Document `json:"spec"`
}

// WebhookDeliverPayload names the delivery row. Only the id: the URL, the
// body and the event all live in that row, and a payload that repeated them
// would be a second copy able to disagree with the log a tenant reads.
type WebhookDeliverPayload struct {
	DeliveryID string `json:"delivery_id"`
}

// ChatRunPayload carries everything the worker needs to process one chat
// turn. UserMsgID lets the worker re-derive the original message row in
// case retries fire after the user message has been persisted but before
// the assistant reply was written.
//
// ScheduledTaskID/ScheduledRunID are populated only when the run was
// triggered by a cron-scheduled task; ChatRunner uses them to update the
// matching scheduled_task_runs row with the assistant message id.
type ChatRunPayload struct {
	CompanyID        string         `json:"company_id"`
	ThreadID         string         `json:"thread_id"`
	UserID           string         `json:"user_id,omitempty"`
	PhoneNumber      string         `json:"phone_number,omitempty"`
	DiscordUserID    string         `json:"discord_user_id,omitempty"`
	DiscordChannelID string         `json:"discord_channel_id,omitempty"`
	LarkOpenID       string         `json:"lark_open_id,omitempty"`
	LarkChatID       string         `json:"lark_chat_id,omitempty"`
	LarkThreadKey    string         `json:"lark_thread_key,omitempty"`
	LarkMessageID    string         `json:"lark_message_id,omitempty"` // reply target
	Channel          domain.Channel `json:"channel"`
	Message          string         `json:"message"`
	UserMsgID        string         `json:"user_msg_id"`
	CompanyName      string         `json:"company_name,omitempty"`
	DefaultCurrency  string         `json:"default_currency,omitempty"` // ISO 4217
	ScheduledTaskID  string         `json:"scheduled_task_id,omitempty"`
	ScheduledRunID   string         `json:"scheduled_run_id,omitempty"`
	// APIReportID ties this turn to the report job `POST /v1/reports` handed
	// the caller (T-A2). The worker marks that row terminal when the turn
	// finishes, which is how "is my report ready?" gets an answer — a thread
	// id would not do, because a thread outlives the turn and accumulates more
	// of them.
	APIReportID string `json:"api_report_id,omitempty"`
	// APIKeyID attributes a turn started over /v1 to the key that started it
	// (T-13). The audit log records who a tool call ran for, and for an
	// integration that is a credential rather than a person — the queue is
	// what carries that fact from the HTTP request into the worker, which is
	// a different process and has no other way to learn it. The first writer
	// is T-A3's chat route; until then it is set only by tests.
	APIKeyID string `json:"api_key_id,omitempty"`
	// RequestID carries the `X-Request-Id` of the HTTP call that started this
	// turn (T-A1) into the audit rows the worker writes for it. Support
	// starts from a request id, so a request id has to resolve to rows — and
	// the rows are written in another process, minutes later, from a payload
	// that is the only thing crossing the gap.
	RequestID string `json:"request_id,omitempty"`
}

// ScheduledRunPayload is the body of a `scheduled:run` task. Only the
// task ID is sent; the worker reloads the full ScheduledTask from the
// database so cron firings always use the latest prompt/timezone/etc.
type ScheduledRunPayload struct {
	TaskID string `json:"task_id"`
}
