DROP INDEX IF EXISTS idx_threads_api_user;

ALTER TABLE conversation_threads
    DROP COLUMN IF EXISTS api_user_ref;
