-- Reverses 038. The column goes before the table only for tidiness; neither
-- depends on the other.
ALTER TABLE agent_actions DROP COLUMN IF EXISTS mcp_server_id;
DROP TABLE IF EXISTS agent_mcp_servers;
