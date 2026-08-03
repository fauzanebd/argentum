-- Reversing 010 loses which model each usage event was billed against, so spend
-- can still be totalled but no longer split by model.
DROP INDEX IF EXISTS idx_usage_events_company_model;
ALTER TABLE usage_events DROP COLUMN IF EXISTS model;
