-- Embed keys (T-19): the browser-visible half of putting Argentum's chat
-- inside a tenant's own site.
--
-- Filed as `*_embed_keys`, "next free on landing" — 050 is report_shares, so
-- this is 051. The ticket's original 028 was spent by api_reports in T-A2.
--
-- **Why this is not `api_keys` with an extra column.** An API key is held by a
-- server, is broadly scoped, and authorises by secret alone. An embed key is
-- *published to a browser*: its public half appears in the tenant's page
-- source, and it authorises nothing on its own. What authorises is the
-- combination of an origin we allowlisted and an HMAC their backend computed
-- over the identity they are asserting. Merging the two tables would put a
-- browser-visible credential in the same lookup path as a server-side one, and
-- the first mistake in that lookup leaks scope.
--
-- **`secret_enc` is encrypted, not hashed, and that is a deliberate deviation
-- from the ticket's `secret_hash` (Argon2id).** The ticket also specifies
-- `HMAC-SHA256(secret, "{user_ref}:{exp}")` recomputed on this side, and an
-- HMAC cannot be recomputed from a hash of its key — the two lines are not
-- jointly satisfiable. Of the two, the HMAC is the security model (it is what
-- stops a page forging an identity), so the storage gives way: the secret is
-- sealed with the same AES-256-GCM cipher that protects every tenant DSN, under
-- ARGENTUM_DSN_KEY. What that costs is that a database dump plus the key yields
-- signing secrets, which is the same exposure a dump plus the key already
-- yields for every warehouse credential in `connections`. Recorded in
-- docs/coverage/embed-auth.md, as T-13's SHA-256 deviation was.

CREATE TABLE IF NOT EXISTS embed_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id      UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    -- The public half: `argw_pub_<hex>`. It ships in the tenant's page source,
    -- is shown in the dashboard in full, and is what the session mint looks a
    -- key up by. UNIQUE because it is that lookup key.
    client_key      TEXT NOT NULL UNIQUE,
    -- AES-256-GCM sealed signing secret. Never leaves the server after the one
    -- response that mints it.
    secret_enc      BYTEA NOT NULL,
    -- Exact scheme://host[:port] entries. NOT NULL with no default: a key with
    -- no origins can mint no sessions, which is the safe direction for a row
    -- that somehow arrives empty. `*` is rejected in Go, not here — the check
    -- belongs where the error message can explain itself.
    allowed_origins TEXT[] NOT NULL,
    -- The pause switch, distinct from the tombstone below. An admin who is
    -- debugging a host page turns a key off and on; an admin who thinks a key
    -- has leaked revokes it and never gets it back.
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    -- SET NULL like api_keys.created_by: offboarding an admin must not delete
    -- a key their tenant's site is still using.
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Written at most once a minute per key, off the mint path.
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The dashboard's list: one company's keys, newest first.
CREATE INDEX IF NOT EXISTS idx_embed_keys_company_created
    ON embed_keys(company_id, created_at DESC);

-- The mint path reads by client_key alone, and that column is already UNIQUE.
