-- Allowlist of Slack user ids per company. Mirrors allowed_lark_users.
-- Without it, anyone who can reach the bot can query the company's data.
-- A Slack user id is per-workspace, but the same human can be allowed by
-- multiple companies via separate apps, so the primary key is composite.

CREATE TABLE IF NOT EXISTS allowed_slack_users (
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    slack_user_id  TEXT NOT NULL,
    label          TEXT,
    added_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (company_id, slack_user_id)
);

CREATE INDEX IF NOT EXISTS idx_allowed_slack_users_user_id
    ON allowed_slack_users(slack_user_id);
