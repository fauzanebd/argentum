-- T-M1: the tenant's own MCP server, registered as a source of tools.
--
-- This is Argentum as the *client* — we hold the customer's token and call
-- their tools. T-14 is the same protocol pointed the other way (we serve, their
-- agent calls us) and shares no code with this table. The one-line test for
-- which is which is who holds the credential; here, it is us, which is why
-- auth_encrypted exists at all.
--
-- An MCP server is a source of TOOLS, not a source of rows (locked decision 1).
-- It never becomes a db_connections row and never implements db.Driver: there
-- is no SQL to execute and no information_schema to read, and synthesising both
-- would put a lie in the abstraction run_sql's safety rests on.
--
-- Nothing here reaches a turn. Registration, discovery and review are this
-- ticket; tool calls are T-M2, and the agent↔server binding is T-M3.
CREATE TABLE IF NOT EXISTS mcp_servers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    -- The URL is attacker-controlled input (locked decision 4). It is stored as
    -- the tenant typed it and re-checked against the egress guard on every
    -- outbound request, because a hostname that resolved publicly at save time
    -- can resolve to 169.254.169.254 an hour later.
    url            TEXT NOT NULL,
    -- 'http' (streamable HTTP) or 'sse'. No stdio, ever: stdio means spawning
    -- the tenant's process inside our worker, which is arbitrary code execution
    -- wearing a config field (locked decision 3).
    transport      TEXT NOT NULL,
    -- Encrypted at rest exactly as db_connections.dsn_encrypted is, with the
    -- same cipher, and for the same reason: it is a bearer credential for a
    -- system we do not own. Null for a server that needs no token.
    auth_encrypted BYTEA,
    enabled        BOOLEAN NOT NULL DEFAULT true,
    -- The last discovery attempt, and why it failed. A server that is down at
    -- 4pm is not a configuration error: the row saves, the error shows.
    last_probed_at TIMESTAMPTZ,
    probe_error    TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_servers_company_name
    ON mcp_servers(company_id, lower(name));

-- The reviewed tool list. Rows are written by discovery and approved by an
-- admin; nothing is callable until approved is true.
CREATE TABLE IF NOT EXISTS mcp_server_tools (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id      UUID NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    -- The tenant's own name for the tool, stored raw. Namespacing happens on
    -- the way out (T-M2) because the tenant's server is the one that has to
    -- recognise what we ask it for.
    tool_name      TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    input_schema   JSONB NOT NULL,
    -- Default false, which is the opposite of agents.allowed_tools' rule and is
    -- meant to be (locked decision 2). An empty allowlist there means "every
    -- tool", because the failure is a scoped agent that cannot answer; here an
    -- unclassified tool that ran would be a write against a system we do not
    -- own, and the two failure directions are not symmetric.
    read_only      BOOLEAN NOT NULL DEFAULT false,
    approved       BOOLEAN NOT NULL DEFAULT false,
    -- The hash of (description, input_schema) at approval time. Discovery
    -- compares against it, so a server that quietly rewrites a tool's
    -- description — the cheapest injection vector this track opens, because a
    -- description is text that enters the agent's context — shows as drifted
    -- rather than being adopted.
    approved_digest TEXT NOT NULL DEFAULT '',
    discovered_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (server_id, tool_name)
);

CREATE INDEX IF NOT EXISTS idx_mcp_server_tools_server ON mcp_server_tools(server_id);
