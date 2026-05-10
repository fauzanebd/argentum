ALTER TABLE usage_events
    ADD COLUMN IF NOT EXISTS model TEXT;

CREATE INDEX IF NOT EXISTS idx_usage_events_company_model
    ON usage_events(company_id, model)
    WHERE model IS NOT NULL;
