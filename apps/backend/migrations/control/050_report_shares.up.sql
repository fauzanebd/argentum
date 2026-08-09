-- Share links for the report player (T-V4).
--
-- A row is a bearer credential: whoever holds the token opens the page, with
-- no session and no tenant of their own. Three consequences are in the schema
-- rather than in the handler that reads it.
--
--   * The token is never stored. token_hash is SHA-256 of it — the argument is
--     T-13's, unchanged: 256 uniformly random bits have no dictionary behind
--     them, so a KDF buys nothing and costs 64 MiB on every page view.
--   * Expiry and revocation are separate columns because they are separate
--     decisions. expires_at bounds the link nobody remembers; revoked_at is
--     the button pressed at 11pm, and a link that can only expire cannot be
--     taken back.
--   * company_id is denormalised onto the row even though document_id implies
--     it. The `GET /share/:token` lookup has no tenant to scope by, so the
--     company comes *out* of this table and everything the page then reads is
--     bounded by it.

CREATE TABLE IF NOT EXISTS report_shares (
    id             UUID PRIMARY KEY,
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    document_id    UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    -- Unique: two shares hashing the same is either a repeat of one token or a
    -- SHA-256 collision, and both should fail an insert rather than make the
    -- lookup ambiguous.
    token_hash     TEXT NOT NULL UNIQUE,
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ NOT NULL,
    revoked_at     TIMESTAMPTZ,
    view_count     INTEGER NOT NULL DEFAULT 0,
    last_viewed_at TIMESTAMPTZ
);

-- The hot path: one lookup per page view, by hash alone.
CREATE INDEX IF NOT EXISTS idx_report_shares_token ON report_shares(token_hash);

-- The dashboard's list for one document, newest first.
CREATE INDEX IF NOT EXISTS idx_report_shares_document
    ON report_shares(company_id, document_id, created_at DESC);

-- ON DELETE CASCADE on document_id is the deliberate one: deleting a document
-- must take its links with it. A share that outlived its document would
-- resolve to a page with nothing to play, and the alternative — a dangling row
-- somebody has to clean up — is how a revoked-looking link stays alive.
