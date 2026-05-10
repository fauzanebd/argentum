package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// ScheduledTaskCreator is the narrow contract this tool needs. Defined
// here (and not in internal/app) to avoid an import cycle: internal/app
// already depends on internal/tools.
type ScheduledTaskCreator interface {
	CreateScheduledTask(ctx context.Context, in CreateScheduledTaskInput) (*ScheduledTaskResult, error)
}

// CreateScheduledTaskInput mirrors the agent-facing tool parameters.
type CreateScheduledTaskInput struct {
	CompanyID      string
	UserID         string
	Name           string
	Prompt         string
	CronExpression string
	Timezone       string
}

// ScheduledTaskResult is what the tool surfaces back to the agent.
type ScheduledTaskResult struct {
	TaskID         string     `json:"task_id"`
	Name           string     `json:"name"`
	CronExpression string     `json:"cron_expression"`
	Timezone       string     `json:"timezone"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	ThreadID       string     `json:"thread_id"`
}

// ScheduleTaskTool lets the LLM register a cron-driven recurring prompt
// for the current tenant. Each fire reuses a dedicated thread per task,
// so the run history is browsable as a normal conversation.
//
// Per product decision the tool deliberately does NOT return a frontend
// link — the dashboard renders the task by ID via /api/scheduled-tasks.
type ScheduleTaskTool struct {
	creator ScheduledTaskCreator
}

func NewScheduleTaskTool(creator ScheduledTaskCreator) *ScheduleTaskTool {
	return &ScheduleTaskTool{creator: creator}
}

func (t *ScheduleTaskTool) Name() string { return "schedule_task" }

func (t *ScheduleTaskTool) Description() string {
	return "Create a recurring scheduled task. Each run executes the supplied prompt through this agent " +
		"and writes the result to a dedicated thread tied to the task. " +
		"Use ONLY when the user has clearly specified WHAT to run, WHEN (cron), and (optionally) the timezone. " +
		"If any of those are ambiguous, ASK the user to clarify before calling this tool. " +
		"Returns the new task_id; the dashboard surfaces task details and run history via /api/scheduled-tasks."
}

func (t *ScheduleTaskTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"name": {
			Type:        "string",
			Description: "Short human-readable name for the task (e.g. 'Weekly sales report').",
			Required:    true,
		},
		"prompt": {
			Type:        "string",
			Description: "The exact instruction the agent will run on every fire (e.g. 'Show me sales totals for last week, grouped by product').",
			Required:    true,
		},
		"cron_expression": {
			Type:        "string",
			Description: "Standard 5-field cron expression in 'minute hour dom month dow' order. Examples: '0 7 * * 1' = Mondays 07:00; '0 9 1 * *' = 1st of each month 09:00; '*/15 * * * *' = every 15 minutes.",
			Required:    true,
		},
		"timezone": {
			Type:        "string",
			Description: "IANA timezone name applied to the cron expression. Defaults to UTC. Examples: 'Asia/Jakarta', 'Europe/London', 'America/New_York'.",
			Required:    false,
		},
	}
}

func (t *ScheduleTaskTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *ScheduleTaskTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Name           string `json:"name"`
		Prompt         string `json:"prompt"`
		CronExpression string `json:"cron_expression"`
		Timezone       string `json:"timezone"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context")
	}
	userID := tenantctx.UserID(ctx)
	if userID == "" {
		return "", fmt.Errorf("schedule_task requires a dashboard user; cannot be created from a WhatsApp-only thread")
	}

	res, err := t.creator.CreateScheduledTask(ctx, CreateScheduledTaskInput{
		CompanyID:      companyID,
		UserID:         userID,
		Name:           params.Name,
		Prompt:         params.Prompt,
		CronExpression: params.CronExpression,
		Timezone:       params.Timezone,
	})
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(res)
	return string(out), nil
}
