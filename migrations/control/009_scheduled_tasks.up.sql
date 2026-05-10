-- Cron-scheduled prompts. The agent (via the schedule_task tool) or the
-- dashboard can register a task; a worker-side asynq.PeriodicTaskManager
-- polls this table and fires a chat:run for each enabled row at every
-- cron tick.
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    thread_id       UUID NOT NULL REFERENCES conversation_threads(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    cron_expression TEXT NOT NULL,
    timezone        TEXT NOT NULL DEFAULT 'UTC',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_run_at     TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sched_company ON scheduled_tasks(company_id, enabled);
CREATE INDEX IF NOT EXISTS idx_sched_user    ON scheduled_tasks(company_id, user_id);

-- One row per fire, with a pointer to the assistant message produced by the
-- agent. assistant_msg_id stays NULL while the run is in flight or if the
-- run failed before any assistant message was persisted.
CREATE TABLE IF NOT EXISTS scheduled_task_runs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id           UUID NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    company_id        UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    status            TEXT NOT NULL,
    assistant_msg_id  UUID REFERENCES messages(id) ON DELETE SET NULL,
    error_message     TEXT NOT NULL DEFAULT '',
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_sched_runs_task ON scheduled_task_runs(task_id, started_at DESC);
