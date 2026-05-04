-- Control plane: conversation threads and messages.
CREATE TABLE IF NOT EXISTS conversation_threads (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    channel         TEXT NOT NULL,
    phone_number    TEXT,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    title           TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_archived     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_threads_company_recent
    ON conversation_threads(company_id, last_message_at DESC);

CREATE INDEX IF NOT EXISTS idx_threads_phone_recent
    ON conversation_threads(company_id, phone_number, last_message_at DESC)
    WHERE phone_number IS NOT NULL AND NOT is_archived;

CREATE INDEX IF NOT EXISTS idx_threads_user_recent
    ON conversation_threads(company_id, user_id, last_message_at DESC)
    WHERE user_id IS NOT NULL AND NOT is_archived;

CREATE TABLE IF NOT EXISTS messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id   UUID NOT NULL REFERENCES conversation_threads(id) ON DELETE CASCADE,
    role        TEXT NOT NULL,
    content     TEXT NOT NULL,
    tool_calls  JSONB,
    tokens_in   INTEGER NOT NULL DEFAULT 0,
    tokens_out  INTEGER NOT NULL DEFAULT 0,
    latency_ms  BIGINT NOT NULL DEFAULT 0,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id, created_at);
