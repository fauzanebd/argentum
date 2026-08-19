-- Down for 062. The index goes first: dropping the column would take it with
-- it, and a down migration whose steps depend on cascade behaviour is one that
-- breaks the day somebody adds a second index.

DROP INDEX IF EXISTS idx_agent_actions_document_tainted;

ALTER TABLE agent_actions DROP COLUMN IF EXISTS document_tainted;
