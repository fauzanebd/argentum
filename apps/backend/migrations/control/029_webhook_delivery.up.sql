-- Signed outbound callbacks and the log of what we sent (T-A2).
--
-- `POST /v1/reports` takes an optional callback_url, which makes Argentum an
-- HTTP *client* against a tenant's server for the first time. Two things have
-- to exist before that is safe to offer: a way for the receiver to prove the
-- body came from us, and a record on our side of what we sent and how it went.
-- Without the second, "we never got the callback" is an unanswerable support
-- ticket.
--
-- T-15 subscribes watcher events to this table rather than building a second
-- sender, which is why nothing here names reports.

-- The signing secret, one per tenant, minted on first use rather than at
-- signup — a column of secrets for companies that will never receive a
-- callback is a liability with no user.
--
-- NOT NULL DEFAULT '' so "not yet minted" has one representation. It is never
-- returned by an /api route: the dashboard has no reason to hold it, and the
-- only caller that needs it is the integration verifying a signature.
ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS webhook_secret TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- The event name in the signed body, e.g. 'report.completed'. Stored
    -- separately from the payload so the log can be filtered without parsing
    -- JSON out of every row.
    event        TEXT NOT NULL,
    url          TEXT NOT NULL,
    -- Exactly the bytes that were signed. A tenant debugging a signature
    -- mismatch needs the bytes, not a re-serialisation of them — re-marshalled
    -- JSON with different key order verifies against nothing.
    payload      BYTEA NOT NULL,
    -- pending | delivered | failed
    status       TEXT NOT NULL DEFAULT 'pending',
    attempts     INT NOT NULL DEFAULT 0,
    -- The receiver's last HTTP status, or 0 when the request never got one
    -- (DNS, TLS, timeout). That distinction is the first thing to look at.
    last_status  INT NOT NULL DEFAULT 0,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ
);

-- "What did you send us, and when?" — the tenant-scoped question this table
-- exists to answer.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_company
    ON webhook_deliveries(company_id, created_at DESC);
