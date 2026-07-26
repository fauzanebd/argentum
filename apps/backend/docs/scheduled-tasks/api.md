# Scheduled Tasks

A **scheduled task** is a saved prompt that the agent runs on a cron schedule.
Use them when you want the assistant to deliver something automatically — for
example "give me a sales report every Monday at 07:00", "summarise refunds at
the end of each month", or "post yesterday's signups every weekday at 09:00".

Each scheduled task owns its own dedicated thread. Every fire writes its
output into that thread, so the run history reads like a normal conversation.
A separate runs API exposes status + the assistant message id for each fire.

---

## How a task gets created

There are two equivalent ways to create one:

1. **Through the chat agent.** Ask the agent to schedule something. The agent
   has access to a `schedule_task` tool with these parameters:

   - `name` — short title.
   - `prompt` — the exact instruction the agent will run on every fire.
   - `cron_expression` — standard 5-field cron (`minute hour dom month dow`).
   - `timezone` — IANA name (e.g. `Asia/Jakarta`). Defaults to `UTC`.

   When your request is ambiguous about *what* to run, *when* to run it, or
   *which timezone*, the agent will ask you to clarify before scheduling.
   Otherwise it schedules immediately and replies with the new `task_id`.

2. **Through the REST API.** See `POST /api/scheduled-tasks` below.

---

## Endpoints

All endpoints require the same auth as the rest of `/api/*` (Bearer token).
Tasks are scoped to the authenticated company; cross-company access returns
`403`.

### List tasks

```
GET /api/scheduled-tasks
```

Returns every scheduled task owned by your company, newest first.

```json
{
  "tasks": [
    {
      "id": "0f8b9c2e-…",
      "company_id": "5b4cda7e-…",
      "user_id": "9a30…",
      "thread_id": "1c2f…",
      "name": "Weekly sales report",
      "prompt": "Show me sales totals for last week, grouped by product.",
      "cron_expression": "0 7 * * 1",
      "timezone": "Asia/Jakarta",
      "enabled": true,
      "last_run_at": "2026-05-04T00:00:00Z",
      "next_run_at": "2026-05-11T00:00:00Z",
      "created_at": "2026-04-12T10:32:11.213Z",
      "updated_at": "2026-05-04T00:01:02.847Z"
    }
  ]
}
```

### Create a task

```
POST /api/scheduled-tasks
```

Body:

```json
{
  "name": "Weekly sales report",
  "prompt": "Show me sales totals for last week, grouped by product.",
  "cron_expression": "0 7 * * 1",
  "timezone": "Asia/Jakarta"
}
```

A new dedicated thread is created automatically and stored on the task as
`thread_id`. `201 Created` returns the full task record.

Validation:

- `name`, `prompt`, `cron_expression` — required.
- `cron_expression` — must parse as a 5-field cron. `0 7 * * 1` (Mondays
  07:00), `*/15 * * * *` (every 15 minutes), `0 0 1 * *` (1st of each month).
- `timezone` — optional, must be a valid IANA name; defaults to `UTC`.

### Get one task

```
GET /api/scheduled-tasks/{id}
```

### Update a task

```
PATCH /api/scheduled-tasks/{id}
```

Body — every field is optional:

```json
{
  "name": "...",
  "prompt": "...",
  "cron_expression": "...",
  "timezone": "...",
  "enabled": false
}
```

Setting `enabled: false` keeps the row but stops it from firing on the next
sync (~30 s). Setting it back to `true` resumes firing.

### Delete a task

```
DELETE /api/scheduled-tasks/{id}
```

`204 No Content`. The associated runs are cascade-deleted; the dedicated
thread is intentionally left alone so you keep the run history.

### List runs for a task

```
GET /api/scheduled-tasks/{id}/runs?limit=50&offset=0
```

```json
{
  "runs": [
    {
      "id": "ab12…",
      "task_id": "0f8b9c2e-…",
      "company_id": "5b4cda7e-…",
      "status": "succeeded",
      "assistant_msg_id": "f17e…",
      "started_at": "2026-05-04T00:00:00.123Z",
      "finished_at": "2026-05-04T00:00:08.541Z"
    }
  ]
}
```

`status` is one of `running`, `succeeded`, `failed`. `assistant_msg_id` is
the `messages` row holding the agent's reply for that fire (`null` while
running, or for runs that errored before any reply was produced).

### Get one run

```
GET /api/scheduled-tasks/{id}/runs/{runID}
```

Returns the same shape as a single entry in the list. Pair it with
`GET /api/threads/{thread_id}/messages` to fetch the actual assistant text.

---

## Errors

| Status | When | Body |
|--------|------|------|
| `400 Bad Request` | invalid cron, invalid timezone, empty name/prompt | `{"error": "..."}` |
| `403 Forbidden` | task belongs to another company | `{"error": "unauthorized"}` |
| `404 Not Found` | unknown task or run id | `{"error": "not found"}` |

---

## How firings work (briefly)

- The worker process hosts `asynq.PeriodicTaskManager`, which polls the
  `scheduled_tasks` table every ~30 seconds and registers one cron entry per
  enabled row.
- At each cron tick, a `scheduled:run` task is enqueued. The worker handler
  opens a `scheduled_task_runs` row (status `running`), appends the saved
  prompt to the dedicated thread as a user message, and enqueues a regular
  `chat:run` against the agent.
- When the agent finishes, the assistant message id is recorded on the run
  row and `status` flips to `succeeded`. If the agent errors, status is
  `failed` with `error_message`.
