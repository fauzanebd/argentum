DROP INDEX IF EXISTS idx_threads_discord;

ALTER TABLE conversation_threads
    DROP COLUMN IF EXISTS discord_user_id;
