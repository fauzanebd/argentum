-- T-B2: what one connected source looks like it is for.
--
-- Per-source rather than per-company: a tenant with a warehouse and a CRM has
-- two different answers, and T-B4 drafts a persona from only the sources that
-- agent may reach. The company-level draft is folded from these rows at read
-- time and is never stored here — a draft that persisted would be a second
-- profile able to disagree with company_profiles, which is the one the agent
-- actually reads.
--
-- Nothing in this table reaches a turn on its own. It is the raw material the
-- tenant reviews in Settings, and it becomes the agent's view of the business
-- only when somebody presses Apply (locked decision 2).
CREATE TABLE IF NOT EXISTS source_profiles (
    connection_id UUID PRIMARY KEY REFERENCES db_connections(id) ON DELETE CASCADE,
    company_id    UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- The one-line industry label the fold puts on the company draft
    -- ("grocery retail", "3PL logistics"). Not in the ticket's DDL sketch and
    -- added deliberately: the acceptance asks the draft to name a plausible
    -- industry, and deriving one from prose at fold time would be a second
    -- inference — in Go, without a model, over text the model already had the
    -- context to label.
    industry      TEXT NOT NULL DEFAULT '',
    summary       TEXT NOT NULL DEFAULT '',
    -- [{"table":"stock_movements","means":"inventory in/out per store"}, …]
    -- Every entry's table name is checked against the introspected schema
    -- before it is stored, so a row here always names tables that exist.
    entities      JSONB NOT NULL DEFAULT '[]',
    -- Hash of the introspected table+column names. Re-running against an
    -- unchanged schema must not spend a second LLM call.
    schema_fingerprint TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    inferred_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_source_profiles_company ON source_profiles(company_id);
