DROP INDEX IF EXISTS idx_threads_slack_user;
DROP INDEX IF EXISTS idx_threads_slack_thread;

ALTER TABLE conversation_threads
    DROP COLUMN IF EXISTS slack_team_id,
    DROP COLUMN IF EXISTS slack_channel_id,
    DROP COLUMN IF EXISTS slack_thread_ts,
    DROP COLUMN IF EXISTS slack_user_id;
