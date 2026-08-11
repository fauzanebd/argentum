-- Reversing this discards the cookbook. Nothing is permanently lost: every row
-- was distilled from agent_actions and messages, which are untouched, so
-- re-applying the up migration and re-running the harvester rebuilds it — at
-- the cost of one embedding call per example.

DROP INDEX IF EXISTS idx_query_examples_company;
DROP TABLE IF EXISTS query_examples;
