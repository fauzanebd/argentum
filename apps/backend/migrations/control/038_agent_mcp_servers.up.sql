-- T-M2: the binding that makes a tenant's MCP server reachable from a turn, and
-- the audit column that records which server a call went to.
--
-- Numbered 038, taken from schema_migrations at implementation time: the
-- ticket's own header says 034, written before T-M1 (037), T-S4 (033) and the
-- source-profile / template-key migrations landed ahead of it. The tree was at
-- 037 when this was written.
--
-- Unlike agent_sources, EMPTY MEANS NONE (locked decision 5). A database the
-- company connected is already theirs and an agent may reach all of them by
-- default; an MCP server takes actions in a third-party system we hold a token
-- for, so it reaches an agent only when someone said so. The asymmetry is
-- deliberate and it is enforced in agentscope.Scope.AllowsMCPServer, not here.
CREATE TABLE IF NOT EXISTS agent_mcp_servers (
    agent_id  UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, server_id)
);

-- Deleting a server has to take its bindings with it, and the server_id side of
-- the composite key is not the leading column, so it gets its own index for the
-- cascade and for "which agents reach this server".
CREATE INDEX IF NOT EXISTS idx_agent_mcp_servers_server ON agent_mcp_servers(server_id);

-- Which MCP server a tool call went to. No foreign key, for the same reason
-- agent_actions.thread_id and .agent_id carry none: the audit log is
-- append-only and outlives the rows it names — "which server ran this" is a
-- question asked about deleted servers more often than about live ones. Null for
-- every row a non-MCP tool writes, which is all of them until this ticket.
ALTER TABLE agent_actions ADD COLUMN IF NOT EXISTS mcp_server_id UUID;
