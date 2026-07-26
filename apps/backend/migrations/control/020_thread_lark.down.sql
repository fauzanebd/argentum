DROP INDEX IF EXISTS idx_threads_lark;

ALTER TABLE conversation_threads
    DROP COLUMN IF EXISTS lark_chat_id,
    DROP COLUMN IF EXISTS lark_thread_key,
    DROP COLUMN IF EXISTS lark_open_id;
