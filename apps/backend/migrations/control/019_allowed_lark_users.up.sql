-- Allowlist of Lark open_ids per company. Mirrors allowed_discord_users.
-- A Lark open_id is per-app, but the same human can be allowed by multiple
-- companies via separate apps, so the primary key is composite.

CREATE TABLE IF NOT EXISTS allowed_lark_users (
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    lark_open_id   TEXT NOT NULL,
    label          TEXT,
    added_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, lark_open_id)
);

CREATE INDEX IF NOT EXISTS idx_allowed_lark_users_open_id
    ON allowed_lark_users(lark_open_id);
