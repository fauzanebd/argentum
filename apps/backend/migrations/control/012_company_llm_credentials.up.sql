-- Per-tenant LLM credentials. One row per (company, tier).
-- Tiers: 'primary' (main agent LLM), 'light' (guardrails/classifier/summary),
-- 'embedding' (table-picker vectors). Any field NULL = fall back to env
-- default for that field. No row at all = full env-default for that tier.

CREATE TABLE IF NOT EXISTS company_llm_credentials (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    tier              TEXT NOT NULL CHECK (tier IN ('primary', 'light', 'embedding')),
    interface         TEXT,
    model             TEXT,
    base_url          TEXT,
    api_key_encrypted BYTEA,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, tier)
);

CREATE INDEX IF NOT EXISTS idx_company_llm_credentials_company
    ON company_llm_credentials(company_id);
