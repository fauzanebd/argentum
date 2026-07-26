package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type ScheduledTaskRepo struct{ db *sql.DB }

func NewScheduledTaskRepo(db *sql.DB) *ScheduledTaskRepo { return &ScheduledTaskRepo{db: db} }

const taskCols = `id, company_id, COALESCE(user_id::text, ''), thread_id, name, prompt,
		cron_expression, timezone, enabled, last_run_at, next_run_at, created_at, updated_at`

func scanTask(row interface {
	Scan(dest ...interface{}) error
}) (*domain.ScheduledTask, error) {
	t := &domain.ScheduledTask{}
	var lastRun, nextRun sql.NullTime
	if err := row.Scan(
		&t.ID, &t.CompanyID, &t.UserID, &t.ThreadID, &t.Name, &t.Prompt,
		&t.CronExpression, &t.Timezone, &t.Enabled, &lastRun, &nextRun,
		&t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if lastRun.Valid {
		v := lastRun.Time
		t.LastRunAt = &v
	}
	if nextRun.Valid {
		v := nextRun.Time
		t.NextRunAt = &v
	}
	return t, nil
}

func (r *ScheduledTaskRepo) CreateTask(ctx context.Context, t *domain.ScheduledTask) error {
	const q = `
		INSERT INTO scheduled_tasks
			(company_id, user_id, thread_id, name, prompt, cron_expression, timezone, enabled)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		t.CompanyID, t.UserID, t.ThreadID, t.Name, t.Prompt,
		t.CronExpression, t.Timezone, t.Enabled,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return fmt.Errorf("insert scheduled_task: %w", err)
	}
	return nil
}

func (r *ScheduledTaskRepo) GetTask(ctx context.Context, id string) (*domain.ScheduledTask, error) {
	q := `SELECT ` + taskCols + ` FROM scheduled_tasks WHERE id = $1`
	t, err := scanTask(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

func (r *ScheduledTaskRepo) ListTasksByCompany(ctx context.Context, companyID string) ([]*domain.ScheduledTask, error) {
	q := `SELECT ` + taskCols + ` FROM scheduled_tasks WHERE company_id = $1 ORDER BY created_at DESC`
	return r.queryTasks(ctx, q, companyID)
}

func (r *ScheduledTaskRepo) ListTasksByUser(ctx context.Context, companyID, userID string) ([]*domain.ScheduledTask, error) {
	q := `SELECT ` + taskCols + ` FROM scheduled_tasks WHERE company_id = $1 AND user_id = $2 ORDER BY created_at DESC`
	return r.queryTasks(ctx, q, companyID, userID)
}

func (r *ScheduledTaskRepo) ListEnabledForScheduler(ctx context.Context) ([]*domain.ScheduledTask, error) {
	q := `SELECT ` + taskCols + ` FROM scheduled_tasks WHERE enabled = true ORDER BY id`
	return r.queryTasks(ctx, q)
}

func (r *ScheduledTaskRepo) queryTasks(ctx context.Context, q string, args ...interface{}) ([]*domain.ScheduledTask, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ScheduledTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ScheduledTaskRepo) UpdateTask(ctx context.Context, t *domain.ScheduledTask) error {
	const q = `
		UPDATE scheduled_tasks SET
			name = $1,
			prompt = $2,
			cron_expression = $3,
			timezone = $4,
			enabled = $5,
			updated_at = now()
		WHERE id = $6
	`
	_, err := r.db.ExecContext(ctx, q, t.Name, t.Prompt, t.CronExpression, t.Timezone, t.Enabled, t.ID)
	return err
}

func (r *ScheduledTaskRepo) DeleteTask(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id = $1`, id)
	return err
}

func (r *ScheduledTaskRepo) SetTaskEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET enabled = $1, updated_at = now() WHERE id = $2`, enabled, id)
	return err
}

func (r *ScheduledTaskRepo) TouchTaskRunTimes(ctx context.Context, id string, lastRun, nextRun time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET last_run_at = $1, next_run_at = $2, updated_at = now() WHERE id = $3`,
		lastRun, nextRun, id)
	return err
}

// --- runs ---

func (r *ScheduledTaskRepo) AppendRun(ctx context.Context, run *domain.ScheduledTaskRun) error {
	const q = `
		INSERT INTO scheduled_task_runs (task_id, company_id, status, error_message)
		VALUES ($1, $2, $3, $4)
		RETURNING id, started_at
	`
	return r.db.QueryRowContext(ctx, q,
		run.TaskID, run.CompanyID, run.Status, run.ErrorMessage,
	).Scan(&run.ID, &run.StartedAt)
}

func (r *ScheduledTaskRepo) UpdateRun(ctx context.Context, run *domain.ScheduledTaskRun) error {
	const q = `
		UPDATE scheduled_task_runs SET
			status = $1,
			assistant_msg_id = NULLIF($2, '')::uuid,
			error_message = $3,
			finished_at = $4
		WHERE id = $5
	`
	var msgID string
	if run.AssistantMsgID != nil {
		msgID = *run.AssistantMsgID
	}
	var finished interface{}
	if run.FinishedAt != nil {
		finished = *run.FinishedAt
	}
	_, err := r.db.ExecContext(ctx, q, run.Status, msgID, run.ErrorMessage, finished, run.ID)
	return err
}

func (r *ScheduledTaskRepo) GetRun(ctx context.Context, id string) (*domain.ScheduledTaskRun, error) {
	const q = `
		SELECT id, task_id, company_id, status, COALESCE(assistant_msg_id::text, ''),
			error_message, started_at, finished_at
		FROM scheduled_task_runs WHERE id = $1
	`
	row := r.db.QueryRowContext(ctx, q, id)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return run, err
}

func (r *ScheduledTaskRepo) ListRunsByTask(ctx context.Context, taskID string, limit, offset int) ([]*domain.ScheduledTaskRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT id, task_id, company_id, status, COALESCE(assistant_msg_id::text, ''),
			error_message, started_at, finished_at
		FROM scheduled_task_runs
		WHERE task_id = $1
		ORDER BY started_at DESC LIMIT $2 OFFSET $3
	`
	rows, err := r.db.QueryContext(ctx, q, taskID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ScheduledTaskRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func scanRun(row interface {
	Scan(dest ...interface{}) error
}) (*domain.ScheduledTaskRun, error) {
	run := &domain.ScheduledTaskRun{}
	var msgID string
	var finished sql.NullTime
	if err := row.Scan(
		&run.ID, &run.TaskID, &run.CompanyID, &run.Status, &msgID,
		&run.ErrorMessage, &run.StartedAt, &finished,
	); err != nil {
		return nil, err
	}
	if msgID != "" {
		run.AssistantMsgID = &msgID
	}
	if finished.Valid {
		v := finished.Time
		run.FinishedAt = &v
	}
	return run, nil
}
