-- Control plane: usage events and soft credit balance.
CREATE TABLE IF NOT EXISTS usage_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    thread_id       UUID REFERENCES conversation_threads(id) ON DELETE SET NULL,
    message_id      UUID REFERENCES messages(id) ON DELETE SET NULL,
    event_type      TEXT NOT NULL,
    tokens_in       INTEGER NOT NULL DEFAULT 0,
    tokens_out      INTEGER NOT NULL DEFAULT 0,
    cost_micro_usd  BIGINT NOT NULL DEFAULT 0,
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_usage_events_company_recent
    ON usage_events(company_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_events_thread
    ON usage_events(thread_id) WHERE thread_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS company_credits (
    company_id              UUID PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    balance_micro_usd       BIGINT NOT NULL DEFAULT 0,
    monthly_grant_micro_usd BIGINT NOT NULL DEFAULT 0,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
