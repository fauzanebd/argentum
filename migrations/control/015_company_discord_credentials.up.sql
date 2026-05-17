-- Per-tenant Discord bot credentials. One row per company.
-- Token is encrypted with the same AES-256-GCM cipher used for DSNs.
-- public_key is the Ed25519 hex string the Discord dev portal exposes;
-- used only by the interactions HTTP webhook to verify request signatures.

CREATE TABLE IF NOT EXISTS company_discord_credentials (
    company_id          UUID PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    application_id      TEXT NOT NULL,
    public_key          TEXT NOT NULL,
    bot_token_encrypted BYTEA NOT NULL,
    guild_id            TEXT,
    enabled             BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_company_discord_credentials_app
    ON company_discord_credentials(application_id);
