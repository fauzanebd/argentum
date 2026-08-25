-- Share links for native dashboards (T-D13).
--
-- A row is a bearer credential and this is the only place in the product where
-- an unauthenticated request causes a query against a customer's production
-- database. Everything below follows from that.
--
--   * The token is never stored. token_hash is SHA-256 of it — 050's argument,
--     unchanged: 256 uniformly random bits have no dictionary behind them, so a
--     KDF buys nothing and costs 64 MiB on every page view of a public URL,
--     which is a denial of service handed to anyone who can type a wrong token.
--   * `password_hash` is the opposite case and uses a different primitive on
--     purpose. It is Argon2id (internal/auth/password.go), because a
--     human-chosen password *does* have a dictionary behind it. Two secrets on
--     one row, two KDF decisions, and the reason they differ is the entropy of
--     the input rather than the sensitivity of the thing guarded.
--   * Expiry and revocation are separate columns because they are separate
--     decisions: expires_at bounds the link nobody remembers, revoked_at is the
--     button pressed at 11pm, and a link that can only expire cannot be taken
--     back.
--   * company_id is denormalised even though dashboard_id implies it. The
--     lookup has no tenant to scope by, so the company comes *out* of this
--     table and everything the page then reads is bounded by what came back —
--     never by anything the request said.
--
-- Four columns are genuinely new against report_shares:
--
--   * locked_params — pinned filter values. They are LOCKED, NEVER MERGED. A
--     dashboard shared with region pinned to Jakarta shows Jakarta, and a
--     visitor editing the query string still sees Jakarta, because request
--     parameters on a share are ignored rather than merged. Merging is the
--     obvious implementation and it turns every declared filter into a
--     dimension a stranger may enumerate.
--   * allow_filters — whether the visitor may move the filters that are not
--     pinned. Default false: the safe end.
--   * password_hash — optional second factor, above.
--   * max_refresh_per_hour — a bearer link that can spend a customer's
--     warehouse without limit is a leaked link that costs money forever.

CREATE TABLE IF NOT EXISTS dashboard_shares (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id           UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    dashboard_id         UUID NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    -- Unique: two shares hashing the same is either a repeat of one token or a
    -- SHA-256 collision, and both should fail an insert rather than make the
    -- lookup ambiguous.
    token_hash           TEXT NOT NULL UNIQUE,
    locked_params        JSONB NOT NULL DEFAULT '{}'::jsonb,
    allow_filters        BOOLEAN NOT NULL DEFAULT false,
    password_hash        TEXT,
    max_refresh_per_hour INTEGER NOT NULL DEFAULT 60,
    created_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ NOT NULL,
    revoked_at           TIMESTAMPTZ,
    view_count           INTEGER NOT NULL DEFAULT 0,
    last_viewed_at       TIMESTAMPTZ
);

-- The hot path: one lookup per page view, by hash alone.
CREATE INDEX IF NOT EXISTS idx_dashboard_shares_token ON dashboard_shares(token_hash);

-- The dashboard's list for one dashboard, newest first.
CREATE INDEX IF NOT EXISTS idx_dashboard_shares_dashboard
    ON dashboard_shares(company_id, dashboard_id, created_at DESC);
