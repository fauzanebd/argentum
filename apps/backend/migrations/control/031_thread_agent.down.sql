-- Reverse of 031_thread_agent (T-S2). Dropping the column drops its index with
-- it; the index is named here anyway so a partial state left by a failed
-- up-migration still comes back clean.
DROP INDEX IF EXISTS idx_usage_events_company_agent;

ALTER TABLE usage_events DROP COLUMN IF EXISTS agent_id;
ALTER TABLE agent_actions DROP COLUMN IF EXISTS agent_id;
ALTER TABLE conversation_threads DROP COLUMN IF EXISTS agent_id;
