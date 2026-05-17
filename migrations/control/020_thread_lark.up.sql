-- Lark threads are keyed by lark_thread_key, which is thread_id || root_id ||
-- message_id (whichever the inbound event surfaces). One Lark reply-thread
-- maps 1:1 to one conversation_thread row (one thread = one agent memory).

ALTER TABLE conversation_threads
    ADD COLUMN IF NOT EXISTS lark_chat_id     TEXT,
    ADD COLUMN IF NOT EXISTS lark_thread_key  TEXT,
    ADD COLUMN IF NOT EXISTS lark_open_id     TEXT;

CREATE INDEX IF NOT EXISTS idx_threads_lark
    ON conversation_threads(company_id, lark_thread_key)
    WHERE channel = 'lark';
