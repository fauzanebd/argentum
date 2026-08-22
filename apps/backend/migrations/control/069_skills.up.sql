-- T-K1: the tenant's own procedures, named and stored.
--
-- Numbered 069, claimed from schema_migrations at implementation time. The
-- ticket's own header said 067, written on 2026-08-21 before `T-H6` (067) and
-- `T-H12` (068) landed the same week — the same drift `038`'s comment records,
-- and the reason that comment exists.
--
-- A skill is a tenant-authored, named procedure with a stated trigger. Only
-- `name` and `when_to_use` travel in the prompt (T-K3's index); the body is
-- fetched by name when the model judges it applies (T-K4). That split is the
-- whole feature: thirty procedures cost thirty index lines a turn, not thirty
-- procedures.
--
-- **The body is trusted text and this table is where that starts.** It reaches
-- the model inside T-K2's frame, unfenced, on the basis that an authenticated
-- member of the company typed it — the same basis the persona and the company
-- profile already stand on. Nothing that arrives inside *content* may be
-- written here without a human saving it; T-K7's draft is a suggestion in a
-- form, and the save is the authorship event.
CREATE TABLE IF NOT EXISTS skills (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id   UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- Both of these ride in every turn's system prompt through T-K3, which is
    -- why the caps on them are enforced in the domain rather than only here:
    -- a CHECK constraint refuses a write with a Postgres error, and the tenant
    -- needs a sentence naming the field and the limit.
    --
    -- The constraints are here anyway, as the backstop. A cap enforced in one
    -- place is a cap until somebody adds a second writer.
    name         TEXT NOT NULL CHECK (length(name) > 0 AND length(name) <= 60),
    when_to_use  TEXT NOT NULL CHECK (length(when_to_use) > 0 AND length(when_to_use) <= 200),
    -- The part that does not travel unless the model asks for it, and therefore
    -- the part that can afford to be long.
    body         TEXT NOT NULL CHECK (length(body) > 0 AND length(body) <= 8000),
    -- Off is a first-class state: a procedure being revised should stop being
    -- offered without being deleted, because deleting it loses the audit trail
    -- of what the agent read last month.
    enabled      BOOLEAN NOT NULL DEFAULT true,
    -- 'tenant' or 'builtin:<key>'. T-K8's shipped skills and a tenant's own are
    -- one lookup and still distinguishable in an audit — the same shape
    -- db_connections.origin uses for a document warehouse.
    source       TEXT NOT NULL DEFAULT 'tenant',
    -- Who typed it. SET NULL rather than CASCADE, matching data_erasures'
    -- requested_by: a member who leaves must not take the company's procedures
    -- with them.
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One name per company, case-insensitively. `load_skill` takes a name rather
-- than a uuid — it is what the model reads off the index — so two skills a
-- model cannot tell apart is a tool call with no correct answer. Enforced
-- lower(), because "Weekly Report" and "weekly report" are the same procedure
-- to everyone except a byte comparison.
CREATE UNIQUE INDEX IF NOT EXISTS idx_skills_company_name
    ON skills(company_id, lower(name));

-- The index every turn reads: a company's enabled skills, ordered as T-K3
-- truncates them, so the composed prompt costs one index scan.
CREATE INDEX IF NOT EXISTS idx_skills_company_enabled
    ON skills(company_id, lower(name)) WHERE enabled;

-- The per-agent binding, shaped exactly like agent_mcp_servers (038).
--
-- **Empty means EVERY enabled company skill**, which is agent_sources' rule and
-- the opposite of agent_mcp_servers'. Locked in the ticket and repeated here
-- because the two tables look identical and behave differently: an MCP binding
-- is a capability grant to an external system we hold a token for, while a
-- skill grants nothing at all — it cannot widen a scope, and an irrelevant one
-- in an index is a wasted line the model will not open. Empty-means-none would
-- make every skill written after an agent was created invisible to it, which is
-- the failure AllowsTool's own comment describes.
CREATE TABLE IF NOT EXISTS agent_skills (
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_id UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, skill_id)
);

-- Deleting a skill has to take its bindings with it, and skill_id is not the
-- leading column of the composite key — 038's reasoning, unchanged.
CREATE INDEX IF NOT EXISTS idx_agent_skills_skill ON agent_skills(skill_id);
