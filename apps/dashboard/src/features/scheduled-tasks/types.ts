export interface ScheduledTask {
  id: string;
  company_id: string;
  user_id: string;
  thread_id: string;
  name: string;
  prompt: string;
  cron_expression: string;
  timezone: string;
  enabled: boolean;
  last_run_at: string | null;
  next_run_at: string | null;
  created_at: string;
  updated_at: string;
}

export type RunStatus = "running" | "succeeded" | "failed";

export interface TaskRun {
  id: string;
  task_id: string;
  company_id: string;
  status: RunStatus;
  assistant_msg_id: string | null;
  error_message?: string;
  started_at: string;
  finished_at: string | null;
}
