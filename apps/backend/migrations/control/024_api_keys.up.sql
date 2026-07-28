-- Scoped API keys (T-13, finding P-2).
--
-- Filed as T-13's `025_api_keys`, renumbered to 024: golang-migrate only
-- applies versions above the schema's current one, and 023 is the highest
-- applied. This is the fourth consecutive ticket whose reserved number was
-- already spent by a re-ordered plan; the rule is to take the next free
-- number, not the one the ticket names.
--
-- Every route in this product requires a human session JWT, so nothing can
-- integrate with it — not the tenant's own backend, not another agent. This
-- table is the only machine credential Argentum has.
--
-- The secret is never stored. `key_prefix` is the public half — it is what
-- the dashboard lists and what a lookup keys on — and `key_hash` is a
-- SHA-256 of the 256-bit secret half. See internal/auth/apikey.go for why
-- that is a SHA-256 and not the Argon2id the ticket named: a uniformly
-- random 256-bit secret has no dictionary to slow an attacker against, and
-- Argon2id's 64 MiB working set would land on every authenticated API
-- request rather than on an occasional login.

CREATE TABLE IF NOT EXISTS api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    -- Public half of the token, `arg_<prefix>_<secret>`. UNIQUE because it is
    -- the lookup key: a collision would make one key's secret checkable
    -- against another key's hash.
    key_prefix   TEXT NOT NULL UNIQUE,
    key_hash     TEXT NOT NULL,
    -- Deny by default: an empty array grants nothing. TEXT[] rather than a
    -- join table because scopes are a closed vocabulary owned by the Go code,
    -- validated on write, and never queried across keys.
    scopes       TEXT[] NOT NULL DEFAULT '{}',
    -- SET NULL, not CASCADE: removing the admin who minted a key must not
    -- delete the key, or an offboarding silently breaks a tenant's
    -- integration. The same reasoning as user_invites.invited_by.
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Written at most once a minute per key, not per request.
    last_used_at TIMESTAMPTZ,
    -- NULL means no expiry. A key that never expires is the common case for a
    -- server-to-server integration; forcing rotation with no rotation tooling
    -- would just produce keys that expire at 3am.
    expires_at   TIMESTAMPTZ,
    -- Revocation is a tombstone, not a DELETE. "Which key was that?" has to
    -- stay answerable from an audit row long after the key stops working.
    revoked_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The dashboard's list: one company's keys, newest first.
CREATE INDEX IF NOT EXISTS idx_api_keys_company_created
    ON api_keys(company_id, created_at DESC);

-- Authentication reads by prefix alone, and key_prefix is already UNIQUE, so
-- that path needs no further index.
