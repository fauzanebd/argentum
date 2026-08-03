-- T-15: per-company subscriptions to the events a workspace produces.
--
-- Delivery already exists. `T-A2` built internal/webhookout for report
-- callbacks — HMAC over `<timestamp>.<body>`, asynq retry with backoff, an
-- SSRF refusal on our own network, and a row per attempt — and this ticket
-- subscribes events to it rather than writing a second signer or a second retry
-- loop. So there is no secret column here: the signing secret is the company's,
-- on companies.webhook_secret, minted on first use.
CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    url        TEXT NOT NULL,
    -- The events this subscription wants. Empty matches nothing and the service
    -- refuses to store one — the opposite of an agent's tool allowlist, where
    -- empty means every tool. There, an empty list widens what an agent may do
    -- inside Argentum; here it would widen what leaves it.
    events     TEXT[] NOT NULL DEFAULT '{}',
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,

    -- Health. consecutive_failures counts terminal failures since the last
    -- delivery that landed; twenty of them disables the subscription and writes
    -- the reason, so the settings screen can tell "you turned this off" from
    -- "we did".
    consecutive_failures INT NOT NULL DEFAULT 0,
    disabled_reason      TEXT NOT NULL DEFAULT '',
    last_success_at      TIMESTAMPTZ,
    last_failure_at      TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The fan-out's only read: one company's enabled subscriptions, on the hot path
-- of every watcher breach.
CREATE INDEX IF NOT EXISTS idx_webhook_subscriptions_company
    ON webhook_subscriptions (company_id) WHERE enabled;

-- Which subscription a delivery came from, so the worker can count failures
-- against it. NULL for a `report.completed` callback, which belongs to one
-- request rather than to a standing subscription — and ON DELETE SET NULL
-- because a delivery log outlives the subscription that caused it and deleting
-- a subscription must not delete the record of what it sent.
ALTER TABLE webhook_deliveries
    ADD COLUMN IF NOT EXISTS subscription_id UUID REFERENCES webhook_subscriptions(id) ON DELETE SET NULL;
