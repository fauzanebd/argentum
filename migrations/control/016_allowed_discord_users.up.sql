-- Allowlist of Discord user IDs per company. Mirrors allowed_phone_numbers.
-- A Discord user id is globally unique but the same user might belong to
-- multiple companies, so the primary key is composite.

CREATE TABLE IF NOT EXISTS allowed_discord_users (
    company_id       UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    discord_user_id  TEXT NOT NULL,
    label            TEXT,
    added_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, discord_user_id)
);

CREATE INDEX IF NOT EXISTS idx_allowed_discord_users_user
    ON allowed_discord_users(discord_user_id);
