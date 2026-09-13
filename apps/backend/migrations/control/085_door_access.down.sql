-- Reversible, and like 084's down it fails open in one place: dropping
-- `api_keys.agent_ids` makes every key reach every agent again, and dropping the
-- acknowledgement columns loses which channels an admin cleared. Both are
-- acceptable only because the migration travels with the code that reads it —
-- revert T-Z8 first, and a deployment with neither the columns nor the checks
-- is exactly the deployment that existed before this migration.
--
-- Run `down` without reverting T-Z8 and the repositories error on the missing
-- columns: keys fail to authenticate and channel turns fail to resolve, which
-- refuses rather than opens.
ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS disabled_reason;
ALTER TABLE watchers DROP COLUMN IF EXISTS disabled_reason;

ALTER TABLE agent_channel_bindings
    DROP COLUMN IF EXISTS restricted_ack_by,
    DROP COLUMN IF EXISTS restricted_ack_at;

ALTER TABLE api_keys DROP COLUMN IF EXISTS agent_ids;
