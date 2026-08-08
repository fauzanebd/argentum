-- Per-tenant Slack app credentials. One row per company.
-- bot_token is encrypted with the same AES-256-GCM cipher used for DSNs.
-- signing_secret comes from the Slack app's "Basic Information" page; the
-- webhook handler uses it to verify the v0 request signature.
-- bot_user_id is the bot's own user id (Uxxxx). It is optional: when unset
-- the webhook falls back to the `authorizations` array Slack sends on every
-- event callback, and persists what it learns.

CREATE TABLE IF NOT EXISTS company_slack_credentials (
    company_id           UUID PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    app_id               TEXT NOT NULL,
    team_id              TEXT,
    bot_token_encrypted  BYTEA NOT NULL,
    signing_secret       TEXT NOT NULL,
    bot_user_id          TEXT,
    enabled              BOOLEAN NOT NULL DEFAULT true,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The webhook resolves the tenant by app_id on every inbound event, and two
-- tenants sharing one Slack app would make that resolution ambiguous.
CREATE UNIQUE INDEX IF NOT EXISTS idx_company_slack_credentials_app
    ON company_slack_credentials(app_id);
