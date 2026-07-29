-- Rolling back drops the roster, including the backfilled default. Nothing
-- reads these rows at turn time in T-S1, so a company reverted to this point
-- runs exactly the agent it ran before the migration — there is no orphaned
-- reference to repair and no behaviour to restore.
--
-- agent_sources goes first: its FK to agents would refuse the drop otherwise.
DROP TABLE IF EXISTS agent_sources;

DROP INDEX IF EXISTS idx_agents_one_default;
DROP INDEX IF EXISTS idx_agents_company_name;

DROP TABLE IF EXISTS agents;
