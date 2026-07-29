-- The tenant agent roster (T-S1).
--
-- A customer with four jobs has one agent. Marketing, Ops, HR and Finance ask
-- incompatible questions of incompatible data through a single prompt, and the
-- only per-tenant customization before this table was a system prompt nobody
-- outside this repo can edit. This is the noun; T-S2 is the verb — nothing
-- reads these rows at turn time yet.
--
-- **Empty means unrestricted**, for `allowed_tools` and for `agent_sources`
-- alike. The alternative — empty means nothing — reads safer and behaves
-- worse: every tool added after an agent was created would be invisible to it,
-- and every new database connection would reach no agent until somebody
-- remembered to tick it. An agent that must be restricted carries an explicit
-- list. That rule is also what lets the backfill at the bottom create one
-- unrestricted default agent per existing company and change no behaviour.
--
-- An agent is persona + tools + sources. It is **not** an access boundary:
-- company membership stays the authorization boundary, so any member can open
-- the Finance agent even though the Finance agent physically cannot query the
-- HR source. Per-agent user grants are a follow-on, and this schema is shaped
-- so adding an `agent_grants` table later touches no column below.

CREATE TABLE IF NOT EXISTS agents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id     UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    -- Appended to the shared system prompt, never a replacement for it. The
    -- shared prompt carries the SQL-dialect rules, the anti-fabrication
    -- language T-16 fought for and the formatting contract; a customer-authored
    -- prompt that could replace it would be a self-service route back to the
    -- C-1 fabrication.
    persona_prompt TEXT NOT NULL DEFAULT '',
    -- Empty = every registered tool. TEXT[] rather than a join table for the
    -- same reason api_keys.scopes is one: the vocabulary is owned by the Go
    -- code, validated on write, and never queried across agents.
    allowed_tools  TEXT[] NOT NULL DEFAULT '{}',
    is_default     BOOLEAN NOT NULL DEFAULT false,
    enabled        BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- lower(name), not raw: "Finance" and "finance" in one picker is a support
-- ticket.
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_company_name
    ON agents(company_id, lower(name));

-- Exactly one default per company, enforced the way db_connections does it
-- (001_init.up.sql:33) rather than in application code.
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_one_default
    ON agents(company_id) WHERE is_default;

-- Empty set = every source the company owns. A source the company deletes
-- disappears from every agent's allowlist, which correctly widens the
-- remaining scope rather than leaving a dangling id behind.
CREATE TABLE IF NOT EXISTS agent_sources (
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, connection_id)
);

-- Backfill: one unrestricted default agent per existing company. No rows in
-- agent_sources, empty allowed_tools — so every existing tenant keeps exactly
-- the agent they have today, under a name.
--
-- ON CONFLICT DO NOTHING covers the one case that would otherwise abort the
-- migration: a company that somehow already holds an agent named "Analyst" or
-- a default. Neither can exist on a first run, and both are survivable.
INSERT INTO agents (company_id, name, description, is_default)
SELECT id, 'Analyst', 'General analytics assistant', true FROM companies
ON CONFLICT DO NOTHING;
