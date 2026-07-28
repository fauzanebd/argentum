DROP INDEX IF EXISTS idx_agent_actions_request;

ALTER TABLE agent_actions
    DROP COLUMN IF EXISTS request_id;
