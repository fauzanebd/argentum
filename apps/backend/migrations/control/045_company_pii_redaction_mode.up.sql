-- T-07b: the tenant's policy for the output redaction rules.
--
-- The rules — emails, phone numbers, NIK/KTP, SSN, card numbers — have existed
-- since the guardrails config was written and have never run: agent-sdk-go
-- applies output guardrails only on its blocking path, and every chat turn
-- streams. Switching them on globally would blank the answer to "give me the
-- top 10 customers with their emails", which is an ordinary BI question and the
-- exact failure finding Q-4 recorded. So activation ships with the control that
-- makes it survivable.
--
-- strict     — redact everything (the default, and what every existing row gets)
-- contact_ok — emails and phone numbers pass; identity documents and cards do not
-- off        — no redaction at all
--
-- A CHECK rather than an enum type: three values that a later ticket may want to
-- extend, and adding a value to a Postgres enum inside a transaction is the kind
-- of migration that fails on a Friday.
ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS pii_redaction_mode TEXT NOT NULL DEFAULT 'strict';

ALTER TABLE companies
    DROP CONSTRAINT IF EXISTS companies_pii_redaction_mode_check;

ALTER TABLE companies
    ADD CONSTRAINT companies_pii_redaction_mode_check
    CHECK (pii_redaction_mode IN ('strict', 'contact_ok', 'off'));
