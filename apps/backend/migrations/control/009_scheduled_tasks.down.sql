-- Reversing 009 drops every schedule and its run history. Runs first: they
-- reference the task.
DROP TABLE IF EXISTS scheduled_task_runs;
DROP TABLE IF EXISTS scheduled_tasks;
