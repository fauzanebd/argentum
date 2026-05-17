-- Per-tenant Lark (Feishu) app credentials. One row per company.
-- app_secret is encrypted with the same AES-256-GCM cipher used for DSNs.
-- verification_token and encrypt_key come from the Lark developer console;
-- the webhook handler uses them to authenticate and decrypt event callbacks.
-- bot_open_id is the bot's own open_id, used to detect @mentions of the bot
-- in inbound events.

CREATE TABLE IF NOT EXISTS company_lark_credentials (
    company_id            UUID PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    app_id                TEXT NOT NULL,
    app_secret_encrypted  BYTEA NOT NULL,
    verification_token    TEXT NOT NULL,
    encrypt_key           TEXT,
    bot_open_id           TEXT,
    enabled               BOOLEAN NOT NULL DEFAULT true,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_company_lark_credentials_app
    ON company_lark_credentials(app_id);
